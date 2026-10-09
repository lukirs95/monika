# Vertrag: Treiber-SDK

**Status:** abgestimmt am 2026-10-09. **Paket:** `sdk/` (öffentlich). **Hintergrund:** `docs/ARCHITECTURE.md`, Abschnitt 4, 5, 8, 9 und 11.

Das SDK ist die einzige Schnittstelle zwischen Core und Treibern. Treiber importieren nur `sdk/` und ihre Gerätelibrary, nie `internal/`. Alles, was ein Treiber vom Core braucht, kommt über die Typen und Interfaces dieses Vertrags.

## Grundsätze

- **Ein Treiber pro Gerätetyp, eine Instanz pro Quelle.** Der Treiber registriert sich einmal (`init()`); der Core erzeugt für jede Quelle (`device_source`) eine eigene `Source`.
- **Der Treiber treibt, der Core hört zu.** Jede `Source` läuft in einer eigenen Goroutine (`Run`) und meldet über den `Reporter`, was sie sieht. Ob sie dafür abfragt oder einen Event-Stream liest, entscheidet der Treiber. Wer abfragt, nutzt den `Scheduler` des Core, damit nicht alle Treiber gleichzeitig senden.
- **Treiber sprechen in `external_id`s.** UUIDs, Labels, Gruppen und Health kennt der Treiber nicht. Das Zugangs-Element der Quelle hat die leere `external_id` `""`.
- **Treiber melden Fakten, keine Bewertung.**

## Typen und Interfaces

```mermaid
classDiagram
    direction LR

    class Driver {
        <<struct>>
        +Name string
        +ConfigSchema json.RawMessage
        +Actions []ActionDef
        +Heartbeat time.Duration
        +New(cfg SourceConfig) (Source, error)
    }
    class SourceConfig {
        <<struct>>
        +Address string
        +Config json.RawMessage
        +Secret json.RawMessage
        +Logger *slog.Logger
        +Scheduler Scheduler
    }
    class Scheduler {
        <<interface>>
        +Every(ctx, interval time.Duration, fn func(ctx) error)
    }
    class Source {
        <<interface>>
        +Run(ctx, r Reporter) error
        +Execute(ctx, c Command) (Result, error)
    }
    class Reporter {
        <<interface>>
        +Connected()
        +Disconnected(err error)
        +Snapshot(s Snapshot)
        +Update(u Update)
        +Heartbeat()
    }
    class Snapshot {
        <<struct>>
        +Identity Identity
        +Elements []Element
        +Links []Link
        +Facts map~string, Facts~
        +Alarms map~string, []Alarm~
    }
    class Update {
        <<struct>>
        +Facts map~string, Facts~
        +Alarms map~string, []Alarm~
    }
    class Element {
        <<struct>>
        +ExternalID string
        +ParentID string
        +Kind string
        +Transport string
        +Direction string
        +Name string
        +GlobalRef string
        +Position *int
        +Attributes json.RawMessage
        +Actions []string
        +CriticalActions []string
        +Settings map~string, Setting~
    }
    class Link {
        <<struct>>
        +Type string
        +From Endpoint
        +To Endpoint
    }
    class Endpoint {
        <<struct>>
        +ExternalID string
        +GlobalRef string
    }
    class Alarm {
        <<struct>>
        +Code string
        +Severity string
        +Message string
    }
    class Command {
        <<struct>>
        +ExternalID string
        +Action string
        +Params json.RawMessage
    }
    class Result {
        <<struct>>
        +Status Status
        +Detail string
        +RejectedFields []string
        +Response json.RawMessage
    }
    class ActionDef {
        <<struct>>
        +Name string
        +Params json.RawMessage
        +Verify []Condition
        +Timeout time.Duration
        +Critical bool
    }
    class Counter {
        <<struct>>
        +Width int
        +Rate(raw uint64, at time.Time) (float64, bool)
    }
    class Identity {
        <<struct>>
        +Vendor string
        +Model string
        +Serial string
        +Firmware string
    }
    class Setting {
        <<struct>>
        +Type string
        +Min *float64
        +Max *float64
        +Values []string
        +Unit string
        +Critical bool
    }
    class Condition {
        <<struct>>
        +Fact string
        +Op string
        +Value string
    }

    Driver ..> SourceConfig : New
    SourceConfig *-- Scheduler
    Driver ..> Source : erzeugt
    Driver *-- ActionDef
    Source ..> Reporter : meldet über
    Source ..> Command : führt aus
    Source ..> Result : liefert
    Reporter ..> Snapshot
    Reporter ..> Update
    Snapshot *-- Identity
    Snapshot *-- Element
    Snapshot *-- Link
    Snapshot *-- Alarm
    Element *-- Setting
    ActionDef *-- Condition
    Link *-- Endpoint
```

