package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/lukirs95/monika/internal/secret"
)

// Fehler der Anmeldung; die API macht daraus 401 bzw. 429.
var (
	// ErrInvalidCredentials ist dieselbe Antwort für unbekannten Namen und falsches Passwort.
	ErrInvalidCredentials = errors.New("invalid username or password")
	ErrThrottled          = errors.New("too many failed attempts")
	ErrInvalidChallenge   = errors.New("invalid or expired login challenge")
	ErrInvalidCode        = errors.New("invalid code")
	ErrUnauthenticated    = errors.New("not authenticated")
)

// ThrottledError nennt die Wartezeit (API: Retry-After).
type ThrottledError struct{ Wait time.Duration }

func (e ThrottledError) Error() string {
	return fmt.Sprintf("too many failed attempts, wait %s", e.Wait.Round(time.Second))
}
func (e ThrottledError) Is(target error) bool { return target == ErrThrottled }

// Zeiten aus auth.md (M5, M7).
const (
	challengeTTL      = 5 * time.Minute
	challengeAttempts = 5
	touchEvery        = time.Minute
	kioskForever      = 100 * 365 * 24 * time.Hour // Kiosk ohne Ablauf des Tokens
)

// Crypter verschlüsselt TOTP-Geheimnisse mit dem Master-Key (secret.Service).
type Crypter interface {
	Seal(id uuid.UUID, plaintext []byte) (secret.Encrypted, error)
	Open(e secret.Encrypted) ([]byte, error)
}

// Config enthält, was der Service braucht; verdrahtet wird in cmd/monika.
type Config struct {
	Store       Store
	Crypter     Crypter
	SessionIdle time.Duration
	SessionMax  time.Duration
	Log         *slog.Logger
}

// Meta beschreibt, woher eine Anmeldung kommt.
type Meta struct {
	RemoteAddr string
	UserAgent  string
}

// LoginStep ist das Ergebnis des Passworts: weiter mit dem Code oder mit der Einrichtung.
type LoginStep struct {
	Challenge       string
	NeedsEnrollment bool
}

// IssuedSession ist eine neue Sitzung für das Cookie; den Klartext gibt es nur hier.
type IssuedSession struct {
	Token     string
	ExpiresAt time.Time
}

// Service beantwortet „Wer bist du?“ (Anmeldung, Sitzungen, Tokens) und „Was darfst du?“
// (Rollen und Rechte im Speicher), siehe docs/contracts/auth.md.
type Service struct {
	cfg      Config
	now      func() time.Time
	throttle *throttle
	dummy    string // Hash zum Vergleich bei unbekanntem Namen, damit die Antwortzeit nichts verrät

	mu         sync.Mutex
	challenges map[string]*challenge   // Hash der Challenge → Anmeldung in Arbeit
	seen       map[uuid.UUID]time.Time // Sitzung → letzte Anfrage (Leerlauf)
	sessions   map[uuid.UUID]time.Time // noch zu schreiben: last_seen_at
	tokens     map[uuid.UUID]time.Time // noch zu schreiben: last_used_at
}

type challenge struct {
	user     User
	expires  time.Time
	attempts int
	enroll   bool
}

// New liefert den Service.
func New(cfg Config) *Service {
	dummy, _ := HashPassword(randomString(16)) // mit den echten Parametern, damit die Zeit stimmt
	return &Service{
		cfg: cfg, now: time.Now, throttle: newThrottle(), dummy: dummy,
		challenges: map[string]*challenge{}, seen: map[uuid.UUID]time.Time{},
		sessions: map[uuid.UUID]time.Time{}, tokens: map[uuid.UUID]time.Time{},
	}
}

// Login prüft Benutzername und Passwort und liefert eine Challenge für den zweiten Schritt: den
// TOTP-Code oder, wenn TOTP noch nicht eingerichtet ist, die Einrichtung (A1).
func (s *Service) Login(ctx context.Context, username, password string, meta Meta) (LoginStep, error) {
	name := strings.ToLower(strings.TrimSpace(username))
	now := s.now()
	if wait := s.throttle.wait(name, meta.RemoteAddr, now); wait > 0 {
		return LoginStep{}, ThrottledError{Wait: wait}
	}
	u, err := s.cfg.Store.UserByName(ctx, name)
	switch {
	case errors.Is(err, ErrNotFound):
		VerifyPassword(password, s.dummy)
		return LoginStep{}, s.failed(ctx, User{Username: name}, meta, "unknown_user", ErrInvalidCredentials)
	case err != nil:
		return LoginStep{}, err
	case u.PasswordHash == "" || !VerifyPassword(password, u.PasswordHash):
		return LoginStep{}, s.failed(ctx, u, meta, "wrong_password", ErrInvalidCredentials)
	case u.Disabled:
		return LoginStep{}, s.failed(ctx, u, meta, "disabled", ErrInvalidCredentials)
	case u.Service:
		return LoginStep{}, s.failed(ctx, u, meta, "service_account", ErrInvalidCredentials)
	}
	t, err := s.cfg.Store.TOTP(ctx, u.ID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return LoginStep{}, err
	}
	enroll := errors.Is(err, ErrNotFound) || t.ConfirmedAt.IsZero()
	token := randomString(32)
	s.mu.Lock()
	s.challenges[HashToken(token)] = &challenge{user: u, expires: now.Add(challengeTTL), enroll: enroll}
	s.mu.Unlock()
	return LoginStep{Challenge: token, NeedsEnrollment: enroll}, nil
}

