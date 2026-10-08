package secret

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

// Store persistiert Secrets. Jede Methode ist eine in sich atomare Operation.
//
// Methoden, die der User auslöst, bekommen den handelnden Benutzer als actor und schreiben
// einen Eintrag ins Audit, ohne den Wert; uuid.Nil steht für Monika selbst.
type Store interface {
	// CreateSecret legt ein Secret an. Ist der Name vergeben: ErrNameTaken.
	CreateSecret(ctx context.Context, actor uuid.UUID, s Secret, v Encrypted) error
	// ListSecrets liefert alle Secrets ohne Wert, sortiert nach Name.
	ListSecrets(ctx context.Context) ([]Secret, error)
	// SecretValue liefert den verschlüsselten Wert. Unbekannt: ErrNotFound.
	SecretValue(ctx context.Context, id uuid.UUID) (Encrypted, error)
	// ListSecretValues liefert alle verschlüsselten Werte (Startprüfung, Rotation).
	ListSecretValues(ctx context.Context) ([]Encrypted, error)
	// ReplaceSecretValue ersetzt den Wert eines Secrets. Unbekannt: ErrNotFound.
	ReplaceSecretValue(ctx context.Context, actor uuid.UUID, v Encrypted) error
	// ReencryptSecret speichert denselben Wert mit neuem Schlüssel (Rotation durch Monika,
	// ohne Audit). Unbekannt: ErrNotFound.
	ReencryptSecret(ctx context.Context, v Encrypted) error
	// DeleteSecret löscht ein Secret. Wird es noch verwendet: ErrInUse; unbekannt: ErrNotFound.
	DeleteSecret(ctx context.Context, actor uuid.UUID, id uuid.UUID) error
}

var (
	// ErrNotFound bedeutet, dass das Secret nicht existiert.
	ErrNotFound = errors.New("secret not found")
	// ErrNameTaken bedeutet, dass bereits ein Secret mit diesem Namen existiert.
	ErrNameTaken = errors.New("secret name already taken")
	// ErrInUse bedeutet, dass eine Quelle oder Weiterleitung das Secret noch verwendet.
	ErrInUse = errors.New("secret in use")
)
