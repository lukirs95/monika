# Vertrag: Anmeldung und Rechte

**Status:** abgestimmt am 2026-10-09. **Paket:** `internal/auth`. **Hintergrund:** `docs/ARCHITECTURE.md`, Abschnitt 12; [`services.md`](services.md) (Zusammenspiel mit der API).

`auth` beantwortet zwei getrennte Fragen: **Wer bist du?** (Anmeldung, Sitzung, Token) und **Was darfst du?** (Rollen, Rechte, Geltungsbereich). Benutzer, Rollen, Rechte, Tokens und Sitzungen ändert nur `auth`; die API ruft es auf.

## Bereits entschieden

| # | Entscheidung |
|---|---|
| P1 | API als JSON über HTTP, Vertrag als OpenAPI 3.1 (`api.yaml`, eigener Vertrag) |
| P2 | Live-Push per Server-Sent Events; die Event-ID ist `View.Version` |
| P3 | Sitzungen der UI auf dem Server (Tabelle), Token im `HttpOnly`-Cookie; lokale Anmeldung mit **TOTP** als zweitem Faktor |
| P4 | Dienstkonten melden sich mit API-Token an: `Authorization: Bearer …` |

## Typen und Interfaces

```mermaid
classDiagram
    direction LR

    class Service {
        <<auth>>
        +Login(ctx, username, password, meta) (LoginStep, error)
        +VerifyTOTP(ctx, challenge, code, meta) (Session, error)
        +Authenticate(ctx, sessionToken) (Principal, error)
        +AuthenticateToken(ctx, bearer) (Principal, error)
        +KioskLogin(ctx, bearer, meta) (Session, error)
        +Logout(ctx, sessionToken) error
        +Allowed(v *View, p Principal, perm Permission, element ID) bool
        +EnrollTOTP(ctx, user ID) (Enrollment, error)
        +ConfirmTOTP(ctx, user ID, code) (recoveryCodes []string, error)
        +CreateUser(ctx, actor, u NewUser) error
        +UpdateUser(ctx, actor, u User) error
        +SetPassword(ctx, actor, user ID, password) error
        +ResetTOTP(ctx, actor, user ID) error
        +CreateRole(ctx, actor, r Role) error
        +SetGrants(ctx, actor, role ID, g []Grant) error
        +SetMembers(ctx, actor, role ID, users []ID) error
        +CreateToken(ctx, actor, user ID, name, readOnly, expires) (plaintext string, error)
        +RevokeToken(ctx, actor, token ID) error
    }
    class Principal {
        <<struct>>
        +UserID ID
        +Username string
        +Service bool
        +SessionID ID
        +TokenID ID
        +ReadOnly bool
    }
    class LoginStep {
        <<struct>>
        +Challenge string
        +NeedsEnrollment bool
    }
    class Session {
        <<struct>>
        +Token string
        +ExpiresAt time.Time
    }
    class Enrollment {
        <<struct>>
        +OTPAuthURL string
        +Secret string
    }
    class Grant {
        <<struct>>
        +Permission Permission
        +ScopeElementID ID
    }
    class Store {
        <<interface, auth.Store>>
        +LoadAuthz(ctx) (Authz, error)
        +UserByName(ctx, name) (User, error)
        +CreateSession / TouchSession / RevokeSession
        +SessionByHash(ctx, hash) (Session, User, error)
        +TokenByHash(ctx, hash) (Token, User, error)
        +TOTP(ctx, user) (secret.Encrypted, error)
        +SaveTOTP / ConfirmTOTP / UseRecoveryCode
        +Benutzer, Rollen, Rechte, Tokens ändern (mit Audit)
    }

    Service ..> Principal
    Service ..> LoginStep
    Service ..> Session
    Service ..> Enrollment
    Service *-- Grant
    Service ..> Store
```

- **`Principal`** ist der angemeldete Benutzer einer Anfrage. Die API gibt seine `UserID` als `actor` an jedes Fachpaket weiter. Ist `ReadOnly` gesetzt (Token bzw. Kiosk-Sitzung mit `read_only`), lehnt die API jede ändernde Anfrage ab, unabhängig von den Rechten der Rollen.
- **Rollen und Rechte hält `auth` im Speicher** (`LoadAuthz` beim Start, nach jeder Änderung neu). `Allowed` braucht deshalb keine Datenbank, nur die `View` für den Geltungsbereich.

