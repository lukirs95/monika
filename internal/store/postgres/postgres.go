// Package postgres implementiert die Persistenz von Monika in PostgreSQL.
//
// Die Abfragen stehen als SQL in queries/; sqlc erzeugt daraus das Paket db
// (make db-gen-go). Das Schema stammt aus migrations/.
package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

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
