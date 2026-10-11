package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testKey ist ein gültiger Master-Key nur für Tests (32 Nullbytes).
const testKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

func minimal() []string {
	return []string{
		"MONIKA_MASTER_KEY=" + testKey,
		"MONIKA_DATABASE_URL=postgres://monika:geheim@db/monika",
		"MONIKA_PUBLIC_URL=https://monika.example",
		"PATH=/usr/bin", // fremde Variablen stören nicht
	}
}

func TestParseDotEnv(t *testing.T) {
	in := `# Kommentar
MONIKA_MASTER_KEY=abc+/def==
MONIKA_LISTEN = ":9090"
MONIKA_LOG_LEVEL='debug'
MONIKA_INFLUX_TOKEN=${NICHT_ERSETZT}

`
	got, err := parseDotEnv(strings.NewReader(in))
	if err != nil {
		t.Fatalf("parseDotEnv: %v", err)
	}
	want := map[string]string{
		"MONIKA_MASTER_KEY":   "abc+/def==",
		"MONIKA_LISTEN":       ":9090",
		"MONIKA_LOG_LEVEL":    "debug",
		"MONIKA_INFLUX_TOKEN": "${NICHT_ERSETZT}",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("parseDotEnv = %v, want %v", got, want)
	}

	for _, bad := range []string{"export MONIKA_X=1", "monika_x=1", "MONIKA_X", `MONIKA_X="open`, "MONIKA_X=1\nMONIKA_X=2"} {
		if _, err := parseDotEnv(strings.NewReader(bad)); err == nil {
			t.Errorf("parseDotEnv(%q) succeeded, want error", bad)
		}
	}
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(minimal(), filepath.Join(t.TempDir(), "missing.env"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	switch {
	case cfg.Listen != ":8080", cfg.SessionIdle != 12*time.Hour, cfg.SessionMax != 168*time.Hour,
		cfg.SchedulerConcurrency != 32, cfg.LogLevel != slog.LevelInfo, cfg.LogFormat != LogJSON,
		cfg.Influx.URL != nil, cfg.Influx.Interval != time.Second, cfg.Admin != nil,
		cfg.Retention.Findings != 180*24*time.Hour, cfg.Retention.Audit != 730*24*time.Hour,
		cfg.Keys.Current.IsZero(), !cfg.Keys.Previous.IsZero(), len(cfg.Warnings) != 0:
		t.Fatalf("defaults = %+v", cfg)
	}
	if cfg.DatabaseURL.Reveal() != "postgres://monika:geheim@db/monika" {
		t.Fatalf("DatabaseURL not kept")
	}
	if out := fmt.Sprintf("%+v", cfg); strings.Contains(out, "geheim") {
		t.Fatalf("printing the config leaks a secret: %s", out)
	}
}

func TestLoadEnvFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	content := "MONIKA_LISTEN=:9090\nMONIKA_LOG_FORMAT=text\nMONIKA_ADMIN_USER=Admin\nMONIKA_ADMIN_PASSWORD=sehr-geheim-123\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	env := append(minimal(), "MONIKA_LISTEN=:7070") // Umgebung hat Vorrang
	cfg, err := Load(env, path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Listen != ":7070" || cfg.LogFormat != LogText || cfg.Admin == nil || cfg.Admin.Username != "admin" {
		t.Fatalf("cfg = %+v", cfg)
	}
	if len(cfg.Warnings) != 1 || !strings.Contains(cfg.Warnings[0], "readable by others") {
		t.Fatalf("warnings = %v, want one about file mode", cfg.Warnings)
	}
}

func TestLoadErrors(t *testing.T) {
	tests := []struct {
		name string
		env  []string
		want []string // alle müssen im Fehler stehen
	}{
		{"missing required", []string{"PATH=/x"}, []string{"MONIKA_MASTER_KEY: required", "MONIKA_DATABASE_URL: required", "MONIKA_PUBLIC_URL: required"}},
		{"unknown variable", append(minimal(), "MONIKA_SESION_IDLE=1h"), []string{"MONIKA_SESION_IDLE: unknown variable"}},
		{"bad key", append(minimal(), "MONIKA_MASTER_KEY_PREVIOUS=kurz"), []string{"MONIKA_MASTER_KEY_PREVIOUS"}},
		{"relative public URL", append(minimal(), "MONIKA_PUBLIC_URL=monika.example"), []string{"MONIKA_PUBLIC_URL: want an absolute"}},
		{"bad proxy", append(minimal(), "MONIKA_TRUSTED_PROXIES=10.0.0.0/8, nonsense"), []string{`"nonsense" is not a CIDR`}},
		{"idle above max", append(minimal(), "MONIKA_SESSION_IDLE=200h"), []string{"must not exceed"}},
		{"zero session", append(minimal(), "MONIKA_SESSION_MAX=0"), []string{"MONIKA_SESSION_MAX: must be positive"}},
		{"bad level", append(minimal(), "MONIKA_LOG_LEVEL=verbose"), []string{"MONIKA_LOG_LEVEL: want one of"}},
		{"influx without bucket", append(minimal(), "MONIKA_INFLUX_URL=http://influx:8086"), []string{"MONIKA_INFLUX_BUCKET: required"}},
		{"bad retention", append(minimal(), "MONIKA_RETENTION_RUNS=halbes Jahr"), []string{"MONIKA_RETENTION_RUNS"}},
		{"admin without password", append(minimal(), "MONIKA_ADMIN_USER=admin"), []string{"go together"}},
		{"zero concurrency", append(minimal(), "MONIKA_SCHEDULER_CONCURRENCY=0"), []string{"positive integer"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(tt.env, filepath.Join(t.TempDir(), "missing.env"))
			if err == nil {
				t.Fatal("Load succeeded, want error")
			}
			for _, w := range tt.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q does not contain %q", err, w)
				}
			}
			if strings.Contains(err.Error(), "geheim") {
				t.Errorf("error leaks a secret: %v", err)
			}
		})
	}
}