## Anmeldung mit Passwort und TOTP

```mermaid
sequenceDiagram
    autonumber
    participant U as Browser
    participant A as API
    participant Au as auth.Service
    participant S as auth.Store

    U->>A: POST /login {username, password}
    A->>Au: Login(username, password, meta)
    Au->>Au: Fehlversuche begrenzen (Name und IP)
    Au->>S: UserByName (kleingeschrieben)
    Au->>Au: argon2id prüfen, deaktiviert? Dienstkonto?
    alt falsch
        Au->>S: Audit login_failed
        Au-->>A: Fehler (gleich für „unbekannt“ und „falsches Passwort“)
    else richtig, TOTP eingerichtet
        Au-->>A: LoginStep{Challenge}
        A-->>U: 200 {challenge}
        U->>A: POST /login/totp {challenge, code}
        A->>Au: VerifyTOTP(challenge, code, meta)
        Au->>S: TOTP-Geheimnis (entschlüsseln), Code und Zeitschritt prüfen
        Au->>S: CreateSession (Hash des Tokens), Audit login
        Au-->>A: Session{Token}
        A-->>U: Set-Cookie (HttpOnly, Secure, SameSite=Strict)
    else richtig, TOTP noch nicht eingerichtet
        Au-->>A: LoginStep{Challenge, NeedsEnrollment}
        A-->>U: Einrichtung erzwingen (siehe unten)
    end
```

- Die **Challenge** zwischen Passwort und Code lebt nur im Speicher: 5 min gültig, höchstens 5 Versuche.
- Ein TOTP-Code gilt nur einmal: Der letzte verwendete Zeitschritt wird gespeichert.
- Statt des Codes ist ein **Wiederherstellungscode** möglich; jeder gilt einmal.

## TOTP einrichten

```mermaid
sequenceDiagram
    autonumber
    participant U as Browser
    participant Au as auth.Service
    participant Sec as secret (Master-Key)
    participant S as auth.Store

    U->>Au: EnrollTOTP(user)
    Au->>Au: 20 zufällige Bytes
    Au->>Sec: verschlüsseln (Master-Key, Associated Data = User-ID)
    Au->>S: SaveTOTP (unbestätigt)
    Au-->>U: otpauth://-URL (QR-Code) und Geheimnis zum Abtippen
    U->>Au: ConfirmTOTP(user, code aus der App)
    Au->>S: ConfirmTOTP, 10 Wiederherstellungscodes (als Hash)
    Au-->>U: Wiederherstellungscodes (nur einmal sichtbar)
```

## Anfragen

```mermaid
sequenceDiagram
    autonumber
    participant C as Client
    participant A as API
    participant Au as auth.Service

    alt Browser
        C->>A: Anfrage mit Cookie
        A->>A: ändernde Anfrage? Origin prüfen
        A->>Au: Authenticate(sessionToken)
        Au->>Au: Hash, Sitzung gültig, nicht abgelaufen, Benutzer aktiv, Kiosk: Token noch gültig
        Au->>Au: last_seen_at gesammelt aktualisieren
    else Dienstkonto
        C->>A: Authorization: Bearer monika_…
        A->>Au: AuthenticateToken(bearer)
        Au->>Au: Hash, nicht widerrufen, nicht abgelaufen, Benutzer aktiv
    end
    Au-->>A: Principal
    A->>A: ReadOnly und ändernde Anfrage? → 403
    A->>Au: Allowed(view, principal, permission, element)
```

## Dashboards (Kiosk)

Ein Dashboard, etwa eine Monitorwand, ist ein **Dienstkonto** mit einer Rolle, die nur `view` im gewünschten Geltungsbereich hat, und einem Token mit `read_only`. Weil ein Browser bei einem Seitenaufruf keinen `Authorization`-Header senden kann und ein Token in der URL in Logs und Verlauf landen würde, tauscht der Kiosk das Token einmal gegen eine Sitzung.

