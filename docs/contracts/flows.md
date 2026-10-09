# Vertrag: Abläufe

**Status:** abgestimmt am 2026-10-09. **Hintergrund:** `docs/ARCHITECTURE.md`, Abschnitt 9, 10, 12, 13 und 14; Bausteine aus [`sdk.md`](sdk.md) und [`core.md`](core.md).

Discovery-Lauf und „Fakt → Befund“ stehen in `core.md` (Abläufe: Meldungen eines Treibers). Hier stehen die Abläufe, die mehrere Komponenten verbinden.

## Beteiligte

| Komponente | Paket | Aufgabe in den Abläufen |
|---|---|---|
| Bootstrap | `cmd/monika`, `internal/config` | Konfiguration lesen, alles erzeugen und verdrahten, Start- und Stoppreihenfolge |
| Core | `internal/core` | Schleife, `View`, Ereignisse, Befehle (`core.md`) |
| Supervisor | `internal/core/supervisor` | eigener Dienst: startet und stoppt die `Source`s im Auftrag der Schleife, fängt Panics, meldet `Stuck`, führt `Execute` aus |
| Executor | `internal/action` | Läufe: Warteschlange pro Element, Ausführung, Prüfbedingung |
| Preset-Runner | `internal/preset` | Stufen, Verzögerungen, Rechte-Vorabprüfung |
| Weiterleitung | `internal/forward` | Ereignisse filtern und an syslog/webhook senden |
| Influx-Schreiber | `internal/history/influx` | Fakten und Health im festen Takt aus der `View` |
| Rechte | `internal/auth` | `Allowed(view, user, permission, element)` für API und Preset-Runner |
| API | `internal/api` | Anmeldung, Rechte, Befehle an Core/Executor/Preset-Runner, Live-Push |

## Start

```mermaid
sequenceDiagram
    autonumber
    participant M as main
    participant DB as PostgreSQL
    participant Sec as secret.Service
    participant C as Core
    participant Sup as Supervisor
    participant X as Executor, Weiterleitung, Influx
    participant A as API

    M->>M: Konfiguration aus Umgebung/.env, Master-Keys parsen
    M->>DB: postgres.Open (Ping)
    M->>DB: postgres.Migrate
    M->>Sec: VerifyAndRotate
    alt ein Secret nicht lesbar
        Sec-->>M: Fehler
        M->>M: Abbruch, kein Start
    end
    M->>DB: initialen Admin anlegen, falls es keinen Benutzer gibt
    M->>DB: unterbrochene Läufe abschließen (siehe Aktion)
    M->>C: Laden: Inventar, Regeln, aktive Befunde, Health
    M->>+C: go Run(ctx)
    M->>X: go Run(ctx) (abonnieren Ereignisse)
    C->>Sup: Start(alle aktiven Quellen nicht gelöschter Geräte)
    loop pro Quelle
        Sup->>Sec: Value(secret_id)
        Sup->>Sup: Driver.New(SourceConfig), go Run(ctx, Reporter)
    end
    M->>A: Listen (zuletzt)
    Note over C: alle Elemente unknown, bis ihre Quelle einen Snapshot meldet
```

- **Abbruch statt Warten:** Ist die Konfiguration ungültig, die Datenbank nicht erreichbar, eine Migration fehlerhaft oder ein Secret nicht lesbar, beendet sich Monika mit Fehler. Neustarts übernimmt die Umgebung (Docker, systemd).
- Die API öffnet zuletzt, damit keine Anfrage einen halb gestarteten Core sieht.

## Herunterfahren

```mermaid
sequenceDiagram
    autonumber
    participant M as main
    participant A as API
    participant P as Preset-Runner, Executor
    participant Sup as Supervisor
    participant C as Core
    participant X as Weiterleitung, Influx
    participant DB as PostgreSQL

    M->>M: SIGTERM / SIGINT
    M->>A: Shutdown (keine neuen Anfragen, laufende beenden, Frist)
    M->>P: Stopp: keine neuen Läufe, laufende enden als unterbrochen
    M->>Sup: alle Quellen stoppen (ctx), höchstens 10 s je Quelle
    M->>C: Schleife beenden: Rest verarbeiten, letztes SaveHealth
    M->>X: Rest senden bzw. schreiben (kurze Frist)
    M->>DB: Pool schließen
```