| Typ | Bedeutung |
|---|---|
| `sdk.Register(Driver)` | Registrierung in `init()`; doppelte Namen sind ein Programmierfehler (`panic`) |
| `Driver.Actions` | **Katalog**: wie jede Aktion funktioniert, die der Treiber irgendwo anbietet (Parameter, Prüfbedingung, Timeout, `critical`). *Welches* Element sie unterstützt, steht in `Element.Actions`; `reboot` kann z. B. am Zugangs-Element (Gerät) und an einzelnen Modulen stehen |
| `Driver.Heartbeat` | erwarteter Abstand der Lebenszeichen; ohne Lebenszeichen für das **Dreifache** gilt die Quelle als nicht erreichbar. `0` = Sende-Ziel ohne Health-Check, nie `unknown` |
| `SourceConfig.Logger` | vom Core pro Quelle erzeugt, mit den Attributen `driver`, `source_id` und `device_id` (siehe Logging) |
| `SourceConfig.Secret` | entschlüsselter Klartext des Secrets der Quelle (JSON, Format legt der Treiber fest); leer ohne Secret. Nie loggen |
| `Facts` | `map[string]any`; Werte nur `bool`, `float64` oder `string`, keine Listen oder Objekte. **Zahlen sind immer `float64`**, auch Ganzzahlen (Treiber wandeln um); exakt bis 2⁵³. Andere Werte verwirft der Core mit Log-Eintrag. Schlüssel aus dem Vokabular |
| `Snapshot.Facts`, `.Alarms` | Schlüssel ist die `external_id` (`""` = Zugangs-Element) |
| `Update.Alarms` | pro Element die **vollständige** Liste der aktuellen Alarme; sie ersetzt die vorige. Ein Element ohne Eintrag bleibt unverändert |
| `Element.ParentID` | `external_id` des Elternelements; `""` = direkt unter dem Gerät |
| `Endpoint` | entweder `ExternalID` (Element dieser Quelle) oder `GlobalRef` (Element einer anderen Quelle, ggf. noch unbekannt) |
| `Result.Status` | `Accepted` oder `Rejected`; ein `error` aus `Execute` bedeutet `failed` (nicht erreicht, Transportfehler) |
| `Setting` | schreibbarer Fakt-Schlüssel: `Type` (`int`, `float`, `bool`, `string`, `enum`, `ipv4` …), Grenzen bzw. `Values` bei `enum`, `Critical` |
| `Condition.Value` | JSON-Wert als Text, darf Platzhalter enthalten |
| `ActionDef.Verify` | Prüfbedingung mit Platzhaltern aus den Parametern (z. B. `bitrate eq {{bitrate}}`); bei `set` erzeugt der Core sie selbst aus den gesetzten Werten |

**Vokabular** (`sdk/vocab`): Konstanten für `kind`, `transport`, Kern- und bekannte Fakten, Kern-Aktionen. Reserviert und für Treiber verboten: `kind` `device`, `group`, `access`; Fakt `present`.

## Pflichten im Ablauf

### Start, Verbindung und vollständiger Stand

```mermaid
sequenceDiagram
    autonumber
    participant C as Core (Supervisor)
    participant D as Driver
    participant S as Source
    participant G as Gerät

    C->>D: New(SourceConfig)
    D-->>C: Source
    C->>+S: go Run(ctx, Reporter)
    S->>G: verbinden
    G-->>S: verbunden
    S->>C: Connected()
    S->>G: vollständigen Stand lesen
    S->>C: Snapshot(Elemente, Links, alle Fakten, alle Alarme)
    loop solange verbunden
        G-->>S: Änderung bzw. Abfrage-Ergebnis
        S->>C: Update(geänderte Fakten, Alarme)
        S->>C: Heartbeat() (wenn nichts zu melden ist)
    end
    G--xS: Verbindung verloren
    S->>C: Disconnected(err)
    S->>G: neu verbinden (eigene Wartezeit)
    G-->>S: verbunden
    S->>C: Connected()
    S->>C: Snapshot(vollständig)
    C->>S: ctx abgebrochen (Quelle deaktiviert, Konfiguration geändert, Stopp)
    S-->>-C: Run kehrt zurück
```

