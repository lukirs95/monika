//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lukirs95/monika/internal/store/postgres"
)

func TestInstanceLock(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := server.NewDB(t)
	cfg := pool.Config().ConnConfig

	first, err := postgres.AcquireLock(ctx, cfg)
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	if _, err := postgres.AcquireLock(ctx, cfg); !errors.Is(err, postgres.ErrLocked) {
		t.Fatalf("second instance = %v, want ErrLocked", err)
	}

	// Watch endet ohne Fehler, wenn sein ctx endet.
	wctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if err := first.Watch(wctx); err != nil {
		t.Errorf("Watch = %v", err)
	}

	if err := first.Release(ctx); err != nil {
		t.Fatalf("Release: %v", err)
	}
	second, err := postgres.AcquireLock(ctx, cfg)
	if err != nil {
		t.Fatalf("AcquireLock after release: %v", err)
	}
	if err := second.Release(ctx); err != nil {
		t.Errorf("Release: %v", err)
	}
}

// Reißt die Verbindung der Sperre ab, meldet Watch einen Fehler: Die Sperre ist dann frei.
func TestInstanceLockLost(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := server.NewDB(t)
	lock, err := postgres.AcquireLock(ctx, pool.Config().ConnConfig)
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	if _, err := pool.Exec(ctx, `SELECT pg_terminate_backend(pid) FROM pg_locks WHERE locktype = 'advisory' AND database = (SELECT oid FROM pg_database WHERE datname = current_database())`); err != nil {
		t.Fatalf("terminate: %v", err)
	}
	wctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := lock.Watch(wctx); err == nil {
		t.Error("Watch = nil after the connection was lost")
	}
}
