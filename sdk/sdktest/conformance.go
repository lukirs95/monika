package sdktest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/lukirs95/monika/sdk"
	"github.com/lukirs95/monika/sdk/vocab"
)

// Options steuern Conformance.
type Options struct {
	Timeout     time.Duration // Wartezeit auf Connected und Snapshot; Standard 10 s
	StopTimeout time.Duration // so schnell muss Run nach Abbruch zurückkehren; Standard 10 s
	Commands    []sdk.Command // werden gleichzeitig ausgeführt, um Execute auf Nebenläufigkeit zu prüfen
}

// Conformance prüft einen Treiber gegen die Regeln des SDK-Vertrags, mit einer Attrappe seines
// Geräts hinter cfg: gültiger Katalog; gültiges ConfigSchema, zu dem cfg.Config passt; nach Connected zuerst ein vollständiger Snapshot; gültige
// Elemente, Links, Fakten und Alarme; Lebenszeichen innerhalb des Dreifachen von Heartbeat;
// stabile external_ids über zwei Läufe; Run kehrt nach Abbruch zügig zurück; Execute verträgt
// gleichzeitige Aufrufe.
func Conformance(t testing.TB, d sdk.Driver, cfg sdk.SourceConfig, opts Options) {
	t.Helper()
	for _, err := range check(t.Context(), d, cfg, opts) {
		t.Error(err)
	}
}

func check(ctx context.Context, d sdk.Driver, cfg sdk.SourceConfig, opts Options) []error {
	if opts.Timeout == 0 {
		opts.Timeout = 10 * time.Second
	}
	if opts.StopTimeout == 0 {
		opts.StopTimeout = 10 * time.Second
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if cfg.Scheduler == nil {
		cfg.Scheduler = Scheduler{}
	}
	if err := d.Validate(); err != nil {
		return []error{fmt.Errorf("catalog: %w", err)}
	}
	if err := checkConfig(d.ConfigSchema, cfg.Config); err != nil {
		return []error{fmt.Errorf("config: %w", err)}
	}

	var errs []error
	first, err := run(ctx, d, cfg, opts, true)
	errs = append(errs, err...)
	if first == nil {
		return errs
	}
	second, err := run(ctx, d, cfg, opts, false)
	errs = append(errs, err...)
	if second != nil && !slices.Equal(externalIDs(*first), externalIDs(*second)) {
		errs = append(errs, fmt.Errorf("external IDs differ between two runs: %v, then %v", externalIDs(*first), externalIDs(*second)))
	}
	return errs
}

// run startet eine Source, prüft ihren ersten Snapshot und, wenn full gesetzt ist, auch
// Lebenszeichen, Updates und Execute; danach bricht es ab. Es liefert den ersten Snapshot.
func run(ctx context.Context, d sdk.Driver, cfg sdk.SourceConfig, opts Options, full bool) (*sdk.Snapshot, []error) {
	src, err := d.New(cfg)
	if err != nil {
		return nil, []error{fmt.Errorf("driver constructor: %w", err)}
	}
	hb := src.Heartbeat()
	var errs []error
	if hb < 0 {
		errs = append(errs, fmt.Errorf("Heartbeat() = %s, must not be negative", hb))
	}

	rec := NewReporter()
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		defer func() {
			if p := recover(); p != nil {
				done <- fmt.Errorf("Run panicked: %v", p)
			}
		}()
		done <- src.Run(runCtx, rec)
	}()

	waitCtx, stopWait := context.WithTimeout(ctx, opts.Timeout)
	defer stopWait()
	_, ci, err := rec.WaitFor(waitCtx, 0, Is(CallConnected))
	if err != nil {
		cancel()
		return nil, append(errs, fmt.Errorf("no Connected within %s", opts.Timeout))
	}
	c, si, err := rec.WaitFor(waitCtx, ci+1, func(c Call) bool { return c.Kind != CallHeartbeat })
	if err != nil || c.Kind != CallSnapshot {
		cancel()
		return nil, append(errs, fmt.Errorf("after Connected, the first report must be a full Snapshot, got %q", c.Kind))
	}
	snap := *c.Snapshot
	errs = append(errs, checkSnapshot(d, snap)...)

	if full {
		if hb > 0 {
			beatCtx, stopBeat := context.WithTimeout(ctx, 3*hb)
			if _, _, err := rec.WaitFor(beatCtx, si+1, func(Call) bool { return true }); err != nil {
				errs = append(errs, fmt.Errorf("no sign of life within 3 × Heartbeat (%s) after the Snapshot", 3*hb))
			}
			stopBeat()
		}
		errs = append(errs, execute(ctx, src, opts.Commands)...)
		for _, u := range rec.Updates() {
			errs = append(errs, checkReports(d, snap, u.Facts, u.Alarms)...)
		}
	}

	cancel()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			errs = append(errs, fmt.Errorf("Run after cancel: %w", err))
		}
	case <-time.After(opts.StopTimeout):
		errs = append(errs, fmt.Errorf("Run did not return within %s after its context was canceled", opts.StopTimeout))
	}
	return &snap, errs
}

// execute ruft alle Befehle gleichzeitig; Fehler aus Execute sind erlaubt, Panics nicht.
func execute(ctx context.Context, src sdk.Source, cmds []sdk.Command) []error {
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)
	for _, c := range cmds {
		wg.Go(func() {
			defer func() {
				if p := recover(); p != nil {
					mu.Lock()
					errs = append(errs, fmt.Errorf("Execute(%s %q) panicked: %v", c.Action, c.ExternalID, p))
					mu.Unlock()
				}
			}()
			_, _ = src.Execute(ctx, c) // die Antwort des Geräts ist hier egal
		})
	}
	wg.Wait()
	return errs
}

