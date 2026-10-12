package auth

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/argon2"

	"github.com/lukirs95/monika/internal/secret"
)

// memStore ist die Attrappe von Store im Speicher, nur mit dem, was der Service braucht.
type memStore struct {
	Store
	mu        sync.Mutex
	users     map[uuid.UUID]User
	sessions  map[string]Session // Hash → Sitzung
	tokens    map[string]Token   // Hash → Token
	totp      map[uuid.UUID]TOTP
	recovery  map[uuid.UUID][]string
	logins    []Login
	touched   []Touch
	authzData Authz
}

func newMemStore() *memStore {
	return &memStore{users: map[uuid.UUID]User{}, sessions: map[string]Session{}, tokens: map[string]Token{},
		totp: map[uuid.UUID]TOTP{}, recovery: map[uuid.UUID][]string{}}
}

func (m *memStore) UserByName(_ context.Context, name string) (User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, u := range m.users {
		if u.Username == name {
			return u, nil
		}
	}
	return User{}, ErrNotFound
}

func (m *memStore) RecordLogin(_ context.Context, l Login) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.logins = append(m.logins, l)
	return nil
}

func (m *memStore) CreateSession(_ context.Context, s Session, hash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[hash] = s
	return nil
}

func (m *memStore) SessionByHash(_ context.Context, hash string) (Session, User, Token, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[hash]
	if !ok {
		return Session{}, User{}, Token{}, ErrNotFound
	}
	var tok Token
	for _, t := range m.tokens {
		if t.ID == s.TokenID {
			tok = t
		}
	}
	return s, m.users[s.UserID], tok, nil
}

func (m *memStore) RevokeSession(_ context.Context, user, id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for h, s := range m.sessions {
		if s.ID == id && s.UserID == user {
			s.RevokedAt = time.Now()
			m.sessions[h] = s
			return nil
		}
	}
	return ErrNotFound
}

func (m *memStore) TokenByHash(_ context.Context, hash string) (Token, User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tokens[hash]
	if !ok {
		return Token{}, User{}, ErrNotFound
	}
	return t, m.users[t.UserID], nil
}

func (m *memStore) TOTP(_ context.Context, user uuid.UUID) (TOTP, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.totp[user]
	if !ok {
		return TOTP{}, ErrNotFound
	}
	return t, nil
}

func (m *memStore) SaveTOTP(_ context.Context, user uuid.UUID, v secret.Encrypted) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.totp[user].ConfirmedAt.IsZero() {
		return ErrTOTPConfirmed
	}
	m.totp[user] = TOTP{Value: v}
	return nil
}

func (m *memStore) ConfirmTOTP(_ context.Context, user uuid.UUID, step int64, hashes []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.totp[user]
	if !ok {
		return ErrNotFound
	}
	t.ConfirmedAt, t.LastStep = time.Now(), step
	m.totp[user] = t
	m.recovery[user] = hashes
	return nil
}

func (m *memStore) UseTOTPStep(_ context.Context, user uuid.UUID, step int64) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.totp[user]
	if step <= t.LastStep {
		return false, nil
	}
	t.LastStep = step
	m.totp[user] = t
	return true, nil
}

func (m *memStore) UseRecoveryCode(_ context.Context, user uuid.UUID, hash string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	i := slices.Index(m.recovery[user], hash)
	if i < 0 {
		return false, nil
	}
	m.recovery[user] = slices.Delete(m.recovery[user], i, i+1)
	return true, nil
}

func (m *memStore) TouchSessions(_ context.Context, ts []Touch) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.touched = append(m.touched, ts...)
	return nil
}

func (m *memStore) TouchTokens(context.Context, []Touch) error { return nil }

// plainCrypter „verschlüsselt“ nicht; die Tests prüfen den Ablauf, nicht die Kryptografie.
type plainCrypter struct{}

func (plainCrypter) Seal(id uuid.UUID, p []byte) (secret.Encrypted, error) {
	return secret.Encrypted{ID: id, Ciphertext: p}, nil
}
func (plainCrypter) Open(e secret.Encrypted) ([]byte, error) { return e.Ciphertext, nil }