- Nach **jedem** `Connected()` folgt ein **vollständiger** `Snapshot`, bevor `Update`s kommen. Der Core kennt nach einem Neustart nichts und verlässt sich darauf.
- Ändert sich die **Struktur** (Element neu oder weg, Link neu oder weg, Aktionen, Einstellungen), meldet der Treiber erneut einen vollständigen `Snapshot`. Der Core vergleicht selbst; der Treiber muss keine Differenzen bilden.
- Jeder Aufruf am `Reporter` zählt als Lebenszeichen. `Heartbeat()` braucht nur, wer längere Zeit nichts zu melden hat (Event-Streams).
- Reporter-Aufrufe kommen aus einer Goroutine nacheinander; der `Reporter` blockiert nicht lange.

### Fehler, Panic und Neustart

```mermaid
stateDiagram-v2
    [*] --> Starting: Quelle aktiviert
    Starting --> Connecting: Run gestartet
    Connecting --> Live: Connected + Snapshot
    Live --> Live: Update / Heartbeat
    Live --> Connecting: Disconnected
    Live --> Unreachable: keine Lebenszeichen (3 × Heartbeat)
    Connecting --> Unreachable: keine Verbindung (3 × Heartbeat)
    Unreachable --> Live: Connected + Snapshot
    Starting --> Backoff: New liefert Fehler
    Connecting --> Backoff: Run endet mit Fehler oder Panic
    Live --> Backoff: Run endet mit Fehler oder Panic
    Backoff --> Starting: Wartezeit abgelaufen
    Live --> Stopping: ctx abgebrochen
    Connecting --> Stopping: ctx abgebrochen
    Unreachable --> Stopping: ctx abgebrochen
    Stopping --> [*]: Run kehrt zurück
    Stopping --> Stuck: Run nach 10 s nicht zurückgekehrt
    Stuck --> [*]: Run kehrt doch noch zurück
```

- **`Unreachable`** erzeugt den Befund `source.unreachable` am Zugangs-Element; alle anderen Elemente der Quelle werden `unknown`.
- **Panic** in `Run` oder `Execute` fängt der Core (`recover`), meldet sie als `source.unreachable` mit Fehlertext und startet die Quelle nach einer Wartezeit neu.
- **Stoppen:** Der Core bricht den `ctx` von `Run` ab (Quelle deaktiviert, Gerät gelöscht, Konfiguration oder Secret geändert, Monika beendet). `Run` muss danach zügig zurückkehren und alle Verbindungen schließen.
- **Hängender Treiber (`Stuck`):** Kehrt `Run` nach 10 s nicht zurück, meldet der Core das als `source.unreachable` mit Grund „Treiber reagiert nicht“. Go kann die Goroutine nicht beenden; der Core startet deshalb **keine zweite Instanz**, solange die alte läuft. Sonst hätte das Gerät zwei gleichzeitige Sitzungen.
- **Konfiguration oder Secret geändert:** Stoppen wie oben, danach Neustart mit der neuen `SourceConfig`. Kein Live-Update im Treiber.
- **Wiederverbinden** ist Sache des Treibers (bzw. seiner Gerätelibrary). Der Core startet eine Quelle nur neu, wenn `Run` mit Fehler endet oder eine Panic auftritt.

### Ausführung

```mermaid
sequenceDiagram
    autonumber
    participant E as Core (Aktions-Executor)
    participant S as Source
    participant G as Gerät
    participant C as Core (Zustand)

    E->>+S: Execute(ctx, Command{ExternalID, Action, Params})
    S->>G: Befehl
    alt Gerät nimmt an
        G-->>S: ok
        S-->>E: Result{Accepted}
        S->>C: Update(neuer Zustand) über Run
        E->>C: Prüfbedingung gegen Fakten bis Timeout
    else Gerät lehnt ab
        G-->>S: Fehler mit Feldern
        S-->>E: Result{Rejected, RejectedFields, Response}
    else nicht erreichbar
        S-->>-E: error
    end
```