func checkSnapshot(d sdk.Driver, s sdk.Snapshot) []error {
	var errs []error
	ids := map[string]bool{"": true}
	for _, e := range s.Elements {
		if ids[e.ExternalID] {
			errs = append(errs, fmt.Errorf("element %q reported twice", e.ExternalID))
		}
		ids[e.ExternalID] = true
		if err := d.CheckElement(e); err != nil {
			errs = append(errs, err)
		}
		if e.GlobalRef != "" {
			errs = append(errs, checkOwnRef(d, e.GlobalRef)...)
		}
	}
	for _, e := range s.Elements {
		if !ids[e.ParentID] {
			errs = append(errs, fmt.Errorf("element %q: unknown parent %q", e.ExternalID, e.ParentID))
		}
	}
	if s.GlobalRef != "" {
		errs = append(errs, checkOwnRef(d, s.GlobalRef)...)
	}
	for _, l := range s.Links {
		errs = append(errs, checkLink(l, ids)...)
	}
	return append(errs, checkReports(d, s, s.Facts, s.Alarms)...)
}

// checkReports prüft Fakten und Alarme gegen die Elemente des Snapshots.
func checkReports(d sdk.Driver, s sdk.Snapshot, facts map[string]sdk.Facts, alarms map[string][]sdk.Alarm) []error {
	ids := map[string]bool{"": true}
	for _, e := range s.Elements {
		ids[e.ExternalID] = true
	}
	var errs []error
	for id, f := range facts {
		if !ids[id] {
			errs = append(errs, fmt.Errorf("facts for unknown element %q", id))
		}
		for key, v := range f {
			t, ok := d.FactType(key)
			switch {
			case key == vocab.Present:
				errs = append(errs, fmt.Errorf("element %q: fact %q is reserved", id, key))
			case !ok:
				errs = append(errs, fmt.Errorf("element %q: fact %q is neither in the vocabulary nor in the catalog", id, key))
			case !sdk.ValueOK(t, v):
				errs = append(errs, fmt.Errorf("element %q: fact %q must be %s, got %T", id, key, t, v))
			}
		}
	}
	for id, list := range alarms {
		if !ids[id] {
			errs = append(errs, fmt.Errorf("alarms for unknown element %q", id))
		}
		for _, a := range list {
			if a.Code == "" || !slices.Contains([]sdk.Severity{sdk.SeverityInfo, sdk.SeverityWarning, sdk.SeverityError}, a.Severity) {
				errs = append(errs, fmt.Errorf("element %q: alarm %+v needs a code and a valid severity", id, a))
			}
		}
	}
	return errs
}

func checkLink(l sdk.Link, ids map[string]bool) []error {
	var errs []error
	if !slices.Contains([]string{sdk.LinkFeeds, sdk.LinkUses, sdk.LinkRepresents}, l.Type) {
		errs = append(errs, fmt.Errorf("link %+v: unknown type %q", l, l.Type))
	}
	if l.Type == sdk.LinkRepresents && l.To.GlobalRef == "" {
		errs = append(errs, fmt.Errorf("link %+v: represents must point to a global_ref", l))
	}
	for _, ep := range []sdk.Endpoint{l.From, l.To} {
		switch {
		case ep.GlobalRef != "" && ep.ExternalID != "":
			errs = append(errs, fmt.Errorf("link %+v: an endpoint is either an external ID or a global_ref", l))
		case ep.GlobalRef != "":
			if ns, _, ok := strings.Cut(ep.GlobalRef, ":"); !ok || ns == "" {
				errs = append(errs, fmt.Errorf("link %+v: global_ref %q needs a namespace", l, ep.GlobalRef))
			}
		case !ids[ep.ExternalID]:
			errs = append(errs, fmt.Errorf("link %+v: unknown element %q", l, ep.ExternalID))
		}
	}
	return errs
}

// checkOwnRef prüft eine global_ref, die der Treiber als eigene Identität setzt.
func checkOwnRef(d sdk.Driver, ref string) []error {
	ns, id, ok := strings.Cut(ref, ":")
	if !ok || ns == "" || id == "" {
		return []error{fmt.Errorf("global_ref %q must be <namespace>:<id>", ref)}
	}
	if owner, known := vocab.NamespaceOwner(ns); !known || owner != d.Name {
		return []error{fmt.Errorf("global_ref %q: namespace %q does not belong to driver %q", ref, ns, d.Name)}
	}
	return nil
}

func externalIDs(s sdk.Snapshot) []string {
	ids := make([]string, 0, len(s.Elements))
	for _, e := range s.Elements {
		ids = append(ids, e.ExternalID)
	}
	slices.Sort(ids)
	return ids
}

// checkConfig prüft, dass ConfigSchema ein gültiges JSON-Schema ist und die Konfiguration des
// Tests dazu passt; der Core lehnt sonst jede Quelle mit invalid_config ab.
func checkConfig(schema, config json.RawMessage) error {
	if len(schema) == 0 {
		return nil
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schema))
	if err != nil {
		return fmt.Errorf("ConfigSchema is not JSON: %w", err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("config.json", doc); err != nil {
		return fmt.Errorf("ConfigSchema: %w", err)
	}
	compiled, err := c.Compile("config.json")
	if err != nil {
		return fmt.Errorf("ConfigSchema: %w", err)
	}
	if len(config) == 0 {
		config = json.RawMessage(`{}`)
	}
	v, err := jsonschema.UnmarshalJSON(bytes.NewReader(config))
	if err != nil {
		return fmt.Errorf("config is not JSON: %w", err)
	}
	if err := compiled.Validate(v); err != nil {
		return fmt.Errorf("config does not match ConfigSchema: %w", err)
	}
	return nil
}