// cheapHash ist ein argon2id-Hash mit kleinen Parametern, damit die Tests schnell bleiben;
// VerifyPassword liest die Parameter aus dem Hash.
func cheapHash(p string) string {
	salt := []byte("0123456789abcdef")
	key := argon2.IDKey([]byte(p), salt, 1, 64, 1, 32)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=64,t=1,p=1$%s$%s", argon2.Version, b64.EncodeToString(salt), b64.EncodeToString(key))
}

type bench struct {
	t     *testing.T
	s     *Service
	store *memStore
	now   time.Time
	anna  User
	meta  Meta
}

func newBench(t *testing.T) *bench {
	t.Helper()
	b := &bench{t: t, store: newMemStore(), now: time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC), meta: Meta{RemoteAddr: "10.0.0.1"}}
	b.anna = User{ID: uuid.New(), Username: "anna", PasswordHash: cheapHash("correct horse battery")}
	b.store.users[b.anna.ID] = b.anna
	b.s = New(Config{Store: b.store, Crypter: plainCrypter{}, SessionIdle: 12 * time.Hour, SessionMax: 7 * 24 * time.Hour, Log: slog.New(slog.DiscardHandler)})
	b.s.now = func() time.Time { return b.now }
	return b
}

// enroll meldet anna an und richtet TOTP ein; liefert Geheimnis, Sitzung und Codes.
func (b *bench) enroll() ([]byte, IssuedSession, []string) {
	b.t.Helper()
	step, err := b.s.Login(b.t.Context(), "Anna", "correct horse battery", b.meta)
	if err != nil || !step.NeedsEnrollment {
		b.t.Fatalf("Login = %+v, %v; want enrollment", step, err)
	}
	e, err := b.s.EnrollTOTP(b.t.Context(), step.Challenge)
	if err != nil {
		b.t.Fatalf("EnrollTOTP: %v", err)
	}
	key, _ := totpEncoding.DecodeString(e.Secret)
	issued, codes, err := b.s.ConfirmTOTP(b.t.Context(), step.Challenge, totpCode(key, b.now.Unix()/30), b.meta)
	if err != nil {
		b.t.Fatalf("ConfirmTOTP: %v", err)
	}
	return key, issued, codes
}

func TestLoginWithTOTP(t *testing.T) {
	b := newBench(t)
	key, issued, codes := b.enroll()
	if len(codes) != 10 || issued.Token == "" || !issued.ExpiresAt.Equal(b.now.Add(7*24*time.Hour)) {
		t.Fatalf("enrollment session %+v, %d codes", issued, len(codes))
	}
	p, err := b.s.Authenticate(t.Context(), issued.Token)
	if err != nil || p.UserID != b.anna.ID || p.ReadOnly {
		t.Fatalf("Authenticate = %+v, %v", p, err)
	}

	b.now = b.now.Add(time.Minute)
	step, err := b.s.Login(t.Context(), "anna", "correct horse battery", b.meta)
	if err != nil || step.NeedsEnrollment {
		t.Fatalf("second Login = %+v, %v", step, err)
	}
	if _, err := b.s.VerifyTOTP(t.Context(), step.Challenge, "123456", b.meta); !errors.Is(err, ErrInvalidCode) {
		t.Errorf("wrong code = %v", err)
	}
	if _, err := b.s.VerifyTOTP(t.Context(), step.Challenge, totpCode(key, b.now.Unix()/30), b.meta); err != nil {
		t.Fatalf("VerifyTOTP: %v", err)
	}
	// Derselbe Code gilt nur einmal; ein Wiederherstellungscode auch.
	step, _ = b.s.Login(t.Context(), "anna", "correct horse battery", b.meta)
	if _, err := b.s.VerifyTOTP(t.Context(), step.Challenge, totpCode(key, b.now.Unix()/30), b.meta); !errors.Is(err, ErrInvalidCode) {
		t.Errorf("replayed code = %v", err)
	}
	if _, err := b.s.VerifyTOTP(t.Context(), step.Challenge, codes[3], b.meta); err != nil {
		t.Errorf("recovery code = %v", err)
	}
	step, _ = b.s.Login(t.Context(), "anna", "correct horse battery", b.meta)
	if _, err := b.s.VerifyTOTP(t.Context(), step.Challenge, codes[3], b.meta); !errors.Is(err, ErrInvalidCode) {
		t.Errorf("used recovery code = %v", err)
	}
	var ok, failed int
	for _, l := range b.store.logins {
		if l.Success {
			ok++
		} else if l.Reason == "wrong_code" {
			failed++
		}
	}
	if ok != 3 || failed != 3 {
		t.Errorf("audit: %d logins, %d wrong codes; want 3 and 3", ok, failed)
	}
}

