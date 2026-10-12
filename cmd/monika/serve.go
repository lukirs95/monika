package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lukirs95/monika/internal/action/executor"
	"github.com/lukirs95/monika/internal/api"
	"github.com/lukirs95/monika/internal/auth"
	"github.com/lukirs95/monika/internal/config"
	"github.com/lukirs95/monika/internal/core"
	"github.com/lukirs95/monika/internal/core/evaluation"
	"github.com/lukirs95/monika/internal/core/inventory"
	"github.com/lukirs95/monika/internal/core/supervisor"
	"github.com/lukirs95/monika/internal/forward"
	"github.com/lukirs95/monika/internal/history/influx"
	"github.com/lukirs95/monika/internal/preset"
	"github.com/lukirs95/monika/internal/retention"
	"github.com/lukirs95/monika/internal/secret"
	"github.com/lukirs95/monika/internal/store/postgres"
	"github.com/lukirs95/monika/sdk"
	"github.com/lukirs95/monika/sdk/vocab"
)

// Fristen der Phasen beim Herunterfahren (bootstrap.md, K4), zusammen höchstens 30 s.
const (
	phaseAPI      = 5 * time.Second
	phaseActions  = 5 * time.Second
	phaseSources  = 10 * time.Second
	phaseCore     = 3 * time.Second
	phaseServices = 5 * time.Second
	phaseDatabase = 2 * time.Second
	startTimeout  = time.Minute // Datenbank, Sperre, Migrationen, Laden
)

// service ist ein laufender Dienst mit eigenem ctx: Das Herunterfahren bricht ihn in seiner
// Phase ab und wartet auf sein Ende.
type service struct {
	name   string
	cancel context.CancelFunc
	done   chan struct{}
}

// monika hält die laufenden Dienste.
type monika struct {
	log    *slog.Logger
	exited chan error // ein Dienst ist vorzeitig zurückgekehrt (Fehler oder nicht)
}

// start betreibt run in eigener Goroutine mit eigenem ctx. Kehrt es zurück, bevor das
// Herunterfahren es abbricht, stößt das das Herunterfahren an (statt errgroup, K4).
func (m *monika) start(name string, run func(context.Context) error) *service {
	ctx, cancel := context.WithCancel(context.Background())
	s := &service{name: name, cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(s.done)
		err := run(ctx)
		if ctx.Err() != nil { // regulär beendet
			if err != nil {
				m.log.Error("service stopped with error", "service", name, "err", err)
			}
			return
		}
		if err == nil {
			err = errors.New("returned unexpectedly")
		}
		select {
		case m.exited <- fmt.Errorf("%s: %w", name, err):
		default:
		}
	}()
	return s
}

// stop ist eine Phase: Dienste abbrechen und höchstens bis zur Frist auf sie warten.
func (m *monika) stop(phase string, timeout time.Duration, services ...*service) {
	deadline := time.After(timeout)
	for _, s := range services {
		if s != nil {
			s.cancel()
		}
	}
	for _, s := range services {
		if s == nil {
			continue
		}
		select {
		case <-s.done:
		case <-deadline:
			m.log.Warn("service did not stop in time", "phase", phase, "service", s.name, "timeout", timeout)
		}
	}
}

