// Package pgtest stellt PostgreSQL in Docker für Integrationstests bereit.
//
// Ein Server wird pro Testpaket in TestMain gestartet und einmal migriert.
// Jeder Test bekommt mit NewDB eine eigene, frische Datenbank als Kopie
// dieser Vorlage; Tests können daher parallel laufen.
package pgtest

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/lukirs95/monika/internal/store/postgres"
)

const (
	image        = "postgres:18-alpine"
	templateName = "monika_template"
)

// Server ist ein PostgreSQL-Container mit migrierter Vorlagen-Datenbank.
type Server struct {
	container *tcpostgres.PostgresContainer
	admin     *pgxpool.Pool
	config    *pgxpool.Config
	seq       atomic.Int64
}

// Start startet den Container und migriert die Vorlagen-Datenbank.
func Start(ctx context.Context) (*Server, error) {
	container, err := tcpostgres.Run(ctx, image,
		tcpostgres.WithDatabase(templateName),
		tcpostgres.WithUsername("monika"),
		tcpostgres.WithPassword("monika"),
		tcpostgres.BasicWaitStrategies(),
		testcontainers.WithTmpfs(map[string]string{"/var/lib/postgresql": "rw"}),
		testcontainers.WithCmdArgs("-c", "fsync=off"),
	)
	if err != nil {
		return nil, fmt.Errorf("start postgres container: %w", err)
	}
	s := &Server{container: container}
	if err := s.init(ctx); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

func (s *Server) init(ctx context.Context) error {
	url, err := s.container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		return fmt.Errorf("connection string: %w", err)
	}
	if s.config, err = pgxpool.ParseConfig(url); err != nil {
		return fmt.Errorf("parse connection string: %w", err)
	}

	template, err := postgres.Open(ctx, url)
	if err != nil {
		return err
	}
	err = postgres.Migrate(ctx, template)
	// Eine Vorlage darf beim Kopieren keine offenen Verbindungen haben.
	template.Close()
	if err != nil {
		return err
	}

	s.admin, err = s.pool(ctx, "postgres")
	return err
}

// Close beendet den Container.
func (s *Server) Close() {
	if s.admin != nil {
		s.admin.Close()
	}
	_ = s.container.Terminate(context.Background())
}

// NewDB legt eine frische, migrierte Datenbank an und räumt sie nach dem Test ab.
func (s *Server) NewDB(t testing.TB) *pgxpool.Pool {
	t.Helper()
	ctx := t.Context()

	name := fmt.Sprintf("test_%d", s.seq.Add(1))
	ident := pgx.Identifier{name}.Sanitize()
	if _, err := s.admin.Exec(ctx, "CREATE DATABASE "+ident+" TEMPLATE "+templateName); err != nil {
		t.Fatalf("create test database: %v", err)
	}
	pool, err := s.pool(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		if _, err := s.admin.Exec(context.Background(), "DROP DATABASE "+ident+" WITH (FORCE)"); err != nil {
			t.Errorf("drop test database: %v", err)
		}
	})
	return pool
}

// NewDBURL legt wie NewDB eine frische, migrierte Datenbank an und liefert ihre URL, etwa für
// einen Test, der Monika als Ganzes startet.
func (s *Server) NewDBURL(t testing.TB) string {
	t.Helper()
	c := s.NewDB(t).Config().ConnConfig
	u := url.URL{Scheme: "postgres", User: url.UserPassword(c.User, c.Password),
		Host: net.JoinHostPort(c.Host, strconv.Itoa(int(c.Port))), Path: "/" + c.Database, RawQuery: "sslmode=disable"}
	return u.String()
}

func (s *Server) pool(ctx context.Context, database string) (*pgxpool.Pool, error) {
	config := s.config.Copy()
	config.ConnConfig.Database = database
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", database, err)
	}
	return pool, nil
}
