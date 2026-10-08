package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
)

const redacted = "[redacted]"

// Key ist ein Master-Key: 32 zufällige Bytes für AES-256-GCM. String, GoString und LogValue
// geben nie den Schlüssel aus, damit er nicht versehentlich in Logs landet.
type Key struct {
	bytes [32]byte
	id    string
}

// ParseKey liest einen Base64-kodierten Master-Key, z. B. aus MONIKA_MASTER_KEY
// (erzeugt mit `openssl rand -base64 32`).
func ParseKey(s string) (Key, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return Key{}, errors.New("master key is not valid base64")
	}
	if len(b) != 32 {
		return Key{}, fmt.Errorf("master key has %d bytes, want 32", len(b))
	}
	var k Key
	copy(k.bytes[:], b)
	sum := sha256.Sum256(append([]byte("monika master key id\x00"), b...))
	k.id = hex.EncodeToString(sum[:8])
	return k, nil
}

// ID ist der Fingerabdruck des Schlüssels; er steht als key_id an jedem verschlüsselten Wert.
// Aus ihm lässt sich der Schlüssel nicht zurückrechnen.
func (k Key) ID() string { return k.id }

// IsZero meldet, ob kein Schlüssel gesetzt ist.
func (k Key) IsZero() bool { return k.id == "" }

func (k Key) String() string               { return redacted }
func (k Key) GoString() string             { return redacted }
func (k Key) LogValue() slog.Value         { return slog.StringValue(redacted) }
func (k Key) MarshalText() ([]byte, error) { return []byte(redacted), nil }

// seal verschlüsselt plaintext für das Secret id. Die ID ist Associated Data, damit ein Wert
// nicht unbemerkt an ein anderes Secret kopiert werden kann.
func (k Key) seal(id uuid.UUID, plaintext []byte) (Encrypted, error) {
	aead, err := k.aead()
	if err != nil {
		return Encrypted{}, err
	}
	nonce := make([]byte, aead.NonceSize())
	rand.Read(nonce) // liefert laut Doku nie einen Fehler
	return Encrypted{
		ID:         id,
		Ciphertext: aead.Seal(nil, nonce, plaintext, id[:]),
		Nonce:      nonce,
		KeyID:      k.id,
	}, nil
}

// open entschlüsselt e und prüft dabei Schlüssel, Associated Data und Unversehrtheit.
func (k Key) open(e Encrypted) ([]byte, error) {
	if e.KeyID != k.id {
		return nil, fmt.Errorf("secret %s: encrypted with key %s, not %s", e.ID, e.KeyID, k.id)
	}
	aead, err := k.aead()
	if err != nil {
		return nil, err
	}
	if len(e.Nonce) != aead.NonceSize() {
		return nil, fmt.Errorf("secret %s: nonce has %d bytes, want %d", e.ID, len(e.Nonce), aead.NonceSize())
	}
	plaintext, err := aead.Open(nil, e.Nonce, e.Ciphertext, e.ID[:])
	if err != nil {
		return nil, fmt.Errorf("secret %s: decrypt: %w", e.ID, err)
	}
	return plaintext, nil
}

func (k Key) aead() (cipher.AEAD, error) {
	if k.IsZero() {
		return nil, errors.New("no master key")
	}
	block, err := aes.NewCipher(k.bytes[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