func TestLoginFailures(t *testing.T) {
	b := newBench(t)
	tests := []struct {
		name, user, password string
		change               func(*User)
	}{
		{"unknown user", "bob", "correct horse battery", nil},
		{"wrong password", "anna", "wrong horse battery", nil},
		{"disabled", "anna", "correct horse battery", func(u *User) { u.Disabled = true }},
		{"service account", "anna", "correct horse battery", func(u *User) { u.Service = true }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := b.anna
			if tt.change != nil {
				tt.change(&u)
			}
			b.store.users[u.ID] = u
			b.s.throttle = newThrottle()
			if _, err := b.s.Login(t.Context(), tt.user, tt.password, b.meta); !errors.Is(err, ErrInvalidCredentials) {
				t.Errorf("Login = %v", err)
			}
		})
	}
	b.store.users[b.anna.ID] = b.anna
	if l := b.store.logins[0]; l.Success || l.Reason != "unknown_user" {
		t.Errorf("audit = %+v", l)
	}
}

func TestThrottleCountsCodes(t *testing.T) {
	b := newBench(t)
	key, _, _ := b.enroll()
	for range 5 { // fünf falsche Codes über verschiedene Challenges
		step, err := b.s.Login(t.Context(), "anna", "correct horse battery", b.meta)
		if err != nil {
			t.Fatalf("Login: %v", err)
		}
		_, _ = b.s.VerifyTOTP(t.Context(), step.Challenge, "000000", b.meta)
	}
	if _, err := b.s.Login(t.Context(), "anna", "correct horse battery", b.meta); !errors.Is(err, ErrThrottled) {
		t.Fatalf("Login after 5 wrong codes = %v", err)
	}
	var te ThrottledError
	_, err := b.s.Login(t.Context(), "anna", "correct horse battery", b.meta)
	if !errors.As(err, &te) || te.Wait != 30*time.Second {
		t.Errorf("throttled = %v", err)
	}
	b.now = b.now.Add(31 * time.Second)
	step, _ := b.s.Login(t.Context(), "anna", "correct horse battery", b.meta)
	if _, err := b.s.VerifyTOTP(t.Context(), step.Challenge, totpCode(key, b.now.Unix()/30), b.meta); err != nil {
		t.Errorf("login after waiting = %v", err)
	}
}

func TestChallenge(t *testing.T) {
	b := newBench(t)
	b.enroll()
	step, _ := b.s.Login(t.Context(), "anna", "correct horse battery", b.meta)
	if _, err := b.s.EnrollTOTP(t.Context(), step.Challenge); !errors.Is(err, ErrInvalidChallenge) {
		t.Errorf("enroll with a code challenge = %v", err)
	}
	for range 5 {
		_, _ = b.s.VerifyTOTP(t.Context(), step.Challenge, "000000", Meta{RemoteAddr: "10.0.0.9"})
	}
	if _, err := b.s.VerifyTOTP(t.Context(), step.Challenge, "000000", Meta{}); !errors.Is(err, ErrInvalidChallenge) {
		t.Errorf("challenge after 5 attempts = %v", err)
	}
	b.s.throttle = newThrottle()
	step, _ = b.s.Login(t.Context(), "anna", "correct horse battery", b.meta)
	b.now = b.now.Add(6 * time.Minute)
	if _, err := b.s.VerifyTOTP(t.Context(), step.Challenge, "000000", b.meta); !errors.Is(err, ErrInvalidChallenge) {
		t.Errorf("expired challenge = %v", err)
	}
}

