// Package config liest die Bootstrap-Konfiguration von Monika aus der Umgebung und einer .env-Datei
// (docs/contracts/bootstrap.md). Alle übrigen Geheimnisse liegen im Secret-Store.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/lukirs95/monika/internal/secret"
)

// LogFormat ist das Format der Log-Ausgabe.
type LogFormat string

const (
	LogJSON LogFormat = "json"
	LogText LogFormat = "text"
)

// Config ist die geprüfte Bootstrap-Konfiguration.
type Config struct {
	Listen               string
	PublicURL            *url.URL
	TrustedProxies       []netip.Prefix
	SessionIdle          time.Duration
	SessionMax           time.Duration
	SchedulerConcurrency int
	LogLevel             slog.Level
	LogFormat            LogFormat
	DatabaseURL          Secret
	Keys                 secret.Keys // MONIKA_MASTER_KEY und MONIKA_MASTER_KEY_PREVIOUS
	Influx               InfluxConfig
	Retention            RetentionConfig
	Admin                *AdminBootstrap // nil ohne MONIKA_ADMIN_*

	// Warnings sind Hinweise, die den Start nicht verhindern; main loggt sie, sobald der Logger
	// steht (z. B. .env für andere lesbar, öffentliche URL ohne https).
	Warnings []string
}

// InfluxConfig ist die Verbindung zu InfluxDB; ohne URL schreibt Monika keine Zeitreihen.
type InfluxConfig struct {
	URL      *url.URL
	Token    Secret
	Bucket   string
	Org      string
	Interval time.Duration
}

// RetentionConfig ist die Aufbewahrung des Verlaufs; 0 heißt „nie löschen“.
type RetentionConfig struct {
	Findings time.Duration
	Runs     time.Duration
	Audit    time.Duration
}

// AdminBootstrap ist der initiale Admin; angelegt nur, wenn es noch keinen Benutzer gibt.
type AdminBootstrap struct {
	Username string
	Password Secret
}

// EnvFile liefert den Pfad der .env-Datei: MONIKA_ENV_FILE aus environ, sonst ./.env.
func EnvFile(environ []string) string {
	for _, kv := range environ {
		if v, ok := strings.CutPrefix(kv, "MONIKA_ENV_FILE="); ok && v != "" {
			return v
		}
	}
	return ".env"
}

// known sind alle Variablen, die Monika liest; unbekannte MONIKA_* brechen den Start ab.
var known = map[string]bool{
	"MONIKA_ENV_FILE": true, "MONIKA_MASTER_KEY": true, "MONIKA_MASTER_KEY_PREVIOUS": true,
	"MONIKA_DATABASE_URL": true, "MONIKA_PUBLIC_URL": true, "MONIKA_LISTEN": true,
	"MONIKA_TRUSTED_PROXIES": true, "MONIKA_SESSION_IDLE": true, "MONIKA_SESSION_MAX": true,
	"MONIKA_SCHEDULER_CONCURRENCY": true, "MONIKA_LOG_LEVEL": true, "MONIKA_LOG_FORMAT": true,
	"MONIKA_INFLUX_URL": true, "MONIKA_INFLUX_TOKEN": true, "MONIKA_INFLUX_BUCKET": true,
	"MONIKA_INFLUX_ORG": true, "MONIKA_INFLUX_INTERVAL": true,
	"MONIKA_RETENTION_FINDINGS": true, "MONIKA_RETENTION_RUNS": true, "MONIKA_RETENTION_AUDIT": true,
	"MONIKA_ADMIN_USER": true, "MONIKA_ADMIN_PASSWORD": true,
}

