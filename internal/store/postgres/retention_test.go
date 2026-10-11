//go:build integration

package postgres_test

import (
	"testing"
	"time"

	"github.com/lukirs95/monika/internal/store/postgres"
)

func TestRetention(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := server.NewDB(t)
	s := postgres.New(pool)
	user := newUser(t, pool)
	old, recent := time.Now().Add(-200*24*time.Hour), time.Now().Add(-time.Hour)
	cutoff := time.Now().Add(-180 * 24 * time.Hour)

	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	finding := func(cleared *time.Time) {
		exec(`INSERT INTO finding (element_id, element_path, origin, code, severity, raised_at, cleared_at) VALUES ($1, 'p', 'device', $2, 'error', $3, $4)`,
			newID(), newID().String(), old, cleared)
	}
	finding(&old)    // weg
	finding(&recent) // bleibt
	finding(nil)     // aktiv: bleibt

	run := func(parent any, finished *time.Time) any {
		id := newID()
		exec(`INSERT INTO action_run (id, parent_id, element_id, action, status, requested_by_id, requested_at, finished_at) VALUES ($1, $2, $3, 'start', 'confirmed', $4, $5, $6)`,
			id, parent, newID(), user, old, finished)
		return id
	}
	root := run(nil, &old)
	run(root, &old) // Kind: geht mit der Wurzel
	run(nil, &recent)
	running := run(nil, nil)
	run(running, &old) // Kind eines laufenden Baums: bleibt

	exec(`INSERT INTO audit_log (at, verb, object_type) VALUES ($1, 'x', 'y'), ($2, 'x', 'y')`, old, recent)

	exec(`INSERT INTO user_session (user_id, kind, token_hash, expires_at, last_seen_at) VALUES ($1, 'user', 'a', $2, $2)`, user, old)                     // abgelaufen
	exec(`INSERT INTO user_session (user_id, kind, token_hash, expires_at, revoked_at) VALUES ($1, 'user', 'b', now() + interval '1 day', $2)`, user, old) // widerrufen
	exec(`INSERT INTO user_session (user_id, kind, token_hash, expires_at) VALUES ($1, 'user', 'c', now() + interval '1 day')`, user)                      // gültig
	exec(`INSERT INTO user_recovery_code (user_id, code_hash, used_at) VALUES ($1, 'x', $2), ($1, 'y', NULL)`, user, old)

	purge := []struct {
		name string
		do   func() (int, error)
		want int
	}{
		{"findings", func() (int, error) { return s.PurgeFindings(ctx, cutoff, 1000) }, 1},
		{"runs", func() (int, error) { return s.PurgeRuns(ctx, cutoff, 1000) }, 1},
		{"audit", func() (int, error) { return s.PurgeAudit(ctx, cutoff, 1000) }, 1},
		{"sessions and codes", func() (int, error) { return s.PurgeSessions(ctx, time.Now().Add(-30*24*time.Hour)) }, 3},
	}
	for _, p := range purge {
		if n, err := p.do(); err != nil || n != p.want {
			t.Errorf("%s: deleted %d, %v; want %d", p.name, n, err, p.want)
		}
	}
	checks := []struct {
		sql  string
		want int
	}{
		{`SELECT count(*) FROM finding`, 2},
		{`SELECT count(*) FROM finding WHERE cleared_at IS NULL`, 1},
		{`SELECT count(*) FROM action_run`, 3},
		{`SELECT count(*) FROM audit_log WHERE at < now() - interval '180 days'`, 0},
		{`SELECT count(*) FROM user_session`, 1},
		{`SELECT count(*) FROM user_recovery_code`, 1},
	}
	for _, c := range checks {
		if got := count(t, pool, c.sql); got != c.want {
			t.Errorf("%s = %d, want %d", c.sql, got, c.want)
		}
	}
	if n, _ := s.PurgeFindings(ctx, cutoff, 1); n != 0 {
		t.Errorf("second purge deleted %d", n)
	}
}
