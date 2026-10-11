package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// Passwortregeln und argon2id-Parameter (A6, M6).
const (
	minPasswordLen = 12
	maxPasswordLen = 1024 // Bytes; schützt argon2 vor riesigen Eingaben
	argonTime      = 2
	argonMemory    = 19 * 1024 // KiB
	argonThreads   = 1
	argonKeyLen    = 32
	argonSaltLen   = 16
)

var (
	ErrWeakPassword = errors.New("password must have at least 12 characters")
	ErrLongPassword = errors.New("password is too long")
)

// CheckPassword prüft die Passwortregeln.
func CheckPassword(p string) error {
	switch {
	case len(p) > maxPasswordLen:
		return ErrLongPassword
	case utf8.RuneCountInString(p) < minPasswordLen:
		return ErrWeakPassword
	}
	return nil
}

// HashPassword liefert den argon2id-Hash im PHC-Format
// ($argon2id$v=19$m=…,t=…,p=…$salz$hash).
func HashPassword(p string) (string, error) {
	if err := CheckPassword(p); err != nil {
		return "", err
	}
	salt := make([]byte, argonSaltLen)
	_, _ = rand.Read(salt) // liefert laut Doku immer
	key := argon2.IDKey([]byte(p), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, argonMemory, argonTime, argonThreads,
		b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// VerifyPassword vergleicht ein Passwort in konstanter Zeit mit einem Hash; ein unlesbarer Hash
// passt nie.
func VerifyPassword(p, hash string) bool {
	if len(p) > maxPasswordLen {
		return false
	}
	parts := strings.Split(hash, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var version int
	var memory, iterations uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &threads); err != nil {
		return false
	}
	b64 := base64.RawStdEncoding
	salt, err1 := b64.DecodeString(parts[4])
	want, err2 := b64.DecodeString(parts[5])
	if err1 != nil || err2 != nil || len(want) == 0 {
		return false
	}
	got := argon2.IDKey([]byte(p), salt, iterations, memory, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// tokenPrefix kennzeichnet API-Tokens (A7), damit Scanner sie finden.
const tokenPrefix = "monika_"

// newAPIToken liefert ein API-Token im Klartext und seinen Hash; gespeichert wird nur der Hash.
func newAPIToken() (plaintext, hash string) {
	plaintext = tokenPrefix + randomString(32)
	return plaintext, HashToken(plaintext)
}

// newSessionToken liefert ein Sitzungs-Token für das Cookie und seinen Hash (M5).
func newSessionToken() (plaintext, hash string) {
	plaintext = randomString(32)
	return plaintext, HashToken(plaintext)
}

// HashToken ist der gespeicherte Hash eines Tokens: SHA-256, hexadezimal.
func HashToken(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

func randomString(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// recoveryCodes erzeugt die Wiederherstellungscodes (A8, M4) und ihre Hashes.
func recoveryCodes() (codes, hashes []string) {
	enc := base32.StdEncoding.WithPadding(base32.NoPadding)
	for range 10 {
		b := make([]byte, 7) // 56 Bit, davon 50 genutzt: 10 Zeichen base32
		_, _ = rand.Read(b)
		s := strings.ToLower(enc.EncodeToString(b))[:10]
		codes = append(codes, s[:5]+"-"+s[5:])
		hashes = append(hashes, recoveryHash(s))
	}
	return codes, hashes
}

// recoveryHash normalisiert einen eingegebenen Code (Groß-/Kleinschreibung, Bindestrich,
// Leerzeichen egal) und liefert seinen Hash.
func recoveryHash(code string) string {
	code = strings.ToLower(strings.NewReplacer("-", "", " ", "").Replace(code))
	return HashToken(code)
}