## Weiterleitung

```mermaid
sequenceDiagram
    autonumber
    participant C as Core
    participant F as Weiterleitung
    participant Sec as secret.Service
    participant Z as Ziel (syslog/webhook)
    participant S as Store

    C-->>F: FindingRaised / FindingEnded / FindingAcked / ActionFinished
    F->>F: pro Ziel filtern: Ereignistyp, Mindest-Schweregrad, Geltungsbereich (über die View)
    F->>F: in die Warteschlange des Ziels
    F->>Sec: Value(secret_id) (bei Bedarf, z. B. Token)
    F->>Z: senden (Vorlage gefüllt)
    alt Fehler
        F->>Z: bis zu 3 Wiederholungen (1 s, 5 s, 30 s)
        F->>S: last_error setzen
    else ok
        F->>S: last_sent_at setzen (gesammelt, nicht pro Ereignis)
    end
```

- Eine Warteschlange **pro Ziel**: ein langsames Ziel hält die anderen nicht auf. Ist sie voll, werden die ältesten Ereignisse verworfen und geloggt (best effort).
- `FindingEnded` wird je nach `cleared_reason` als `finding.cleared` (behoben) oder `finding.withdrawn` gesendet.

## Aktion

```mermaid
sequenceDiagram
    autonumber
    participant A as API
    participant X as Executor
    participant S as Store
    participant Sup as Supervisor
    participant Src as Source
    participant C as Core (View, Ereignisse)

    A->>A: Rechte: operate bzw. configure (+ critical) am Ziel
    A->>X: Submit(Lauf: Ziel, Aktion, Parameter, Treiber?)
    X->>X: Ziel auflösen: Element, Sammelaktion (Gerät, Gruppe), Zugangs-Element
    X->>S: action_run anlegen (pending), bei Sammelaktion mit Kindern
    X-->>A: Lauf-ID
    Note over X: Warteschlange pro Element (FIFO)
    X->>S: Status sent
    X->>Sup: Execute(sourceID, Command)
    Sup->>Src: Execute(ctx, Command)
    alt Rejected
        Src-->>X: Result{Rejected}
        X->>S: Status rejected, response
    else error
        Src-->>X: error
        X->>S: Status failed
    else Accepted
        Src-->>X: Result{Accepted}
        X->>S: Status accepted
        opt Prüfbedingung vorhanden
            X->>C: FactsChanged des Elements abonnieren, bei jeder Änderung gegen die View prüfen
            alt erfüllt vor Timeout
                X->>S: Status confirmed
            else Timeout
                X->>S: Status unconfirmed
            end
        end
    end
    X-->>C: ActionFinished (für Weiterleitung und UI)
```

```mermaid
stateDiagram-v2
    [*] --> pending: angelegt
    pending --> sent: an der Reihe, an den Treiber
    pending --> cancelled: abgebrochen (Preset abgebrochen, Monika gestoppt)
    pending --> failed: Ziel verschwunden oder Quelle fehlt
    sent --> rejected: Gerät lehnt ab
    sent --> failed: nicht erreicht
    sent --> accepted: Gerät nimmt an
    accepted --> confirmed: Prüfbedingung erfüllt
    accepted --> unconfirmed: Timeout
    accepted --> [*]: keine Prüfbedingung
    rejected --> [*]
    failed --> [*]
    confirmed --> [*]
    unconfirmed --> [*]
    cancelled --> [*]
```

