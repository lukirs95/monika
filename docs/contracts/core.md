# Vertrag: Core-Komponenten

**Status:** abgestimmt am 2026-10-09. **Pakete:** `internal/core` mit `inventory/`, `state/`, `evaluation/`. **Hintergrund:** `docs/ARCHITECTURE.md`, Abschnitt 3 (Datenfluss), 5, 6, 8 und 14; Treiberseite: [`sdk.md`](sdk.md).

Der Core hält Inventar, Fakten, Regeln, aktive Befunde und Health im Speicher und bewertet dort. Er ist der einzige, der über die Store-Interfaces schreibt (siehe `CLAUDE.md`).

## Grundsätze

- **Eine Schleife besitzt den Zustand.** Alle Eingaben (Meldungen der Treiber, Befehle der API, Timer) laufen als Nachrichten in **eine** Goroutine, die sie nacheinander verarbeitet. Kein Zustand im Speicher wird von zwei Goroutinen gleichzeitig angefasst; es braucht keine Locks, und jede Verarbeitung sieht einen in sich stimmigen Stand.
- **Die Komponenten sind Logik, keine Dienste.** `inventory`, `state` und `evaluation` haben keine eigenen Goroutinen. Die Schleife ruft sie in fester Reihenfolge auf und reicht die Änderungen der einen an die nächste weiter.
- **Lesen ohne Schleife.** Nach jeder Verarbeitung veröffentlicht die Schleife eine unveränderliche Ansicht (`View`). API, Influx-Schreiber und Aktions-Executor lesen sie ohne Wartezeit.
- **Änderungen werden als Ereignisse verteilt.** Wer reagieren muss (UI-Push, Weiterleitung, Aktions-Executor), abonniert sie.
- **Erst die Datenbank, dann der Speicher.** Inventar und Befunde werden geschrieben, bevor der Speicher sie übernimmt. Scheitert das Schreiben, bleibt der Speicher unverändert.

## Typen und Interfaces

```mermaid
classDiagram
    direction TB

    class Core {
        <<struct>>
        +Run(ctx) error
        +View() *View
        +Subscribe(buffer int) (<-chan Event, func())
        +Reporter(sourceID) sdk.Reporter
        +Do(ctx, cmd Command) error
    }
    class View {
        <<struct, unveränderlich>>
        +Version uint64
        +Elements map~ID, Element~
        +Facts map~ID, Facts~
        +Health map~ID, ElementHealth~
        +Findings map~ID, Finding~
        +Sources map~ID, SourceStatus~
    }
    class Event {
        <<interface>>
        +Version() uint64
        ElementsChanged
        FactsChanged
        FindingRaised
        FindingEnded
        FindingAcked
        HealthChanged
        SourceStatusChanged
    }
    class Command {
        <<interface>>
        CreateDevice, DeleteDevice
        CreateSource, UpdateSource
        CreateGroup, DeleteGroup
        UpdateUserFields
        AddGroupMember, RemoveGroupMember
        CreateRule, UpdateRule, DeleteRule
        AckFinding
    }

    class Inventory {
        <<inventory>>
        +Load(snap inventory.Snapshot)
        +ApplySnapshot(ctx, sourceID, s sdk.Snapshot) (Diff, error)
        +Apply(ctx, cmd Command) (Diff, error)
        +Element(id) (Element, bool)
        +ByExternalID(sourceID, extID) (ID, bool)
    }
    class Diff {
        <<struct>>
        +Added []ID
        +Changed []ID
        +Missing []ID
        +Returned []ID
        +Deleted []ID
        +LinksChanged bool
    }
    class Facts {
        <<state>>
        +Replace(sourceID, facts map~ID, sdk.Facts~) Changed
        +Merge(facts map~ID, sdk.Facts~) Changed
        +Drop(ids []ID)
        +Get(id, key) (any, bool)
    }
    class Liveness {
        <<state>>
        +Touch(sourceID, at)
        +SetConnected(sourceID, bool, err)
        +Expired(now) []ID
    }
    class Changed {
        <<struct>>
        +Elements map~ID, []string~
    }
    class Evaluator {
        <<evaluation>>
        +Load(rules []Rule, active []Finding)
        +Evaluate(ctx, now, elements []ID, v *View) ([]FindingChange, error)
        +Tick(ctx, now, v *View) ([]FindingChange, error)
        +Health(v *View, elements []ID) []ElementHealth
    }
    class FindingChange {
        <<struct>>
        +Raised *Finding
        +Ended ID
        +Reason ClearReason
        +CauseSet ID
    }

    Core *-- Inventory
    Core *-- Facts
    Core *-- Liveness
    Core *-- Evaluator
    Core ..> View : veröffentlicht
    Core ..> Event : verteilt
    Core ..> Command : nimmt an
    Inventory ..> Diff
    Facts ..> Changed
    Evaluator ..> FindingChange
```

