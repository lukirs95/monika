// Command monika ist der Monolith: Konfiguration lesen, alles verdrahten, Dienste betreiben und
// in sechs Phasen herunterfahren (docs/contracts/bootstrap.md, flows.md).
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/lukirs95/monika/internal/config"

	_ "github.com/lukirs95/monika/drivers/all" // registriert die Treiber
)

// version setzt make per -ldflags "-X main.version=…".
var version string

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Environ(), os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

// run ist main ohne Prozess: Endet ctx (Signal), fährt Monika geordnet herunter. Exit-Code 0
// nach einem Signal, 1 bei jedem Fehler.
func run(ctx context.Context, args, environ []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("monika", flag.ContinueOnError)
	flags.SetOutput(stderr)
	showVersion := flags.Bool("version", false, "print the version and exit")
	healthcheck := flags.Bool("healthcheck", false, "check GET /healthz on MONIKA_LISTEN and exit with 0 or 1")
	if err := flags.Parse(args); err != nil {
		return 1
	}
	if *showVersion {
		_, _ = fmt.Fprintln(stdout, "monika", versionString())
		return 0
	}
	cfg, err := config.Load(environ, config.EnvFile(environ))
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "monika: invalid configuration:\n%v\n", err)
		return 1
	}
	if *healthcheck {
		return checkHealth(ctx, cfg.Listen)
	}
	return serve(ctx, cfg, stdout)
}

func versionString() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

// checkHealth fragt /healthz auf der eigenen Adresse ab (Healthcheck im Container ohne curl).
func checkHealth(ctx context.Context, listen string) int {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return 1
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+net.JoinHostPort(host, port)+"/api/v1/healthz", nil)
	if err != nil {
		return 1
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 1
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}