- `Execute` kann gleichzeitig zu `Run` und für verschiedene Elemente parallel aufgerufen werden; der Treiber muss das aushalten. Für **ein** Element ruft der Core nacheinander auf (FIFO).
- `Execute` prüft Parameter gegen die Grenzen aus `settings` bzw. das Parameter-Schema und lehnt ungültige mit `Rejected` ab.
- **Sammelaktionen auf ein Gerät:** Mit angegebenem Treiber („`reboot` über `videoxlink`“) geht die Aktion nur an das Zugangs-Element dieses Treibers (das Gerät); ohne Treiber an alle übrigen Elemente, die sie unterstützen (z. B. alle Module). Der Treiber sieht davon nur einzelne `Execute`-Aufrufe.
- Erfolg ist der beobachtete Zustand, nicht die Antwort: Die Wirkung meldet der Treiber über `Update`, der Core prüft.

## Scheduler für abfragende Treiber

Ein Komfortangebot des Core: Der Treiber meldet seine periodische Arbeit an, der Scheduler bestimmt den Takt. Was der Treiber im Takt tut, ist seine Sache. Treiber mit Event-Stream brauchen ihn nicht.

```mermaid
sequenceDiagram
    participant S as Source (Run)
    participant K as Scheduler (Core)
    participant G as Gerät
    participant C as Core (Reporter)

    S->>K: Every(ctx, 2s, poll)
    Note over K: Versatz aus der Quellen-ID,<br/>z. B. 0,7 s in jedem 2-s-Intervall
    loop jedes Intervall, solange ctx lebt
        K->>K: globale Obergrenze abwarten (Semaphore)
        alt voriger Aufruf läuft noch
            K->>K: Takt auslassen
        else
            K->>+S: poll(ctx mit Timeout)
            S->>G: abfragen
            G-->>S: Werte
            S->>C: Update(…)
            S-->>-K: nil
        end
    end
```

| Mechanismus | Wirkung |
|---|---|
| **Phasenversatz** | Jede Aufgabe bekommt einen festen Versatz innerhalb ihres Intervalls, abgeleitet aus der ID der Quelle; viele Quellen verteilen sich gleichmäßig statt gleichzeitig zu senden |
| **Globale Obergrenze** | höchstens N Aufrufe gleichzeitig über alle Treiber (`MONIKA_SCHEDULER_CONCURRENCY`, Standard 32); weitere warten |
| **Keine Überlappung** | Läuft der vorige Aufruf einer Aufgabe noch, wird der nächste Takt ausgelassen, nicht gestapelt |
| **Timeout pro Aufruf** | Standard = Intervall. Kehrt `fn` trotzdem nicht zurück, gilt die Aufgabe als hängend: Der Scheduler startet für sie nichts mehr und meldet das. Die Zahl der Goroutinen wächst so nie unbegrenzt |

Ein Fehler aus `fn` wird geloggt; die Aufgabe läuft weiter. Ob die Quelle erreichbar ist, entscheiden allein die Lebenszeichen.

## Zähler-Helfer

```mermaid
sequenceDiagram
    participant S as Source
    participant K as sdk.Counter
    S->>K: Rate(1200, t1)
    K-->>S: 0, false (erster Wert)
    S->>K: Rate(1260, t2)
    K-->>S: 30.0, true (pro Sekunde)
    S->>K: Rate(5, t3)
    K-->>S: 0, true (Reset erkannt)
```