| Typ | Bedeutung |
|---|---|
| `Core.Reporter(sourceID)` | die Umsetzung von `sdk.Reporter` für eine Quelle: verpackt jede Meldung als Nachricht an die Schleife |
| `Core.Do(ctx, cmd)` | Befehl der API (bzw. des Aktions-Executors); wartet, bis die Schleife ihn verarbeitet hat, und liefert den Fehler. Die Rechteprüfung liegt **vor** `Do`, in der API |
| `View` | unveränderlich; jede Verarbeitung erzeugt eine neue (`Version` steigt). Wer sie hält, sieht einen stimmigen Stand, auch wenn die Schleife weiterläuft |
| `Diff` | was die Discovery bzw. ein Befehl am Inventar geändert hat; `Missing`/`Returned` sind Wechsel von `present` |
| `Changed` | pro Element die Schlüssel der Fakten, die sich wirklich geändert haben |
| `Evaluator.Tick` | regelmäßig: Haltezeiten (`hold_seconds`) laufen ab, Lebenszeichen fehlen |

## Ablauf: Meldungen eines Treibers

### Vollständiger Stand (`Snapshot`)

```mermaid
sequenceDiagram
    autonumber
    participant R as Reporter (Quelle)
    participant L as Core-Schleife
    participant I as Inventory
    participant F as Facts
    participant E as Evaluator
    participant S as Store
    participant B as Abonnenten

    R->>L: Snapshot(s)
    L->>I: ApplySnapshot(sourceID, s)
    I->>I: external_id → ID, neue IDs vergeben, Differenz bilden
    I->>S: ApplyDiscovery (inkl. Audit)
    S-->>I: verschwundene IDs
    I-->>L: Diff
    L->>F: Drop(Diff.Missing)
    L->>F: Replace(sourceID, s.Facts)
    F-->>L: Changed
    L->>E: Evaluate(Diff ∪ Changed, View)
    E-->>L: FindingChanges
    L->>S: RaiseFinding / EndFinding / SetFindingCause
    L->>E: Health(betroffene Elemente und ihre Geräte, Gruppen)
    E-->>L: geänderte Health
    L->>L: neue View veröffentlichen
    L->>B: ElementsChanged, FactsChanged, FindingRaised/Ended, HealthChanged
    Note over L,S: Health geht gesammelt im Takt an SaveHealth, nicht pro Meldung
```

- `Replace` ersetzt alle Fakten der Quelle: Was im `Snapshot` fehlt, ist weg. `Update` dagegen ergänzt (`Merge`).
- Bewertet werden nur betroffene Elemente: geänderte Fakten, neue, verschwundene und zurückgekehrte Elemente, sowie Elemente, deren Links sich geändert haben (Ursache und Symptom).
- Schlägt `ApplyDiscovery` fehl, verwirft die Schleife den ganzen `Snapshot` und loggt; der nächste vollständige Stand des Treibers versucht es erneut.

### Änderung (`Update`) und Lebenszeichen

```mermaid
sequenceDiagram
    autonumber
    participant R as Reporter (Quelle)
    participant L as Core-Schleife
    participant F as Facts / Liveness
    participant E as Evaluator
    participant S as Store
    participant B as Abonnenten

    R->>L: Update(u)
    L->>F: Touch(sourceID, now)
    L->>F: Merge(u.Facts)
    F-->>L: Changed (nur echte Änderungen)
    opt etwas hat sich geändert
        L->>E: Evaluate(Changed, View)
        E-->>L: FindingChanges
        L->>S: Befunde schreiben
        L->>L: Health, neue View
        L->>B: Ereignisse
    end

    loop Takt der Schleife (1 s)
        L->>F: Expired(now)
        F-->>L: Quellen ohne Lebenszeichen seit 3 × Heartbeat
        L->>E: Tick(now, View)
        E-->>L: FindingChanges (source.unreachable, abgelaufene Haltezeiten)
        L->>S: Befunde schreiben, SaveHealth (gesammelt)
        L->>B: Ereignisse
    end
```

**Zusagen bei Last.** Läuft die Schleife hinterher, fasst der Reporter aufeinanderfolgende `Update`s einer Quelle zusammen. Wie er das tut, ist Sache der Implementierung; zugesagt ist:

- **Es gilt immer der neueste Wert** pro Element und Fakt, bei Alarmen die neueste Liste pro Element.
- **Zwischenwerte können entfallen.** Ein kurzer Wechsel (`signal_present` false und gleich wieder true) ist für die Bewertung dann womöglich nicht sichtbar. Regeln mit `hold_seconds` sollen so kurze Wechsel ohnehin nicht melden.
- **Die Reihenfolge bleibt erhalten:** Ein `Update` wird nie mit einem `Snapshot` zusammengefasst oder an ihm vorbei verarbeitet; `Connected` und `Disconnected` bleiben einzeln.
- **Treiber blockieren nicht**, auch wenn die Schleife hinterherhängt.

## Ablauf: Befehle der API

