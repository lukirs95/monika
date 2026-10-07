package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lukirs95/monika/internal/store/postgres/db"
)

// ErrNotFound bedeutet, dass das angefragte Objekt nicht existiert.
var ErrNotFound = errors.New("not found")

// DeviceInUseError bedeutet, dass Presets noch Schritte mit Zielen am Gerät haben.
type DeviceInUseError struct {
	Presets []db.ListPresetsUsingDeviceRow
}

func (e *DeviceInUseError) Error() string {
	return fmt.Sprintf("device is used by %d preset(s)", len(e.Presets))
}

// DeleteDevice löscht ein Gerät weich (siehe Query DeleteDevice). Solange Presets das Gerät
// verwenden, schlägt das Löschen mit *DeviceInUseError fehl. Ein bereits gelöschtes Gerät
// bleibt unverändert.
func DeleteDevice(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) error {
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		q := db.New(tx)
		deletedAt, err := q.LockDevice(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock device: %w", err)
		}
		if deletedAt != nil {
			return nil
		}

		presets, err := q.ListPresetsUsingDevice(ctx, id)
		if err != nil {
			return fmt.Errorf("list presets using device: %w", err)
		}
		if len(presets) > 0 {
			return &DeviceInUseError{Presets: presets}
		}

		if err := q.DeleteDevice(ctx, id); err != nil {
			return fmt.Errorf("delete device: %w", err)
		}
		return nil
	})
}