- Ein `Counter` pro Zähler und Element; bei angegebener `Width` (32, 64) rechnet er Überläufe richtig, sonst gilt jeder kleinere Wert als Reset.
- **`at` ist der Messzeitpunkt** und kommt vom Treiber: der Zeitstempel des Geräts, wenn es einen liefert, sonst `time.Now()` zum Zeitpunkt der Abfrage. So bleibt die Rate richtig, auch wenn Werte verzögert ankommen.
- Raten gibt es nur für Zähler. `Rate` liefert `float64`; den Rohwert meldet der Treiber ebenfalls als `float64`. `bool`- und `string`-Fakten meldet er direkt.
- **Warum intern `uint64`:** Zähler sind Ganzzahlen ohne Vorzeichen. Differenz und Überlauf (modulo 2³² bzw. 2⁶⁴) sind nur ganzzahlig exakt; `float64` rundet Ganzzahlen ab 2⁵³. Gewandelt wird erst die fertige Rate.
- **Grenze beim Rohwert:** Als Fakt ist der Rohwert `float64` (S6) und ab 2⁵³ gerundet, z. B. ein 64-Bit-Bytezähler einer 100-G-Schnittstelle nach etwa 8 Tagen Dauerlast. Das betrifft nur die Anzeige; die Rate bleibt exakt. Regeln gehören auf die Rate, nicht auf große Rohzähler.
- Zähler, die ein Gerät als Dezimalzahl liefert, deckt der `Counter` nicht ab; ein Helfer dafür kommt, wenn ein Treiber ihn braucht.
- Der Treiber meldet den Rohwert (`crc_errors`) und, sobald `ok`, die Rate (`crc_errors.rate`).

## Logging

- Treiber loggen **ausschließlich** über `SourceConfig.Logger`, nie über `slog.Default()`, das Paket `log` oder `fmt.Print*`. Jede Zeile ist so ihrer Quelle zugeordnet.
- Stufen: `Debug` für Protokolldetails, `Info` für Verbinden und Trennen, `Warn` für Fehler, die der Treiber selbst behebt, `Error` für Fehler, die er nicht behebt.
- Werte aus `SourceConfig.Secret` werden nie geloggt.
- **Gerätelibraries** nehmen einen `*slog.Logger` (oder `slog.Handler`) als Option an; ohne Angabe geben sie nichts aus bzw. nutzen `slog.Default()`. Für eigene Libraries wie `goxlinkclient` ist das Pflicht.

Fremde Libraries mit eigenem Logging:

| Library | Vorgehen | Zuordnung zur Quelle |
|---|---|---|
| nimmt `*slog.Logger` bzw. `slog.Handler` | `cfg.Logger` durchreichen | ja |
| eigenes Logger-Interface (`Printf`, logrus, zap …) | Adapter im Treiberpaket, der nach `slog` schreibt | ja |
| Standard-Paket `log` | Monika setzt beim Start `slog.SetDefault`; seit Go 1.21 landet damit auch `log.Printf` strukturiert im selben Handler | nein (global) |
| schreibt direkt auf `os.Stdout` | nicht abzufangen; Library anpassen oder forken | nein |

Eine Isolation pro Library gibt es in einem Prozess nicht (keine Goroutine-lokalen Variablen, `os.Stdout` ist global); sie wäre nur mit eigenen Treiber-Prozessen möglich, die `docs/ARCHITECTURE.md` (Abschnitt 4) verwirft.

## Entscheidungen

| # | Frage | Entscheidung |
|---|---|---|
| S1 | Wer treibt? | Der Treiber (`Run` + `Reporter`); für abfragende Treiber gibt es den `Scheduler` des Core |
| S2 | Struktur als vollständiger `Snapshot` oder als Differenz? | vollständiger `Snapshot`; der Core vergleicht |
| S3 | Wo steht der erwartete Abstand der Lebenszeichen? | fest pro Treiber (`Driver.Heartbeat`), nicht konfigurierbar; nicht erreichbar nach dem Dreifachen |
| S4 | Wie kommen Secret- und Konfigurationsänderungen an? | Neustart der Quelle mit neuer `SourceConfig` |
| S5 | `Execute` an der `Source` oder eigenes Interface? | an der `Source` |
| S6 | Typ der Fakt-Werte | `bool`, `float64` oder `string`; Zahlen immer `float64`, damit z. B. Temperaturen nicht gerundet werden und Vergleiche eindeutig sind; andere verwirft der Core |
| S7 | Wer verbindet neu? | der Treiber bzw. seine Gerätelibrary |
| S8 | Hängender Treiber | 10 s nach dem Stopp `Stuck`; keine zweite Instanz, solange die alte läuft |
| S9 | Sammelaktion auf ein Gerät | mit Treiber an das Zugangs-Element, ohne Treiber an die übrigen Elemente |
| S10 | Logging | nur über `SourceConfig.Logger` (mit `driver`, `source_id`, `device_id`); Gerätelibraries nehmen einen `*slog.Logger` an; `log`-Ausgaben fremder Libraries landen global über `slog.SetDefault` |