```mermaid
sequenceDiagram
    autonumber
    participant A as API
    participant L as Core-Schleife
    participant I as Inventory
    participant S as Store
    participant E as Evaluator
    participant B as Abonnenten

    A->>A: Rechte prüfen
    A->>+L: Do(ctx, DeleteDevice{actor, id})
    L->>I: Apply(DeleteDevice)
    I->>S: DeleteDevice(actor, id)
    S-->>I: ok bzw. InUseError
    I-->>L: Diff (Deleted, Missing)
    L->>L: Quellen des Geräts stoppen
    L->>E: Befunde und Health der betroffenen Elemente
    L->>L: neue View
    L->>B: Ereignisse (u. a. FindingEnded mit device_deleted)
    L-->>-A: nil bzw. Fehler
```

- Jeder Befehl ist eine Transaktion im Store **und** ein Schritt der Schleife. Die API sieht nach `Do` die neue `View`.
- Befehle, die Quellen betreffen (Gerät löschen, Quelle ändern, Secret ersetzen), lösen Start bzw. Stopp der Quelle aus (`sdk.md`, Stoppen).

## Abonnenten

Abonnenten (UI-Push pro Browserfenster, Weiterleitung, Aktions-Executor) bekommen Ereignisse über einen gepufferten Kanal. Die Schleife wartet nie auf einen Abonnenten und verwirft nie still ein Ereignis: Ist ein Puffer voll, **hängt sie den Abonnenten ab** und schließt seinen Kanal. Er merkt das, meldet sich neu an und liest die vollständige `View`.

```mermaid
sequenceDiagram
    autonumber
    participant A as Abonnent
    participant L as Core-Schleife

    A->>L: Subscribe(buffer)
    L-->>A: Kanal
    A->>L: View()
    L-->>A: View (Version 4711)
    loop Ereignisse
        L-->>A: Event (Version 4712, 4713, …)
        A->>A: Ereignisse mit Version ≤ 4711 verwerfen, spätere anwenden
    end
    Note over L: Puffer des Abonnenten voll
    L--xA: Kanal geschlossen (abgehängt)
    A->>L: Subscribe(buffer)
    A->>L: View()
    Note over A: wieder exakt auf Stand
```

- Erst anmelden, dann die `View` lesen; über `Version` geht dazwischen nichts verloren.
- **UI-Push:** Ein abgehängtes Browserfenster lädt den Stand neu.
- **Aktions-Executor:** prüft Bedingungen ohnehin gegen die `View`; nach dem Neuanmelden prüft er erneut.
- **Weiterleitung:** bekommt einen großen Puffer, weil jedes Ereignis zählt. Wird sie doch abgehängt, gehen die Ereignisse dazwischen verloren (best effort, `docs/ARCHITECTURE.md` Abschnitt 13); das wird geloggt.

## Lebenszyklus eines Regel-Befunds

```mermaid
stateDiagram-v2
    [*] --> Ok
    Ok --> Pending: Regel verletzt
    Pending --> Ok: wieder erfüllt vor Ablauf der Haltezeit
    Pending --> Active: hold_seconds abgelaufen (RaiseFinding)
    Ok --> Active: Regel verletzt, hold_seconds = 0
    Active --> Ok: erfüllt, bei Hysterese erst ab clear_value (EndFinding resolved)
    Active --> Ended: dauerhaft nicht mehr bewertet (EndFinding withdrawn)
    Active --> Active: Quelle nicht erreichbar (bleibt aktiv, Health unknown)
    Ended --> [*]
```

- `Pending` existiert nur im Speicher. Nach einem Neustart beginnen Haltezeiten neu.
- Befunde aus Gerätealarmen entstehen ohne Haltezeit, sobald der Alarm in der Liste steht, und enden (behoben), sobald er fehlt.

## Entscheidungen

| # | Frage | Entscheidung |
|---|---|---|
| C1 | Nebenläufigkeit | eine Schleife besitzt den gesamten Zustand; Komponenten sind Logik ohne eigene Goroutinen |
| C2 | Lesen | unveränderliche `View` nach jeder Verarbeitung, ohne Locks lesbar |
| C3 | Schreiben in die Datenbank | Inventar und Befunde sofort und synchron in der Schleife; Health gesammelt im Takt |
| C4 | Rückstau bei Last | Reporter fasst `Update`s zusammen (neuester Wert gilt, Zwischenwerte können entfallen, Reihenfolge bleibt); das Verfahren ist Sache der Implementierung |
| C5 | Takt der Schleife | 1 s für Lebenszeichen, Haltezeiten und `SaveHealth`; InfluxDB über einen eigenen Schreiber aus der `View` |
| C6 | Langsame Abonnenten | werden abgehängt, melden sich neu an und lesen die `View` (über `Version` lückenlos); Weiterleitung mit großem Puffer |
| C7 | Größe der `View` | strukturell geteilt: unveränderte Teile werden übernommen, nicht kopiert |