func TestLoadValues(t *testing.T) {
	env := append(minimal(),
		"MONIKA_PUBLIC_URL=http://monika.local",
		"MONIKA_TRUSTED_PROXIES=10.1.2.3/8,fd00::/64",
		"MONIKA_RETENTION_AUDIT=0",
		"MONIKA_RETENTION_FINDINGS=30d",
		"MONIKA_INFLUX_URL=http://influx:8086",
		"MONIKA_INFLUX_BUCKET=monika",
		"MONIKA_INFLUX_INTERVAL=500ms",
	)
	cfg, err := Load(env, filepath.Join(t.TempDir(), "missing.env"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.TrustedProxies) != 2 || cfg.TrustedProxies[0].String() != "10.0.0.0/8" {
		t.Errorf("TrustedProxies = %v", cfg.TrustedProxies)
	}
	if cfg.Retention.Audit != 0 || cfg.Retention.Findings != 30*24*time.Hour {
		t.Errorf("Retention = %+v", cfg.Retention)
	}
	if cfg.Influx.URL.Host != "influx:8086" || cfg.Influx.Interval != 500*time.Millisecond {
		t.Errorf("Influx = %+v", cfg.Influx)
	}
	if len(cfg.Warnings) != 1 || !strings.Contains(cfg.Warnings[0], "not https") {
		t.Errorf("Warnings = %v, want one about http", cfg.Warnings)
	}
}

func TestEnvFile(t *testing.T) {
	if got := EnvFile([]string{"MONIKA_ENV_FILE=/etc/monika.env"}); got != "/etc/monika.env" {
		t.Errorf("EnvFile = %q", got)
	}
	if got := EnvFile(nil); got != ".env" {
		t.Errorf("EnvFile = %q, want .env", got)
	}
}

func TestSecretRedacted(t *testing.T) {
	s := Secret{"geheim"}
	var log bytes.Buffer
	slog.New(slog.NewJSONHandler(&log, nil)).Info("x", "db", s)
	js, _ := json.Marshal(struct{ DB Secret }{s})
	for _, out := range []string{fmt.Sprint(s), fmt.Sprintf("%+v %#v", s, s), log.String(), string(js)} {
		if strings.Contains(out, "geheim") || !strings.Contains(out, "[redacted]") {
			t.Errorf("output %q leaks or misses redaction", out)
		}
	}
}
