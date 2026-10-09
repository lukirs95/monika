package secret

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
)

var (
	// ErrInvalidName bedeutet, dass der Name eines Secrets leer ist.
	ErrInvalidName = errors.New("secret name must not be empty")
	// ErrInvalidValue bedeutet, dass der Klartext kein gültiges JSON ist.
	ErrInvalidValue = errors.New("secret value must be valid JSON")
)

// Keys sind die Master-Keys: der aktuelle und, nur während einer Rotation, der vorige
// (MONIKA_MASTER_KEY bzw. MONIKA_MASTER_KEY_PREVIOUS).
type Keys struct {
	Current  Key
	Previous Key
}

// Vault ist ein Bestand von Werten, die mit dem Master-Key verschlüsselt sind (z. B. die
// TOTP-Geheimnisse). VerifyAndRotate prüft und rotiert alle Vaults zusammen mit den Secrets.
type Vault interface {
	ListEncrypted(ctx context.Context) ([]Encrypted, error)
	Reencrypt(ctx context.Context, v Encrypted) error
}

// storeVault macht die Secrets selbst zu einem Vault.
type storeVault struct{ Store }

func (v storeVault) ListEncrypted(ctx context.Context) ([]Encrypted, error) {
	return v.ListSecretValues(ctx)
}

func (v storeVault) Reencrypt(ctx context.Context, e Encrypted) error {
	return v.ReencryptSecret(ctx, e)
}

// Service ver- und entschlüsselt Secrets und legt sie im Store ab.
type Service struct {
	store  Store
	vaults []Vault // Secrets zuerst, dann weitere Bestände
	keys   Keys
	logger *slog.Logger
}

// NewService erzeugt den Service. vaults sind weitere verschlüsselte Bestände, die beim Start
// geprüft und rotiert werden. Ohne aktuellen Master-Key: Fehler.
func NewService(store Store, keys Keys, logger *slog.Logger, vaults ...Vault) (*Service, error) {
	if keys.Current.IsZero() {
		return nil, errors.New("no current master key")
	}
	return &Service{store: store, vaults: append([]Vault{storeVault{store}}, vaults...), keys: keys, logger: logger}, nil
}

// Seal verschlüsselt beliebige Bytes mit dem aktuellen Master-Key; id ist Associated Data.
// Für Bestände außerhalb der Secrets, z. B. TOTP-Geheimnisse (Vault).
func (s *Service) Seal(id uuid.UUID, plaintext []byte) (Encrypted, error) {
	return s.keys.Current.seal(id, plaintext)
}

// Open entschlüsselt einen Wert, der mit dem aktuellen oder vorigen Master-Key verschlüsselt ist.
func (s *Service) Open(e Encrypted) ([]byte, error) {
	key, err := s.keyFor(e)
	if err != nil {
		return nil, err
	}
	return key.open(e)
}

// Create legt ein Secret an; der Klartext ist JSON, sein Format legt der Treiber fest.
func (s *Service) Create(ctx context.Context, actor uuid.UUID, name, description string, value json.RawMessage) (Secret, error) {
	if name == "" {
		return Secret{}, ErrInvalidName
	}
	if !json.Valid(value) {
		return Secret{}, ErrInvalidValue
	}
	sec := Secret{ID: uuid.Must(uuid.NewV7()), Name: name, Description: description}
	enc, err := s.keys.Current.seal(sec.ID, value)
	if err != nil {
		return Secret{}, err
	}
	if err := s.store.CreateSecret(ctx, actor, sec, enc); err != nil {
		return Secret{}, err
	}
	return sec, nil
}

// Replace ersetzt den Wert eines Secrets. Ein Secret lässt sich nur ersetzen, nie lesen.
func (s *Service) Replace(ctx context.Context, actor uuid.UUID, id uuid.UUID, value json.RawMessage) error {
	if !json.Valid(value) {
		return ErrInvalidValue
	}
	enc, err := s.keys.Current.seal(id, value)
	if err != nil {
		return err
	}
	return s.store.ReplaceSecretValue(ctx, actor, enc)
}

// Value liefert den Klartext, z. B. damit der Core ihn einem Treiber übergibt.
// Nie an die UI weitergeben, nie loggen.
func (s *Service) Value(ctx context.Context, id uuid.UUID) (json.RawMessage, error) {
	enc, err := s.store.SecretValue(ctx, id)
	if err != nil {
		return nil, err
	}
	key, err := s.keyFor(enc)
	if err != nil {
		return nil, err
	}
	return key.open(enc)
}

// List liefert alle Secrets ohne Wert.
func (s *Service) List(ctx context.Context) ([]Secret, error) {
	return s.store.ListSecrets(ctx)
}

// Delete löscht ein Secret; wird es noch verwendet: ErrInUse.
func (s *Service) Delete(ctx context.Context, actor uuid.UUID, id uuid.UUID) error {
	return s.store.DeleteSecret(ctx, actor, id)
}

// VerifyAndRotate läuft beim Start. Es entschlüsselt jeden Wert aller Vaults (Secrets, TOTP …)
// zur Probe; ist einer nicht lesbar, wird nichts rotiert und Monika darf nicht starten. Danach
// verschlüsselt es alle Werte mit dem vorigen Schlüssel neu mit dem aktuellen und liefert ihre
// Anzahl. Bricht das mittendrin ab, setzt der nächste Start mit beiden Schlüsseln fort.
func (s *Service) VerifyAndRotate(ctx context.Context) (rotated int, err error) {
	type staleValue struct {
		vault     Vault
		value     Encrypted
		plaintext []byte
	}
	var (
		errs  []error
		stale []staleValue
		total int
	)
	for _, v := range s.vaults {
		all, err := v.ListEncrypted(ctx)
		if err != nil {
			return 0, fmt.Errorf("list encrypted values: %w", err)
		}
		total += len(all)
		for _, enc := range all {
			key, err := s.keyFor(enc)
			var plaintext []byte
			if err == nil {
				plaintext, err = key.open(enc)
			}
			if err != nil {
				errs = append(errs, err)
				continue
			}
			if key.ID() != s.keys.Current.ID() {
				stale = append(stale, staleValue{vault: v, value: enc, plaintext: plaintext})
			}
		}
	}
	if len(errs) > 0 {
		return 0, fmt.Errorf("%d of %d encrypted values cannot be decrypted with the given master keys: %w",
			len(errs), total, errors.Join(errs...))
	}

	for _, st := range stale {
		fresh, err := s.keys.Current.seal(st.value.ID, st.plaintext)
		if err != nil {
			return rotated, err
		}
		if err := st.vault.Reencrypt(ctx, fresh); err != nil {
			return rotated, fmt.Errorf("re-encrypt %s: %w", st.value.ID, err)
		}
		rotated++
	}
	if rotated > 0 {
		s.logger.InfoContext(ctx, "values re-encrypted with the current master key", "count", rotated)
	}
	if !s.keys.Previous.IsZero() && len(stale) == 0 {
		s.logger.InfoContext(ctx, "no value uses the previous master key any more; MONIKA_MASTER_KEY_PREVIOUS can be removed")
	}
	return rotated, nil
}

// keyFor wählt den Schlüssel, mit dem enc verschlüsselt wurde.
func (s *Service) keyFor(enc Encrypted) (Key, error) {
	for _, k := range []Key{s.keys.Current, s.keys.Previous} {
		if !k.IsZero() && k.ID() == enc.KeyID {
			return k, nil
		}
	}
	return Key{}, fmt.Errorf("secret %s: encrypted with unknown master key %s", enc.ID, enc.KeyID)
}
