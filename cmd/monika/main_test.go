//go:build integration

package main

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lukirs95/monika/internal/store/postgres/pgtest"
)

var server *pgtest.Server

func TestMain(m *testing.M) {
	os.Exit(func() int {
		var err error
		if server, err = pgtest.Start(context.Background()); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		defer server.Close()
		return m.Run()
	}())
}

// freePort liefert eine freie Adresse auf localhost.
func freePort(t *testing.T) string {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

// syncBuffer sammelt die Logs von run.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

// Monika startet gegen eine frische Datenbank, legt den Admin an, antwortet, verweigert eine
// zweite Instanz und fährt nach dem Signal geordnet herunter.
func TestStartAndShutdown(t *testing.T) {
	addr := freePort(t)
	environ := []string{
		"MONIKA_ENV_FILE=" + t.TempDir() + "/none.env",
		"MONIKA_DATABASE_URL=" + server.NewDBURL(t),
		"MONIKA_MASTER_KEY=" + "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=",
		"MONIKA_PUBLIC_URL=http://" + addr,
		"MONIKA_LISTEN=" + addr,
		"MONIKA_ADMIN_USER=Admin",
		"MONIKA_ADMIN_PASSWORD=correct horse battery",
		"MONIKA_LOG_FORMAT=text",
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	logs := &syncBuffer{}
	done := make(chan int, 1)
	go func() { done <- run(ctx, nil, environ, logs, logs) }()

	deadline := time.Now().Add(30 * time.Second)
	for run(t.Context(), []string{"-healthcheck"}, environ, &bytes.Buffer{}, &bytes.Buffer{}) != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("not healthy:\n%s", logs)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.Contains(logs.String(), "initial admin created") {
		t.Errorf("no initial admin:\n%s", logs)
	}

	req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://"+addr+"/api/v1/auth/login",
		strings.NewReader(`{"username":"admin","password":"correct horse battery"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("login = %d", resp.StatusCode)
	}

	// Eine zweite Instanz auf derselben Datenbank bricht ab.
	second := append([]string{}, environ...)
	second[4] = "MONIKA_LISTEN=" + freePort(t)
	secondLogs := &syncBuffer{}
	if code := run(t.Context(), nil, second, secondLogs, secondLogs); code != 1 || !strings.Contains(secondLogs.String(), "another monika is running") {
		t.Errorf("second instance = %d:\n%s", code, secondLogs)
	}

	cancel() // wie SIGTERM
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("exit code = %d:\n%s", code, logs)
		}
	case <-time.After(35 * time.Second):
		t.Fatalf("no shutdown within 35 s:\n%s", logs)
	}
	if !strings.Contains(logs.String(), "monika stopped") || strings.Contains(logs.String(), "did not stop in time") {
		t.Errorf("shutdown:\n%s", logs)
	}
}