// VerifyTOTP schließt die Anmeldung mit dem TOTP-Code oder einem Wiederherstellungscode ab.
func (s *Service) VerifyTOTP(ctx context.Context, challengeToken, code string, meta Meta) (IssuedSession, error) {
	ch, err := s.challenge(challengeToken, false, meta)
	if err != nil {
		return IssuedSession{}, err
	}
	ok, err := s.checkCode(ctx, ch.user, code)
	if err != nil {
		return IssuedSession{}, err
	}
	if !ok {
		return IssuedSession{}, s.wrongCode(ctx, challengeToken, ch, meta)
	}
	return s.complete(ctx, challengeToken, ch.user, meta)
}

// EnrollTOTP erzeugt ein neues TOTP-Geheimnis für eine Anmeldung, die eine Einrichtung braucht.
func (s *Service) EnrollTOTP(ctx context.Context, challengeToken string) (Enrollment, error) {
	ch, err := s.challenge(challengeToken, true, Meta{})
	if err != nil {
		return Enrollment{}, err
	}
	key := newTOTPSecret()
	enc, err := s.cfg.Crypter.Seal(ch.user.ID, key)
	if err != nil {
		return Enrollment{}, fmt.Errorf("seal totp: %w", err)
	}
	if err := s.cfg.Store.SaveTOTP(ctx, ch.user.ID, enc); err != nil {
		return Enrollment{}, err
	}
	return enrollment(key, ch.user.Username), nil
}

// ConfirmTOTP bestätigt die Einrichtung mit dem ersten Code aus der App, erzeugt die
// Wiederherstellungscodes (nur hier im Klartext) und schließt die Anmeldung ab.
func (s *Service) ConfirmTOTP(ctx context.Context, challengeToken, code string, meta Meta) (IssuedSession, []string, error) {
	ch, err := s.challenge(challengeToken, true, meta)
	if err != nil {
		return IssuedSession{}, nil, err
	}
	key, t, err := s.totpSecret(ctx, ch.user.ID)
	if err != nil {
		return IssuedSession{}, nil, err
	}
	step, ok := checkTOTP(key, strings.TrimSpace(code), s.now(), t.LastStep)
	if !ok {
		return IssuedSession{}, nil, s.wrongCode(ctx, challengeToken, ch, meta)
	}
	codes, hashes := recoveryCodes()
	if err := s.cfg.Store.ConfirmTOTP(ctx, ch.user.ID, step, hashes); err != nil {
		return IssuedSession{}, nil, err
	}
	issued, err := s.complete(ctx, challengeToken, ch.user, meta)
	if err != nil {
		return IssuedSession{}, nil, err
	}
	return issued, codes, nil
}

// challenge liefert eine gültige Challenge der passenden Art; abgelaufene werden entfernt.
func (s *Service) challenge(token string, enroll bool, meta Meta) (*challenge, error) {
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	key := HashToken(token)
	ch, ok := s.challenges[key]
	switch {
	case !ok:
		return nil, ErrInvalidChallenge
	case now.After(ch.expires):
		delete(s.challenges, key)
		return nil, ErrInvalidChallenge
	case ch.enroll != enroll:
		return nil, ErrInvalidChallenge
	}
	if meta.RemoteAddr != "" {
		if wait := s.throttle.wait(ch.user.Username, meta.RemoteAddr, now); wait > 0 {
			return nil, ThrottledError{Wait: wait}
		}
	}
	return ch, nil
}

// checkCode prüft einen TOTP-Code (Zeitschritt nur einmal) oder einen Wiederherstellungscode.
func (s *Service) checkCode(ctx context.Context, u User, code string) (bool, error) {
	code = strings.TrimSpace(code)
	if len(code) != totpDigits {
		return s.cfg.Store.UseRecoveryCode(ctx, u.ID, recoveryHash(code))
	}
	key, t, err := s.totpSecret(ctx, u.ID)
	if err != nil {
		return false, err
	}
	step, ok := checkTOTP(key, code, s.now(), t.LastStep)
	if !ok {
		return false, nil
	}
	return s.cfg.Store.UseTOTPStep(ctx, u.ID, step)
}