// Load liest envFile (fehlt sie, ist das kein Fehler) und environ; Variablen aus der Umgebung
// haben Vorrang. Es prüft alles und liefert alle Fehler auf einmal.
func Load(environ []string, envFile string) (Config, error) {
	var cfg Config
	vars, err := readEnvFile(envFile, &cfg)
	if err != nil {
		return Config{}, err
	}
	for _, kv := range environ {
		if k, v, ok := strings.Cut(kv, "="); ok && strings.HasPrefix(k, "MONIKA_") {
			vars[k] = v
		}
	}

	p := parser{vars: vars}
	for k := range vars {
		if !known[k] {
			p.fail(k, errors.New("unknown variable"))
		}
	}
	cfg.Listen = p.str("MONIKA_LISTEN", ":8080")
	cfg.PublicURL = p.url("MONIKA_PUBLIC_URL", true)
	if u := cfg.PublicURL; u != nil && u.Scheme != "https" {
		cfg.Warnings = append(cfg.Warnings, "MONIKA_PUBLIC_URL is not https: session cookies are not Secure")
	}
	cfg.TrustedProxies = p.prefixes("MONIKA_TRUSTED_PROXIES")
	cfg.SessionIdle = p.duration("MONIKA_SESSION_IDLE", 12*time.Hour, false)
	cfg.SessionMax = p.duration("MONIKA_SESSION_MAX", 168*time.Hour, false)
	if cfg.SessionIdle > cfg.SessionMax {
		p.fail("MONIKA_SESSION_IDLE", errors.New("must not exceed MONIKA_SESSION_MAX"))
	}
	cfg.SchedulerConcurrency = p.positive("MONIKA_SCHEDULER_CONCURRENCY", 32)
	cfg.LogLevel = p.level("MONIKA_LOG_LEVEL")
	cfg.LogFormat = LogFormat(p.oneOf("MONIKA_LOG_FORMAT", string(LogJSON), string(LogJSON), string(LogText)))
	cfg.DatabaseURL = Secret{p.required("MONIKA_DATABASE_URL")}
	cfg.Keys.Current = p.key("MONIKA_MASTER_KEY", true)
	cfg.Keys.Previous = p.key("MONIKA_MASTER_KEY_PREVIOUS", false)

	cfg.Influx = InfluxConfig{
		URL:      p.url("MONIKA_INFLUX_URL", false),
		Token:    Secret{p.str("MONIKA_INFLUX_TOKEN", "")},
		Bucket:   p.str("MONIKA_INFLUX_BUCKET", ""),
		Org:      p.str("MONIKA_INFLUX_ORG", ""),
		Interval: p.duration("MONIKA_INFLUX_INTERVAL", time.Second, false),
	}
	if cfg.Influx.URL != nil && cfg.Influx.Bucket == "" {
		p.fail("MONIKA_INFLUX_BUCKET", errors.New("required with MONIKA_INFLUX_URL"))
	}
	cfg.Retention = RetentionConfig{
		Findings: p.duration("MONIKA_RETENTION_FINDINGS", 180*24*time.Hour, true),
		Runs:     p.duration("MONIKA_RETENTION_RUNS", 180*24*time.Hour, true),
		Audit:    p.duration("MONIKA_RETENTION_AUDIT", 730*24*time.Hour, true),
	}
	user, pass := p.str("MONIKA_ADMIN_USER", ""), p.str("MONIKA_ADMIN_PASSWORD", "")
	switch {
	case user != "" && pass != "":
		cfg.Admin = &AdminBootstrap{Username: strings.ToLower(user), Password: Secret{pass}}
	case user != "" || pass != "":
		p.fail("MONIKA_ADMIN_USER", errors.New("MONIKA_ADMIN_USER and MONIKA_ADMIN_PASSWORD go together"))
	}

	if err := errors.Join(p.errs...); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// readEnvFile liest die .env-Datei; eine fehlende Datei ist kein Fehler.
func readEnvFile(path string, cfg *Config) (map[string]string, error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	defer f.Close()
	if st, err := f.Stat(); err == nil && st.Mode().Perm()&0o077 != 0 {
		cfg.Warnings = append(cfg.Warnings, fmt.Sprintf("%s is readable by others (mode %s), want 600", path, st.Mode().Perm()))
	}
	vars, err := parseDotEnv(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return vars, nil
}

// parser liest Variablen und sammelt Fehler, statt beim ersten abzubrechen.
type parser struct {
	vars map[string]string
	errs []error
}

func (p *parser) fail(key string, err error) { p.errs = append(p.errs, fmt.Errorf("%s: %w", key, err)) }

func (p *parser) str(key, def string) string {
	if v, ok := p.vars[key]; ok && v != "" {
		return v
	}
	return def
}

func (p *parser) required(key string) string {
	v := p.str(key, "")
	if v == "" {
		p.fail(key, errors.New("required"))
	}
	return v
}

func (p *parser) url(key string, required bool) *url.URL {
	v := p.str(key, "")
	if v == "" {
		if required {
			p.fail(key, errors.New("required"))
		}
		return nil
	}
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		p.fail(key, errors.New("want an absolute http or https URL"))
		return nil
	}
	return u
}

func (p *parser) prefixes(key string) []netip.Prefix {
	var out []netip.Prefix
	for s := range strings.SplitSeq(p.str(key, ""), ",") {
		if s = strings.TrimSpace(s); s == "" {
			continue
		}
		pfx, err := netip.ParsePrefix(s)
		if err != nil {
			p.fail(key, fmt.Errorf("%q is not a CIDR prefix", s))
			continue
		}
		out = append(out, pfx.Masked())
	}
	return out
}

// duration liest eine Go-Dauer oder <n>d für Tage; zeroOK erlaubt 0 („nie“).
func (p *parser) duration(key string, def time.Duration, zeroOK bool) time.Duration {
	v := p.str(key, "")
	if v == "" {
		return def
	}
	d, err := parseDuration(v)
	switch {
	case err != nil:
		p.fail(key, err)
	case d < 0 || d == 0 && !zeroOK:
		p.fail(key, errors.New("must be positive"))
	}
	return d
}

func parseDuration(v string) (time.Duration, error) {
	if days, ok := strings.CutSuffix(v, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil {
			return 0, fmt.Errorf("%q: want a Go duration or <n>d", v)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%q: want a Go duration or <n>d", v)
	}
	return d, nil
}

func (p *parser) positive(key string, def int) int {
	v := p.str(key, "")
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		p.fail(key, errors.New("want a positive integer"))
	}
	return n
}

func (p *parser) oneOf(key, def string, allowed ...string) string {
	v := p.str(key, def)
	for _, a := range allowed {
		if v == a {
			return v
		}
	}
	p.fail(key, fmt.Errorf("want one of %s", strings.Join(allowed, ", ")))
	return def
}

func (p *parser) level(key string) slog.Level {
	var l slog.Level
	if err := l.UnmarshalText([]byte(p.oneOf(key, "info", "debug", "info", "warn", "error"))); err != nil {
		p.fail(key, err)
	}
	return l
}

func (p *parser) key(name string, required bool) secret.Key {
	v := p.str(name, "")
	if v == "" {
		if required {
			p.fail(name, errors.New("required (create one with: openssl rand -base64 32)"))
		}
		return secret.Key{}
	}
	k, err := secret.ParseKey(v)
	if err != nil {
		p.fail(name, err)
	}
	return k
}