```mermaid
sequenceDiagram
    autonumber
    participant Ad as Admin
    participant W as Browser der Wand
    participant A as API
    participant Au as auth.Service
    participant S as auth.Store

    Ad->>Au: Dienstkonto, Rolle (nur view, Geltungsbereich), CreateToken(read_only)
    Au-->>Ad: Token (nur einmal sichtbar)
    W->>A: POST /kiosk {token}
    A->>Au: KioskLogin(token, meta)
    Au->>S: Token gültig, read_only, Benutzer ist Dienstkonto und aktiv
    Au->>S: CreateSession (kind = kiosk, token_id, ohne Leerlauf-Timeout)
    Au-->>A: Session
    A-->>W: Set-Cookie
    Note over W,A: Dashboard läuft, ändernde Anfragen werden abgelehnt
    Ad->>Au: RevokeToken(token)
    Au->>S: Token widerrufen, alle Sitzungen mit diesem token_id beenden
```

- Eine Kiosk-Sitzung hat **kein Leerlauf-Timeout** und gilt so lange wie ihr Token (Ablauf oder Widerruf beenden sie sofort).
- Nur Tokens mit `read_only` können Kiosk-Sitzungen erzeugen.
- Später möglich: Anmeldung per Code wie beim Fernseher (RFC 8628, Device Authorization Grant), damit niemand ein Token an der Wand eintippen muss; das Modell bleibt dasselbe.

## Rechte prüfen (`Allowed`)

- Rechte des Benutzers = Vereinigung der Rechte aller seiner Rollen (nur Erlauben, kein Verbieten).
- Ein Recht mit Geltungsbereich S gilt für Element E, wenn E im Bereich von S liegt (`docs/ARCHITECTURE.md` Abschnitt 12): S ist NULL; S ist E; S ist ein Vorfahre von E über `parent_id`; S ist das Gerät von E; S ist eine Gruppe, die E oder ein Gerät bzw. einen Vorfahren von E rekursiv enthält. Aufgelöst über die `View`.
- **Kritische Aktionen:** `Allowed(base) && Allowed(critical)` für dasselbe Element; die beiden Rechte dürfen aus verschiedenen Rollen kommen.
- Globale Rechte (`preset_run`, `preset_edit`, `admin`) haben immer Geltungsbereich NULL.

## Neue Tabellen

| Tabelle | Spalten | Zweck |
|---|---|---|
| `user_session` | `id`, `user_id`, `kind` (`user`, `kiosk`), `token_id` (nur Kiosk), `token_hash` (unique), `created_at`, `last_seen_at`, `expires_at`, `revoked_at`, `user_agent`, `remote_addr` | Sitzungen der UI und der Dashboards |
| `api_token` (neue Spalte) | `read_only` | Token darf nichts ändern; Voraussetzung für Kiosk-Sitzungen |
| `user_totp` | `user_id` (pk), `ciphertext`, `nonce`, `key_id`, `confirmed_at`, `last_step` | TOTP-Geheimnis, verschlüsselt mit dem Master-Key |
| `user_recovery_code` | `id`, `user_id`, `code_hash`, `used_at` | Wiederherstellungscodes |

## Entscheidungen

| # | Frage | Entscheidung |
|---|---|---|
| A1 | TOTP Pflicht? | Pflicht für alle interaktiven lokalen Benutzer, Einrichtung bei der ersten Anmeldung erzwungen; Dienstkonten und später OIDC ohne |
| A2 | Verschlüsselung und Rotation der TOTP-Geheimnisse | eigene Tabelle `user_totp` mit dem Master-Key; `VerifyAndRotate` prüft und rotiert alle verschlüsselten Bestände (Secrets und TOTP) über ein gemeinsames Interface |
| A3 | Lebensdauer einer Sitzung | Menschen: Leerlauf 12 h, höchstens 7 Tage (einstellbar). Dashboards: Kiosk-Sitzung über ein Token mit `read_only`, ohne Leerlauf-Timeout, gültig so lange wie das Token; Anmeldung per Code (RFC 8628) später |
| A4 | Schutz gegen CSRF | Cookie `SameSite=Strict` und Prüfung des `Origin`-Headers bei ändernden Anfragen |
| A5 | Schutz gegen Durchprobieren | pro Benutzername und IP nach 5 Fehlversuchen steigende Wartezeit bis 15 min; jeder Fehlversuch ins Audit |
| A6 | Passwörter | argon2id (m = 19 MiB, t = 2, p = 1), Mindestlänge 12 |
| A7 | Format der API-Tokens | `monika_` + 32 zufällige Bytes (base64url), gespeichert nur als SHA-256 |
| A8 | Wiederherstellungscodes | 10 Stück, je einmal gültig, nur einmal angezeigt; Admin kann TOTP zurücksetzen (Audit) |
