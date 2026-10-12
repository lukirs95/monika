package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Einzelinstanz-Sperre (docs/contracts/flows.md, Start): ein Advisory Lock auf Sitzungsebene an
// einer eigenen Verbindung, die Monika bis zum Ende hält. Zwei Instanzen auf derselben Datenbank
// würden alle Geräte doppelt abfragen und Aktionen doppelt ausführen.

// lockKey ist der Schlüssel des Advisory Locks („monika“ als Zahl).
const lockKey int64 = 0x6d6f6e696b61

// lockPing ist der Takt, in dem Watch die Verbindung der Sperre prüft.
const lockPing = 5 * time.Second

// ErrLocked bedeutet, dass eine andere Monika auf dieser Datenbank läuft.
var ErrLocked = errors.New("another monika is running on this database")

// Lock hält die Einzelinstanz-Sperre.
type Lock struct {
	conn *pgx.Conn
}

// AcquireLock verbindet sich eigens und nimmt die Sperre; ist sie belegt: ErrLocked.
func AcquireLock(ctx context.Context, cfg *pgx.ConnConfig) (*Lock, error) {
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect for the instance lock: %w", err)
	}
	var ok bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", lockKey).Scan(&ok); err != nil {
		_ = conn.Close(ctx)
		return nil, fmt.Errorf("take the instance lock: %w", err)
	}
	if !ok {
		_ = conn.Close(ctx)
		return nil, ErrLocked
	}
	return &Lock{conn: conn}, nil
}

// Watch prüft die Verbindung der Sperre, bis ctx endet. Reißt sie ab, ist die Sperre frei für
// eine zweite Instanz; Watch kehrt dann mit Fehler zurück, und Monika fährt herunter.
func (l *Lock) Watch(ctx context.Context) error {
	ticker := time.NewTicker(lockPing)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
		pctx, cancel := context.WithTimeout(ctx, lockPing)
		err := l.conn.Ping(pctx)
		cancel()
		if err != nil && ctx.Err() == nil {
			return fmt.Errorf("instance lock lost: %w", err)
		}
	}
}

// Release gibt die Sperre frei und schließt ihre Verbindung.
func (l *Lock) Release(ctx context.Context) error {
	_, err := l.conn.Exec(ctx, "SELECT pg_advisory_unlock($1)", lockKey)
	return errors.Join(err, l.conn.Close(ctx))
}