// serve startet Monika in der Reihenfolge aus flows.md, wartet auf ein Signal oder einen
// Dienstfehler und fährt dann in sechs Phasen herunter.
func serve(ctx context.Context, cfg config.Config, stdout io.Writer) int {
	log := newLogger(cfg, stdout)
	slog.SetDefault(log)
	for _, w := range cfg.Warnings {
		log.Warn(w)
	}
	log.Info("starting monika", "version", versionString(), "listen", cfg.Listen, "public_url", cfg.PublicURL.String(),
		"influx", cfg.Influx.URL != nil, "scheduler_concurrency", cfg.SchedulerConcurrency)

	m := &monika{log: log, exited: make(chan error, 1)}
	startCtx, cancel := context.WithTimeout(ctx, startTimeout)
	defer cancel()
	pool, lock, err := openDatabase(startCtx, cfg)
	if err != nil {
		log.Error("start failed", "err", err)
		return 1
	}
	defer func() {
		rctx, cancel := context.WithTimeout(context.Background(), phaseDatabase)
		defer cancel()
		if err := lock.Release(rctx); err != nil {
			log.Warn("release instance lock", "err", err)
		}
		pool.Close()
	}()
	d, err := build(startCtx, cfg, pool, log)
	if err != nil {
		log.Error("start failed", "err", err)
		return 1
	}

	// Dienste in der Reihenfolge aus flows.md; die API öffnet zuletzt.
	watch := m.start("instance lock", lock.Watch)
	sup := m.start("supervisor", func(ctx context.Context) error { return d.supervisor.Run(ctx, d.core) })
	loop := m.start("core", d.core.Run)
	actions := []*service{m.start("executor", d.executor.Run), m.start("preset runner", d.presets.Run)}
	services := []*service{m.start("forwarder", d.forwarder.Run), m.start("retention", d.retention.Run), m.start("auth", d.auth.Run)}
	if d.influx != nil {
		services = append(services, m.start("influx writer", d.influx.Run))
	}
	streams := m.start("event distributor", d.api.Run)
	httpServer := &http.Server{
		Handler:           d.handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute, // kein WriteTimeout: Ereignisströme setzen ihre Deadline selbst
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(startCtx, "tcp", cfg.Listen)
	if err != nil {
		log.Error("start failed", "err", fmt.Errorf("listen on %s: %w", cfg.Listen, err))
		m.shutdown(streams, nil, actions, sup, loop, services, watch)
		return 1
	}
	listener := m.start("http", func(context.Context) error {
		if err := httpServer.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	})
	log.Info("monika is running", "listen", ln.Addr().String())

	code := 0
	select {
	case <-ctx.Done():
		log.Info("shutting down", "reason", "signal")
	case err := <-m.exited:
		log.Error("shutting down", "reason", "service failed", "err", err)
		code = 1
	}
	m.shutdown(streams, func(ctx context.Context) error {
		err := httpServer.Shutdown(ctx)
		<-listener.done
		return err
	}, actions, sup, loop, services, watch)
	log.Info("monika stopped")
	return code
}

// shutdown fährt in sechs Phasen herunter (bootstrap.md); Phase 6 (Sperre, Pool) erledigt serve.
func (m *monika) shutdown(streams *service, httpShutdown func(context.Context) error, actions []*service,
	sup, loop *service, services []*service, watch *service) {
	// Phase 1: erst die Ereignisströme schließen, dann den HTTP-Server (V7).
	start := time.Now()
	m.stop("api", phaseAPI, streams)
	if httpShutdown != nil {
		ctx, cancel := context.WithTimeout(context.Background(), phaseAPI-time.Since(start))
		if err := httpShutdown(ctx); err != nil {
			m.log.Warn("http shutdown", "err", err)
		}
		cancel()
	}
	m.stop("actions", phaseActions, actions...) // Phase 2: F8
	m.stop("sources", phaseSources, sup)        // Phase 3
	m.stop("core", phaseCore, loop)             // Phase 4: Rest, letztes SaveHealth
	m.stop("services", phaseServices, services...)
	m.stop("database", phaseDatabase, watch) // Phase 6, Freigabe in serve
}

func newLogger(cfg config.Config, w io.Writer) *slog.Logger {
	opts := &slog.HandlerOptions{Level: cfg.LogLevel}
	if cfg.LogFormat == config.LogText {
		return slog.New(slog.NewTextHandler(w, opts))
	}
	return slog.New(slog.NewJSONHandler(w, opts))
}

// openDatabase verbindet, nimmt die Einzelinstanz-Sperre und spielt die Migrationen ein.
func openDatabase(ctx context.Context, cfg config.Config) (*pgxpool.Pool, *postgres.Lock, error) {
	connCfg, err := pgx.ParseConfig(cfg.DatabaseURL.Reveal())
	if err != nil {
		return nil, nil, errors.New("parse MONIKA_DATABASE_URL") // ohne den Text: er enthält das Passwort
	}
	pool, err := postgres.Open(ctx, cfg.DatabaseURL.Reveal())
	if err != nil {
		return nil, nil, err
	}
	lock, err := postgres.AcquireLock(ctx, connCfg)
	if err != nil {
		pool.Close()
		return nil, nil, err
	}
	if err := postgres.Migrate(ctx, pool); err != nil {
		_ = lock.Release(ctx)
		pool.Close()
		return nil, nil, fmt.Errorf("migrate: %w", err)
	}
	return pool, lock, nil
}

// deps sind die verdrahteten Komponenten.
type deps struct {
	auth       *auth.Service
	supervisor *supervisor.Supervisor
	core       *core.Core
	executor   *executor.Executor
	presets    *preset.Runner
	forwarder  *forward.Forwarder
	retention  *retention.Cleaner
	influx     *influx.Writer // nil ohne MONIKA_INFLUX_URL
	api        *api.Server
	handler    http.Handler
}

// build erzeugt und verdrahtet alle Komponenten und lädt ihren Stand (flows.md, Start).
func build(ctx context.Context, cfg config.Config, pool *pgxpool.Pool, log *slog.Logger) (*deps, error) {
	store := postgres.New(pool)
	secrets, err := secret.NewService(store, cfg.Keys, log, store.TOTPVault())
	if err != nil {
		return nil, err
	}
	if _, err := secrets.VerifyAndRotate(ctx); err != nil {
		return nil, fmt.Errorf("verify secrets: %w", err)
	}

	d := &deps{}
	d.auth = auth.New(auth.Config{Store: store, Crypter: secrets, SessionIdle: cfg.SessionIdle, SessionMax: cfg.SessionMax, Log: log})
	if err := d.auth.Restore(ctx); err != nil {
		return nil, err
	}
	if err := ensureAdmin(ctx, d.auth, cfg.Admin, log); err != nil {
		return nil, err
	}

	knownDriver := func(name string) bool { _, ok := sdk.Lookup(name); return ok }
	inv := inventory.New(store, sdk.Lookup, log)
	if err := inv.Restore(ctx); err != nil {
		return nil, err
	}
	ev := evaluation.New(store, evaluation.RegistryFactTypes, knownDriver, log)
	if err := ev.Restore(ctx); err != nil {
		return nil, err
	}
	d.supervisor = supervisor.New(supervisor.Config{
		Secrets: secrets, Scheduler: supervisor.NewScheduler(cfg.SchedulerConcurrency), Drivers: sdk.Lookup, Log: log,
	})
	d.core = core.New(core.Config{Inventory: inv, Evaluator: ev, Supervisor: d.supervisor, Secrets: secrets, DB: pool, Log: log})

	d.executor = executor.New(executor.Config{Core: d.core, Sources: d.supervisor, Store: store, Drivers: sdk.Lookup, Log: log})
	if n, err := d.executor.FinishInterrupted(ctx); err != nil {
		return nil, err
	} else if n > 0 {
		log.Warn("finished runs interrupted by a restart", "runs", n)
	}
	d.presets = preset.NewRunner(preset.Config{Store: store, Executor: d.executor, Core: d.core, Permissions: d.auth, Log: log})
	d.forwarder = forward.New(forward.Config{Store: store, Core: d.core, Secrets: secrets, PublicURL: cfg.PublicURL, Log: log})
	d.retention = retention.New(retention.Config{
		Store: store, Findings: cfg.Retention.Findings, Runs: cfg.Retention.Runs, Audit: cfg.Retention.Audit, Log: log,
	})

	d.api, err = api.New(api.Config{
		Auth: d.auth, Admin: d.auth, Core: d.core, Executor: d.executor, Presets: d.presets, Forwarder: d.forwarder,
		Secrets: secrets, History: store, PublicURL: cfg.PublicURL, TrustedProxies: cfg.TrustedProxies,
		Drivers: sdk.Drivers, Log: log,
	})
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.Handle(api.Prefix+"/", d.api.Handler())
	d.handler = mux

	if in := cfg.Influx; in.URL != nil {
		d.influx = influx.New(influx.Config{
			URL: in.URL, Token: in.Token.Reveal(), Org: in.Org, Bucket: in.Bucket, Interval: in.Interval,
			Core: d.core, Field: vocab.Lookup, Gauges: d.api.Gauges, Log: log,
		})
	}
	return d, nil
}

// ensureAdmin legt den initialen Admin an, wenn es noch keinen Benutzer gibt (M8).
func ensureAdmin(ctx context.Context, a *auth.Service, admin *config.AdminBootstrap, log *slog.Logger) error {
	if admin == nil {
		return nil
	}
	created, err := a.EnsureAdmin(ctx, admin.Username, admin.Password.Reveal())
	switch {
	case err != nil:
		return fmt.Errorf("initial admin: %w", err)
	case created:
		log.Info("initial admin created; set up TOTP at the first login", "username", admin.Username)
	default:
		log.Warn("users exist; MONIKA_ADMIN_USER and MONIKA_ADMIN_PASSWORD can be removed")
	}
	return nil
}