func TestSessions(t *testing.T) {
	b := newBench(t)
	_, issued, _ := b.enroll()
	b.now = b.now.Add(11 * time.Hour)
	if _, err := b.s.Authenticate(t.Context(), issued.Token); err != nil {
		t.Fatalf("within idle time: %v", err)
	}
	b.now = b.now.Add(11 * time.Hour) // seit der letzten Anfrage 11 h: noch gültig
	if _, err := b.s.Authenticate(t.Context(), issued.Token); err != nil {
		t.Fatalf("activity extends idle time: %v", err)
	}
	b.s.flush(t.Context())
	if len(b.store.touched) != 1 {
		t.Errorf("touches = %+v", b.store.touched)
	}
	b.now = b.now.Add(13 * time.Hour)
	if _, err := b.s.Authenticate(t.Context(), issued.Token); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("after idle time = %v", err)
	}
	if _, err := b.s.Authenticate(t.Context(), "nonsense"); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("unknown token = %v", err)
	}

	b.now = time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	if err := b.s.Logout(t.Context(), issued.Token); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if _, err := b.s.Authenticate(t.Context(), issued.Token); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("after logout = %v", err)
	}
}

// CheckSession hält eine Sitzung nicht am Leben: Ein offener Ereignisstrom allein verlängert den
// Leerlauf nicht.
func TestCheckSession(t *testing.T) {
	b := newBench(t)
	_, issued, _ := b.enroll()
	b.now = b.now.Add(11 * time.Hour)
	if err := b.s.CheckSession(t.Context(), issued.Token); err != nil {
		t.Fatalf("within idle time: %v", err)
	}
	b.now = b.now.Add(2 * time.Hour)
	if err := b.s.CheckSession(t.Context(), issued.Token); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("after idle time = %v, want ErrUnauthenticated", err)
	}
	if b.s.flush(t.Context()); len(b.store.touched) != 0 {
		t.Errorf("touches = %+v, want none", b.store.touched)
	}
}

func TestTokensAndKiosk(t *testing.T) {
	b := newBench(t)
	wall := User{ID: uuid.New(), Username: "wand", Service: true}
	b.store.users[wall.ID] = wall
	plain, hash := newAPIToken()
	b.store.tokens[hash] = Token{ID: uuid.New(), UserID: wall.ID, ReadOnly: true}
	writer, writerHash := newAPIToken()
	b.store.tokens[writerHash] = Token{ID: uuid.New(), UserID: wall.ID}

	p, err := b.s.AuthenticateToken(t.Context(), plain)
	if err != nil || !p.ReadOnly || !p.Service || p.UserID != wall.ID {
		t.Fatalf("AuthenticateToken = %+v, %v", p, err)
	}
	issued, err := b.s.KioskLogin(t.Context(), plain, b.meta)
	if err != nil {
		t.Fatalf("KioskLogin: %v", err)
	}
	b.now = b.now.Add(30 * 24 * time.Hour) // kein Leerlauf-Timeout
	if p, err := b.s.Authenticate(t.Context(), issued.Token); err != nil || !p.ReadOnly {
		t.Fatalf("kiosk session = %+v, %v", p, err)
	}
	if _, err := b.s.KioskLogin(t.Context(), writer, b.meta); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("kiosk with a writing token = %v", err)
	}
	// Widerruf des Tokens beendet die Kiosk-Sitzung.
	tok := b.store.tokens[hash]
	tok.RevokedAt = b.now
	b.store.tokens[hash] = tok
	if _, err := b.s.Authenticate(t.Context(), issued.Token); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("kiosk after revoking its token = %v", err)
	}
	if _, err := b.s.AuthenticateToken(t.Context(), plain); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("revoked token = %v", err)
	}
	if err := b.s.CheckToken(t.Context(), plain); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("CheckToken of a revoked token = %v", err)
	}
	if err := b.s.CheckToken(t.Context(), writer); err != nil {
		t.Errorf("CheckToken = %v", err)
	}
}

func (m *memStore) UserByID(_ context.Context, id uuid.UUID) (User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[id]
	if !ok {
		return User{}, ErrNotFound
	}
	return u, nil
}