func (s *Service) totpSecret(ctx context.Context, user uuid.UUID) ([]byte, TOTP, error) {
	t, err := s.cfg.Store.TOTP(ctx, user)
	if err != nil {
		return nil, TOTP{}, err
	}
	key, err := s.cfg.Crypter.Open(t.Value)
	if err != nil {
		return nil, TOTP{}, fmt.Errorf("open totp: %w", err)
	}
	return key, t, nil
}

// wrongCode zählt einen falschen Code: bei der Challenge (höchstens 5) und bei der Begrenzung der
// Fehlversuche, damit sich der Code nicht über viele Challenges durchprobieren lässt.
func (s *Service) wrongCode(ctx context.Context, token string, ch *challenge, meta Meta) error {
	s.mu.Lock()
	ch.attempts++
	if ch.attempts >= challengeAttempts {
		delete(s.challenges, HashToken(token))
	}
	s.mu.Unlock()
	return s.failed(ctx, ch.user, meta, "wrong_code", ErrInvalidCode)
}

// failed zählt einen Fehlversuch und schreibt ihn ins Audit.
func (s *Service) failed(ctx context.Context, u User, meta Meta, reason string, err error) error {
	s.throttle.fail(u.Username, meta.RemoteAddr, s.now())
	if rerr := s.cfg.Store.RecordLogin(ctx, Login{UserID: u.ID, Username: u.Username, RemoteAddr: meta.RemoteAddr, Reason: reason}); rerr != nil {
		s.cfg.Log.Warn("failed login not recorded", "err", rerr)
	}
	return err
}

// complete schließt eine Anmeldung ab: Challenge weg, Begrenzung zurück, Sitzung, Audit.
func (s *Service) complete(ctx context.Context, token string, u User, meta Meta) (IssuedSession, error) {
	s.mu.Lock()
	delete(s.challenges, HashToken(token))
	s.mu.Unlock()
	s.throttle.succeed(u.Username, meta.RemoteAddr)
	issued, err := s.createSession(ctx, u, SessionUser, uuid.Nil, s.now().Add(s.cfg.SessionMax), meta)
	if err != nil {
		return IssuedSession{}, err
	}
	if err := s.cfg.Store.RecordLogin(ctx, Login{UserID: u.ID, Username: u.Username, RemoteAddr: meta.RemoteAddr, Success: true}); err != nil {
		s.cfg.Log.Warn("login not recorded", "err", err)
	}
	return issued, nil
}

func (s *Service) createSession(ctx context.Context, u User, kind SessionKind, tokenID uuid.UUID, expires time.Time, meta Meta) (IssuedSession, error) {
	plain, hash := newSessionToken()
	now := s.now()
	sess := Session{ID: uuid.Must(uuid.NewV7()), UserID: u.ID, Kind: kind, TokenID: tokenID, ExpiresAt: expires,
		CreatedAt: now, LastSeenAt: now, UserAgent: meta.UserAgent, RemoteAddr: meta.RemoteAddr}
	if err := s.cfg.Store.CreateSession(ctx, sess, hash); err != nil {
		return IssuedSession{}, fmt.Errorf("create session: %w", err)
	}
	s.mu.Lock()
	s.seen[sess.ID] = now
	s.mu.Unlock()
	return IssuedSession{Token: plain, ExpiresAt: expires}, nil
}

// Authenticate prüft das Sitzungs-Token aus dem Cookie: nicht widerrufen, nicht abgelaufen,
// Benutzer aktiv, bei Menschen nicht zu lange untätig, bei Kiosk das Token noch gültig.
func (s *Service) Authenticate(ctx context.Context, sessionToken string) (Principal, error) {
	sess, u, tok, err := s.cfg.Store.SessionByHash(ctx, HashToken(sessionToken))
	if errors.Is(err, ErrNotFound) {
		return Principal{}, ErrUnauthenticated
	} else if err != nil {
		return Principal{}, err
	}
	now := s.now()
	s.mu.Lock()
	last := sess.LastSeenAt
	if t := s.seen[sess.ID]; t.After(last) {
		last = t
	}
	s.mu.Unlock()
	switch {
	case !sess.RevokedAt.IsZero(), !now.Before(sess.ExpiresAt), u.Disabled:
		return Principal{}, ErrUnauthenticated
	case sess.Kind == SessionUser && now.Sub(last) > s.cfg.SessionIdle:
		return Principal{}, ErrUnauthenticated
	case sess.Kind == SessionKiosk && !tokenValid(tok, now):
		return Principal{}, ErrUnauthenticated
	}
	s.mu.Lock()
	s.seen[sess.ID], s.sessions[sess.ID] = now, now
	s.mu.Unlock()
	return Principal{UserID: u.ID, Username: u.Username, Service: u.Service, SessionID: sess.ID, TokenID: sess.TokenID,
		ReadOnly: sess.Kind == SessionKiosk}, nil
}

