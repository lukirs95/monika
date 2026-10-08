// Package postgres implementiert die Persistenz von Monika in PostgreSQL.
//
// Die Abfragen stehen als SQL in queries/; sqlc erzeugt daraus das Paket db
// (make db-gen-go). Das Schema stammt aus migrations/.
package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lukirs95/monika/internal/core/evaluation"
	"github.com/lukirs95/monika/internal/core/inventory"
	"github.com/lukirs95/monika/internal/secret"
	"github.com/lukirs95/monika/internal/store/postgres/db"
)

var (
	_ inventory.Store  = (*Store)(nil)
	_ secret.Store     = (*Store)(nil)
	_ evaluation.Store = (*Store)(nil)
)

// Store implementiert die Store-Interfaces der Fachpakete auf PostgreSQL.
type Store struct {
	pool *pgxpool.Pool
	q    *db.Queries
}

// New erzeugt einen Store auf dem Verbindungspool.
func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, q: db.New(pool)}
}

// Open baut einen Verbindungspool auf und prüft, ob die Datenbank erreichbar ist.
func Open(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("create connection pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}