// SetPasswordHash setzt den Hash und widerruft alle Sitzungen außer keep.
func (m *memStore) SetPasswordHash(_ context.Context, _, user uuid.UUID, hash string, keep uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u := m.users[user]
	u.PasswordHash = hash
	m.users[user] = u
	for h, s := range m.sessions {
		if s.UserID == user && s.ID != keep {
			s.RevokedAt = time.Now()
			m.sessions[h] = s
		}
	}
	return nil
}

func (m *memStore) ListSessions(_ context.Context, user uuid.UUID) ([]Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Session
	for _, s := range m.sessions {
		if s.UserID == user && s.RevokedAt.IsZero() {
			out = append(out, s)
		}
	}
	return out, nil
}

// login meldet anna mit eingerichtetem TOTP erneut an, einen Zeitschritt später.
func (b *bench) login(key []byte) IssuedSession {
	b.t.Helper()
	b.now = b.now.Add(30 * time.Second)
	step, err := b.s.Login(b.t.Context(), "anna", "correct horse battery", b.meta)
	if err != nil {
		b.t.Fatalf("Login: %v", err)
	}
	issued, err := b.s.VerifyTOTP(b.t.Context(), step.Challenge, totpCode(key, b.now.Unix()/30), b.meta)
	if err != nil {
		b.t.Fatalf("VerifyTOTP: %v", err)
	}
	return issued
}

func TestChangePassword(t *testing.T) {
	b := newBench(t)
	key, own, _ := b.enroll()
	other := b.login(key)
	ctx := t.Context()
	p, err := b.s.Authenticate(ctx, own.Token)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if p.DisplayName != b.anna.DisplayName || p.Username != "anna" {
		t.Errorf("principal = %+v", p)
	}

	if err := b.s.ChangePassword(ctx, p, "wrong", "a new correct horse", b.meta); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong current password = %v", err)
	}
	if err := b.s.ChangePassword(ctx, p, "correct horse battery", "short", b.meta); !errors.Is(err, ErrWeakPassword) {
		t.Fatalf("weak new password = %v", err)
	}
	if err := b.s.ChangePassword(ctx, p, "correct horse battery", "a new correct horse", b.meta); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}
	if !VerifyPassword("a new correct horse", b.store.users[b.anna.ID].PasswordHash) {
		t.Error("password not changed")
	}
	if _, err := b.s.Authenticate(ctx, own.Token); err != nil {
		t.Errorf("own session = %v, want kept", err)
	}
	if _, err := b.s.Authenticate(ctx, other.Token); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("other session = %v, want ended", err)
	}

	// Falsche aktuelle Passwörter zählen wie Fehlversuche bei der Anmeldung.
	for range 5 {
		_ = b.s.ChangePassword(ctx, p, "wrong", "a new correct horse", b.meta)
	}
	if err := b.s.ChangePassword(ctx, p, "a new correct horse", "another correct horse", b.meta); !errors.Is(err, ErrThrottled) {
		t.Errorf("after 5 failures = %v, want ErrThrottled", err)
	}
	service := Principal{UserID: uuid.New(), Username: "wand", Service: true}
	b.store.users[service.UserID] = User{ID: service.UserID, Username: "wand", Service: true}
	if err := b.s.ChangePassword(ctx, service, "", "a new correct horse", b.meta); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("service account = %v", err)
	}
}

// Durch Leerlauf abgelaufene Sitzungen fehlen; die letzte Aktivität kommt aus dem Speicher.
func TestListSessions(t *testing.T) {
	b := newBench(t)
	key, old, _ := b.enroll()
	b.now = b.now.Add(11 * time.Hour)
	fresh := b.login(key)
	b.now = b.now.Add(2 * time.Hour) // old: 13 h ohne Aktivität, fresh: 2 h
	p, err := b.s.Authenticate(t.Context(), fresh.Token)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	got, err := b.s.ListSessions(t.Context(), b.anna.ID)
	if err != nil || len(got) != 1 || got[0].ID != p.SessionID || !got[0].LastSeenAt.Equal(b.now) {
		t.Fatalf("ListSessions = %+v, %v; want only the fresh one, seen now", got, err)
	}
	if _, err := b.s.Authenticate(t.Context(), old.Token); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("old session = %v", err)
	}
}