// AuthenticateToken prüft ein API-Token (Authorization: Bearer).
func (s *Service) AuthenticateToken(ctx context.Context, bearer string) (Principal, error) {
	tok, u, err := s.token(ctx, bearer)
	if err != nil {
		return Principal{}, err
	}
	s.mu.Lock()
	s.tokens[tok.ID] = s.now()
	s.mu.Unlock()
	return Principal{UserID: u.ID, Username: u.Username, Service: u.Service, TokenID: tok.ID, ReadOnly: tok.ReadOnly}, nil
}

// KioskLogin tauscht ein Token mit read_only eines Dienstkontos gegen eine Kiosk-Sitzung; sie
// gilt so lange wie das Token, ohne Leerlauf-Timeout.
func (s *Service) KioskLogin(ctx context.Context, bearer string, meta Meta) (IssuedSession, error) {
	tok, u, err := s.token(ctx, bearer)
	if err != nil {
		return IssuedSession{}, err
	}
	if !tok.ReadOnly || !u.Service {
		return IssuedSession{}, ErrUnauthenticated
	}
	expires := tok.ExpiresAt
	if expires.IsZero() {
		expires = s.now().Add(kioskForever)
	}
	return s.createSession(ctx, u, SessionKiosk, tok.ID, expires, meta)
}

func (s *Service) token(ctx context.Context, bearer string) (Token, User, error) {
	tok, u, err := s.cfg.Store.TokenByHash(ctx, HashToken(strings.TrimSpace(bearer)))
	if errors.Is(err, ErrNotFound) {
		return Token{}, User{}, ErrUnauthenticated
	} else if err != nil {
		return Token{}, User{}, err
	}
	if !tokenValid(tok, s.now()) || u.Disabled {
		return Token{}, User{}, ErrUnauthenticated
	}
	return tok, u, nil
}

func tokenValid(t Token, now time.Time) bool {
	return t.ID != uuid.Nil && t.RevokedAt.IsZero() && (t.ExpiresAt.IsZero() || now.Before(t.ExpiresAt))
}

// Logout beendet die Sitzung.
func (s *Service) Logout(ctx context.Context, sessionToken string) error {
	sess, _, _, err := s.cfg.Store.SessionByHash(ctx, HashToken(sessionToken))
	if errors.Is(err, ErrNotFound) {
		return nil
	} else if err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.seen, sess.ID)
	delete(s.sessions, sess.ID)
	s.mu.Unlock()
	if err := s.cfg.Store.RevokeSession(ctx, sess.UserID, sess.ID); err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	return nil
}

// Run schreibt jede Minute die gesammelten Zeitpunkte der letzten Nutzung und räumt abgelaufene
// Challenges auf; mit ctx schreibt es ein letztes Mal.
func (s *Service) Run(ctx context.Context) error {
	ticker := time.NewTicker(touchEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			s.flush(context.WithoutCancel(ctx))
			return nil
		case <-ticker.C:
			s.flush(ctx)
		}
	}
}

func (s *Service) flush(ctx context.Context) {
	now := s.now()
	s.mu.Lock()
	sessions, tokens := s.sessions, s.tokens
	s.sessions, s.tokens = map[uuid.UUID]time.Time{}, map[uuid.UUID]time.Time{}
	for k, ch := range s.challenges {
		if now.After(ch.expires) {
			delete(s.challenges, k)
		}
	}
	for id, t := range s.seen {
		if now.Sub(t) > s.cfg.SessionIdle {
			delete(s.seen, id)
		}
	}
	s.mu.Unlock()
	if err := s.cfg.Store.TouchSessions(ctx, touches(sessions)); err != nil {
		s.cfg.Log.Warn("session activity not saved", "err", err)
	}
	if err := s.cfg.Store.TouchTokens(ctx, touches(tokens)); err != nil {
		s.cfg.Log.Warn("token activity not saved", "err", err)
	}
}

func touches(m map[uuid.UUID]time.Time) []Touch {
	out := make([]Touch, 0, len(m))
	for id, at := range maps.All(m) {
		out = append(out, Touch{ID: id, At: at})
	}
	return out
}
