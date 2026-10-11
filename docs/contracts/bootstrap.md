# Vertrag: Bootstrap und Konfiguration

**Status:** abgestimmt am 2026-10-11. **Pakete:** `cmd/monika`, `internal/config`. **Hintergrund:** `docs/ARCHITECTURE.md`, Abschnitt 14 (Bootstrap aus der Umgebung, Variablen); Start- und Stoppreihenfolge: [`flows.md`](flows.md).

`main` liest die Konfiguration, erzeugt alle Komponenten über ihre Konstruktoren, verdrahtet sie und überwacht ihre Laufzeit. Es gibt kein DI-Framework und keinen globalen Zustand außer der Treiber-Registry.

## Konfiguration

```mermaid
classDiagram
    direction LR

    class Config {
        <<struct, internal/config>>
        +Listen string
        +PublicURL *url.URL
        +TrustedProxies []netip.Prefix
        +SessionIdle time.Duration
        +SessionMax time.Duration
        +SchedulerConcurrency int
        +LogLevel slog.Level
        +LogFormat LogFormat
        +DatabaseURL Secret
        +MasterKey secret.Key
        +MasterKeyPrevious secret.Key
        +Influx InfluxConfig
        +Retention RetentionConfig
        +Admin *AdminBootstrap
    }
    class InfluxConfig {
        <<struct>>
        +URL *url.URL
        +Token Secret
        +Bucket string
        +Org string
        +Interval time.Duration
    }
    class RetentionConfig {
        <<struct>>
        +Findings time.Duration
        +Runs time.Duration
        +Audit time.Duration
    }
    class AdminBootstrap {
        <<struct>>
        +Username string
        +Password Secret
    }
    class Secret {
        <<string, geschwärzt>>
        +String() string
        +LogValue() slog.Value
        +Reveal() string
    }

    Config *-- InfluxConfig
    Config *-- RetentionConfig
    Config *-- AdminBootstrap
    Config ..> Secret
```

| Typ | Bedeutung |
|---|---|
| `config.Load(environ []string, envFile string) (Config, error)` | liest `.env` und Umgebung, prüft alles und liefert **alle** Fehler auf einmal (`errors.Join`), nicht nur den ersten |
| `Secret` | geheimer Text (Datenbank-URL mit Passwort, Influx-Token, Admin-Passwort): `String`, `GoString`, `LogValue` und `MarshalText` liefern `[redacted]`; den Klartext gibt nur `Reveal()` an genau der Stelle heraus, die ihn braucht |
| `InfluxConfig.URL` | leer = keine Zeitreihen; der Influx-Schreiber startet dann nicht |
| `Admin` | `nil`, wenn die Variablen fehlen |

Variablen, Standardwerte und Bedeutung stehen in `docs/ARCHITECTURE.md`, Abschnitt 14.

### Regeln

- **Quelle:** `./.env` im Arbeitsverzeichnis, abweichend über `MONIKA_ENV_FILE`. Fehlt die Datei, ist das kein Fehler. Variablen aus der Umgebung haben Vorrang.
- **Format der `.env`:** eine strikte Teilmenge, von einem eigenen kleinen Parser gelesen: `KEY=VALUE`, Kommentare mit `#`, optional in einfachen oder doppelten Anführungszeichen; keine Ersetzung von `${VAR}`, keine Mehrzeiler. Der Master-Key (Base64 mit `=`, `/`, `+`) wird so nie umgedeutet.
- **Unbekannte `MONIKA_*`-Variablen** brechen den Start ab: Ein Tippfehler würde sonst still den Standardwert nehmen.
- **Rechte der `.env`:** Ist die Datei für Gruppe oder andere lesbar, warnt Monika (in Containern liegt sie oft mit anderen Rechten gemountet).
- **Dauern** im Go-Format (`12h`, `1s`), zusätzlich `d` für Tage (`180d`). `0` bei der Aufbewahrung heißt „nie löschen“.
- **`MONIKA_PUBLIC_URL`** ist Pflicht. Ist sie nicht `https`, warnt Monika: Sitzungs-Cookies sind dann nicht `Secure`. TLS übernimmt ein Reverse-Proxy.

## Verdrahtung und Laufzeit

```mermaid
sequenceDiagram
    autonumber
    participant M as main
    participant G as errgroup
    participant K as Komponenten

    M->>M: signal.NotifyContext(SIGINT, SIGTERM)
    M->>M: config.Load, Logger (slog.SetDefault)
    M->>K: erzeugen und verdrahten in der Reihenfolge aus flows.md (Start)
    alt Fehler beim Erzeugen oder Prüfen
        M->>M: Log, Exit-Code 1
    end
    M->>G: Run jedes Dienstes (Core, Supervisor, Executor, Runner, Weiterleitung, Influx, Aufbewahrung, API)
    alt ein Dienst kehrt mit Fehler zurück
        G->>K: gemeinsamer ctx abgebrochen
        K->>K: geordnet herunterfahren (Reihenfolge aus flows.md)
        G-->>M: Fehler
        M->>M: Exit-Code 1, die Umgebung startet neu
    else Signal
        G->>K: ctx abgebrochen
        K->>K: geordnet herunterfahren
        G-->>M: nil
        M->>M: Exit-Code 0
    end
```

- **Logger:** `slog` mit `MONIKA_LOG_FORMAT` (`json` oder `text`) und `MONIKA_LOG_LEVEL`; `slog.SetDefault`, damit auch `log`-Ausgaben fremder Libraries strukturiert landen (`sdk.md`, Logging). Beim Start eine Zeile mit Version und den wesentlichen, nicht geheimen Einstellungen.
- **Initialer Admin:** wird nur angelegt, wenn es noch keinen Benutzer gibt; das Passwort muss A6 erfüllen (`auth.md`), sonst startet Monika nicht. TOTP richtet er bei der ersten Anmeldung ein (A1). Gibt es schon Benutzer und die Variablen sind gesetzt, warnt Monika, dass sie entfernt werden können.
- **Kein Unterbefehl**, nur zwei Flags: `-version` und `-healthcheck` (fragt `GET /healthz` auf `MONIKA_LISTEN` ab und endet mit `0` oder `1`; für den Healthcheck im Container, der kein `curl` hat). Migrationen laufen beim Start; für die Entwicklung gibt es `make db-migrate`.
- **Exit-Codes:** `0` nach einem Signal, `1` bei jedem Fehler.

## Entscheidungen

| # | Frage | Entscheidung |
|---|---|---|
| K1 | Wie wird `.env` gelesen? | eigener kleiner Parser, strikte Teilmenge, keine Ersetzung |
| K2 | Unbekannte `MONIKA_*`-Variablen | Abbruch beim Start |
| K3 | Rechte der `.env` | Warnung, kein Abbruch |
| K4 | Lebenszyklus der Dienste | `errgroup` mit gemeinsamem `ctx`; ein Fehler fährt alles geordnet herunter, Exit-Code 1, Neustart durch die Umgebung |
| K5 | Woher kommt die `.env`? | `./.env` oder `MONIKA_ENV_FILE`; fehlt sie, kein Fehler; Umgebung hat Vorrang |
| K6 | TLS | nur über einen Reverse-Proxy; Warnung bei `MONIKA_PUBLIC_URL` ohne `https` |
| K7 | Flags | `-version` und `-healthcheck`, sonst keine (`docs/OPERATIONS.md`) |
