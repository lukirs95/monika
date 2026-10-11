package auth

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestPasswords(t *testing.T) {
	hash, err := HashPassword("correct horse battery")
	if err != nil || !strings.HasPrefix(hash, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Fatalf("HashPassword = %q, %v", hash, err)
	}
	if other, _ := HashPassword("correct horse battery"); other == hash {
		t.Error("same hash twice: salt missing")
	}
	tests := []struct {
		password, hash string
		want           bool
	}{
		{"correct horse battery", hash, true},
		{"correct horse batterY", hash, false},
		{"correct horse battery", "$argon2id$garbage", false},
		{"correct horse battery", "", false},
		{strings.Repeat("x", 2000), hash, false},
	}
	for _, tt := range tests {
		if got := VerifyPassword(tt.password, tt.hash); got != tt.want {
			t.Errorf("VerifyPassword(%.20q) = %v", tt.password, got)
		}
	}
	if _, err := HashPassword("kurz"); !errors.Is(err, ErrWeakPassword) {
		t.Errorf("short password = %v", err)
	}
	if err := CheckPassword("äöüäöüäöüäöü"); err != nil { // 12 Zeichen, 24 Bytes
		t.Errorf("12 characters with umlauts = %v", err)
	}
	if err := CheckPassword(strings.Repeat("x", 1025)); !errors.Is(err, ErrLongPassword) {
		t.Errorf("long password = %v", err)
	}
}

func TestTokens(t *testing.T) {
	a, ha := newAPIToken()
	b, _ := newAPIToken()
	if !strings.HasPrefix(a, "monika_") || len(a) != len("monika_")+43 || a == b {
		t.Errorf("api tokens %q, %q", a, b)
	}
	if HashToken(a) != ha || len(ha) != 64 || strings.Contains(ha, a) {
		t.Errorf("hash = %q", ha)
	}
	s, hs := newSessionToken()
	if len(s) != 43 || HashToken(s) != hs {
		t.Errorf("session token %q", s)
	}
}

func TestRecoveryCodes(t *testing.T) {
	codes, hashes := recoveryCodes()
	if len(codes) != 10 || len(hashes) != 10 {
		t.Fatalf("got %d codes", len(codes))
	}
	format := regexp.MustCompile(`^[a-z2-7]{5}-[a-z2-7]{5}$`)
	for i, c := range codes {
		if !format.MatchString(c) {
			t.Errorf("code %q", c)
		}
		if recoveryHash(strings.ToUpper(strings.ReplaceAll(c, "-", " "))) != hashes[i] {
			t.Errorf("normalized code %q does not match", c)
		}
	}
}

func TestTOTP(t *testing.T) {
	secret := []byte("12345678901234567890") // RFC 6238, Anhang B (SHA-1), letzte 6 Ziffern
	vectors := []struct {
		unix int64
		code string
	}{{59, "287082"}, {1111111109, "081804"}, {1234567890, "005924"}, {2000000000, "279037"}}
	for _, v := range vectors {
		if got := totpCode(secret, v.unix/30); got != v.code {
			t.Errorf("totpCode(%d) = %s, want %s", v.unix, got, v.code)
		}
	}
	now := time.Unix(1111111109, 0)
	step := now.Unix() / 30
	tests := []struct {
		name     string
		code     string
		lastStep int64
		want     bool
	}{
		{"current", totpCode(secret, step), 0, true},
		{"previous step", totpCode(secret, step-1), 0, true},
		{"next step", totpCode(secret, step+1), 0, true},
		{"two steps old", totpCode(secret, step-2), 0, false},
		{"already used", totpCode(secret, step), step, false},
		{"wrong", "000000", 0, false},
	}
	for _, tt := range tests {
		if _, ok := checkTOTP(secret, tt.code, now, tt.lastStep); ok != tt.want {
			t.Errorf("%s: ok = %v", tt.name, ok)
		}
	}
	e := enrollment(secret, "anna")
	u, err := url.Parse(e.OTPAuthURL)
	if err != nil || u.Scheme != "otpauth" || u.Host != "totp" || u.Path != "/Monika:anna" ||
		u.Query().Get("issuer") != "Monika" || u.Query().Get("secret") != e.Secret || e.Secret != "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ" {
		t.Errorf("enrollment = %+v", e)
	}
}

func TestThrottle(t *testing.T) {
	th := newThrottle()
	t0 := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	for i := range 4 {
		th.fail("Anna", "10.0.0.1", t0)
		if w := th.wait("anna", "10.0.0.1", t0); w != 0 {
			t.Fatalf("wait after %d failures = %v", i+1, w)
		}
	}
	steps := []time.Duration{30 * time.Second, time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 15 * time.Minute, 15 * time.Minute}
	for _, want := range steps {
		th.fail("anna", "10.0.0.1", t0)
		if w := th.wait("ANNA", "10.0.0.1", t0); w != want {
			t.Errorf("wait = %v, want %v", w, want)
		}
	}
	if w := th.wait("anna", "10.0.0.2", t0); w != 0 {
		t.Errorf("other IP waits %v", w)
	}
	if w := th.wait("anna", "10.0.0.1", t0.Add(16*time.Minute)); w != 0 {
		t.Errorf("wait after it ran out = %v", w)
	}
	th.succeed("anna", "10.0.0.1")
	if w := th.wait("anna", "10.0.0.1", t0); w != 0 {
		t.Errorf("wait after success = %v", w)
	}
	th.fail("bob", "10.0.0.1", t0)
	th.wait("x", "y", t0.Add(2*time.Hour)) // räumt auf
	if len(th.entries) != 0 {
		t.Errorf("entries after an hour = %d", len(th.entries))
	}
}

func TestTOTPSecret(t *testing.T) {
	a, b := newTOTPSecret(), newTOTPSecret()
	if len(a) != 20 || string(a) == string(b) {
		t.Errorf("secrets %x, %x", a, b)
	}
}