- **Sammelaktion:** ein übergeordneter Lauf mit einem Kind pro Element; er endet, wenn alle Kinder enden, mit dem schlechtesten Status (`failed` > `rejected` > `unconfirmed` > `accepted` > `confirmed`, `docs/ARCHITECTURE.md` Abschnitt 9).
- **Kritisch:** `action_run.critical` friert ein, ob die Aktion (Definition oder `critical_actions`) oder ein gesetzter Schlüssel kritisch war.
- **Unterbrochen durch Neustart:** Nach einem Neustart wird nichts erneut an Geräte gesendet. Monika schließt offene Läufe nur ab: `pending` und `sent` als `failed`, `accepted` als `unconfirmed`, jeweils mit Grund „Monika neu gestartet“. Wer die Aktion wiederholen will, löst sie neu aus.
- **Endzustände sind endgültig.** Ein Lauf ist Verlauf, kein Zustand: Er hält fest, was bei dieser einen Ausführung passiert ist. Nichts setzt `unconfirmed` (oder einen anderen Endzustand) später zurück oder auf `confirmed`; ob eine spätere Wirkung von diesem Befehl kam, ist nicht feststellbar. Den aktuellen Zustand eines Elements zeigen immer die Fakten.
- **Prüfbedingung:** Der Executor abonniert `FactsChanged` und prüft bei jeder Änderung des Elements gegen die `View`; nach dem Neuanmelden (`core.md`, Abonnenten) prüft er einmal direkt.
- **Rechte** prüft nur die API beim Absenden (bzw. der Preset-Runner vorab); der Executor prüft nicht erneut. Zwischen Absenden und Senden liegen nur Warteschlange und Preset-Stufen, keine zeitgesteuerte Ausführung.

## Preset-Lauf

```mermaid
sequenceDiagram
    autonumber
    participant A as API
    participant P as Preset-Runner
    participant X as Executor
    participant S as Store

    A->>A: Recht preset_run
    A->>P: Run(preset, user)
    P->>S: GetPreset
    P->>P: Vorabprüfung: Rechte für jeden Schritt am Ziel (inkl. critical)
    alt ein Recht fehlt
        P-->>A: abgelehnt, nichts gestartet
    end
    P->>S: Wurzel-Lauf (action = preset)
    P-->>A: Lauf-ID
    loop Stufen nacheinander
        par Schritte der Stufe parallel
            P->>P: delay_ms abwarten
            P->>X: Submit(Schritt-Lauf, parent = Wurzel)
        end
        P->>P: auf Endzustand aller Läufe der Stufe warten
        alt ein Schritt fehlgeschlagen ohne continue_on_error
            P->>X: offene Läufe des Presets abbrechen (cancelled)
            P->>S: Wurzel-Lauf beenden
        end
    end
    P->>S: Wurzel-Lauf: schlechtester Status der Schritte
```

- „Fehlgeschlagen“ heißt für die Stufe: `failed` oder `rejected`. `unconfirmed` bricht nicht ab (das Gerät hat angenommen, die Wirkung ist nur nicht beobachtet).

## Neue Store-Methoden, die diese Abläufe brauchen

| Interface | Methoden |
|---|---|
| `action.Store` | `CreateRun(ctx, run)` (mit Kindern), `UpdateRunStatus(ctx, id, status, response)`, `FinishInterruptedRuns(ctx, reason)` |
| `forward.Store` | `ListTargets(ctx)`, `SetTargetResult(ctx, id, sentAt, lastError)`; Ziele anlegen/ändern/löschen mit Audit |
| `auth.Store` | Benutzer, Rollen, Rechte laden; initialen Admin anlegen (eigener Vertrag mit der API) |

## Entscheidungen

| # | Frage | Entscheidung |
|---|---|---|
| F1 | Datenbank beim Start nicht erreichbar | Abbruch; Neustart durch die Umgebung |
| F2 | Läufe, die ein Neustart unterbrochen hat | nichts erneut senden; `pending`/`sent` → `failed`, `accepted` → `unconfirmed`, mit Grund |
| F3 | Wer verwaltet die laufenden `Source`s? | eigener Dienst **Supervisor**; die Schleife beauftragt ihn per Nachricht, der Executor ruft über ihn `Execute` |
| F4 | Wo werden Rechte geprüft? | in der API (und im Preset-Runner vorab) über `auth.Allowed` gegen die `View`; Core und Executor prüfen nicht erneut |
| F5 | Wiederholungen der Weiterleitung | 3 Wiederholungen (1 s, 5 s, 30 s), Warteschlange pro Ziel, bei Überlauf die ältesten verwerfen |
| F6 | Wie wartet der Executor auf die Prüfbedingung? | Ereignisse (`FactsChanged`) abonnieren; wer auf Änderungen reagiert, abonniert (`core.md`) |
| F7 | Was bricht eine Preset-Stufe ab? | `failed` und `rejected`, nicht `unconfirmed`; Endzustände von Läufen sind endgültig |
