# Vertrag: Core-Komponenten

**Status:** abgestimmt am 2026-10-09, überarbeitet am 2026-10-10 (Architektur-Audit) und 2026-10-11 (Vokabular, Kennzahlen, Datenbank-Wächter). **Pakete:** `internal/core` mit `inventory/`, `state/`, `evaluation/`. **Hintergrund:** `docs/ARCHITECTURE.md`, Abschnitt 3 (Datenfluss), 5, 6, 8 und 14; Treiberseite: [`sdk.md`](sdk.md).

Der Core hält Inventar, Fakten, Regeln, aktive Befunde und Health im Speicher und bewertet dort. Er ist der einzige, der über die Store-Interfaces schreibt (siehe `CLAUDE.md`).

## Grundsätze

- **Eine Schleife besitzt den Zustand.** Alle Eingaben (Meldungen der Treiber, Befehle der API, Timer) laufen als Nachrichten in **eine** Goroutine, die sie nacheinander verarbeitet. Kein Zustand im Speicher wird von zwei Goroutinen gleichzeitig angefasst; es braucht keine Locks, und jede Verarbeitung sieht einen in sich stimmigen Stand.
- **Die Komponenten sind Logik, keine Dienste.** `inventory`, `state` und `evaluation` haben keine eigenen Goroutinen. Die Schleife ruft sie in fester Reihenfolge auf und reicht die Änderungen der einen an die nächste weiter.
- **Dienste laufen nebenher.** Supervisor (Quellen), Executor, Preset-Runner, Weiterleitung und Influx-Schreiber haben eigene Goroutinen und eigenen Zustand, aber nie Zugriff auf den Zustand der Schleife. Sie sprechen mit ihr nur über Nachrichten (`Do`, `Post`, `Reporter`, Aufträge an den Supervisor), Ereignisse und die `View` (siehe [`flows.md`](flows.md), [`services.md`](services.md)).
- **Lesen ohne Schleife.** Nach jeder Verarbeitung veröffentlicht die Schleife eine unveränderliche Ansicht (`View`). API, Influx-Schreiber und Aktions-Executor lesen sie ohne Wartezeit.
- **Reagieren über Ereignisse, abtasten über die `View`.** Wer auf eine Änderung reagiert (UI-Push, Weiterleitung, Prüfbedingung des Executors), abonniert Ereignisse und fragt nicht ab. Wer im festen Takt einen Stand abtastet (Influx-Schreiber), liest die `View` im Takt. Einmal lesen nach dem Anmelden gehört zum Abonnieren (siehe Abonnenten).
- **Änderungen werden als Ereignisse verteilt.** Wer reagieren muss (UI-Push, Weiterleitung, Aktions-Executor), abonniert sie.
- **Erst die Datenbank, dann der Speicher.** Inventar und Befunde werden geschrieben, bevor der Speicher sie übernimmt. Scheitert das Schreiben, bleibt der Speicher unverändert.
- **Die Datenbank hält die Schleife nie an.** Jedes Schreiben in der Schleife hat ein Timeout (2 s). Ein gescheiterter Befehl liefert seinen Fehler an `Do`; ein gescheitertes Entstehen oder Enden eines Befunds bleibt im Evaluator offen und wird im Takt wiederholt; ein gescheiterter `Snapshot` wird gemerkt und wiederholt (siehe unten). Fakten im Speicher, `View`, Ereignisse und InfluxDB laufen weiter. Den Zustand der Datenbank führt die `View` (`DB`).

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
        +Post(m Message)
    }
    class View {
        <<struct, unveränderlich>>
        +Version uint64
        +Graph *inventory.Graph
        +Facts map~ID, Facts~
        +Health map~ID, ElementHealth~
        +Findings map~ID, Finding~
        +Rules *evaluation.Rules
        +Runs map~ID, Run~
        +Sources map~ID, SourceStatus~
        +DB DBStatus
        +History InfluxStatus
        +Stats Stats
        +InScope(scope, element ID) bool
        +EffectiveRules(element ID) []RuleSet
    }
    class SourceStatus {
        <<struct>>
        +State SourceState
        +Heartbeat time.Duration
        +LastSeen time.Time
        +Error string
    }
    class Message {
        <<interface>>
        SourceStarted, SourceStopped
        SourceFailed, SourceStuck
        RunChanged
        InfluxStatus
        DBPing
    }
    class Event {
        <<interface>>
        +Version() uint64
        ElementsChanged
        FactsChanged
        FindingRaised
        FindingEnded
        FindingAcked
        FindingCauseChanged
        HealthChanged
        RunChanged
        SourceStatusChanged
    }
    class Command {
        <<interface>>
        CreateDevice, DeleteDevice
        CreateSource, UpdateSource, DeleteSource
        CreateGroup, DeleteGroup
        UpdateUserFields
        AddGroupMember, UpdateGroupMember, RemoveGroupMember
        CreateRule, UpdateRule, DeleteRule
        AckFinding
        ReplaceSecret
    }

    class Inventory {
        <<inventory>>
        +Load(snap inventory.Snapshot)
        +ApplySnapshot(ctx, sourceID, s sdk.Snapshot) (Diff, error)
        +CreateDevice(ctx, actor, e) (Diff, error)
        +DeleteDevice(ctx, actor, id) (Diff, error)
        +CreateSource(ctx, actor, s) (Diff, error)
        +UpdateSource(ctx, actor, s) (Diff, error)
        +DeleteSource(ctx, actor, id) (Diff, error)
        +CreateGroup(ctx, actor, e) (Diff, error)
        +DeleteGroup(ctx, actor, id) (Diff, error)
        +UpdateUserFields(ctx, actor, id, f) (Diff, error)
        +AddGroupMember(ctx, actor, l) (Diff, error)
        +UpdateGroupMember(ctx, actor, l) (Diff, error)
        +RemoveGroupMember(ctx, actor, id) (Diff, error)
        +Element(id) (Element, bool)
        +ByExternalID(sourceID, extID) (ID, bool)
        +Graph() *Graph
        +FactsByID(sourceID, facts map~string, sdk.Facts~) (map~ID, sdk.Facts~, unknown []string)
        +Path(id) string
    }
    class Graph {
        <<inventory, unveränderlich>>
        +Element(id) (Element, bool)
        +Source(id) (Source, bool)
        +Link(id) (Link, bool)
        +All() iter.Seq2~ID, Element~
        +Sources() iter.Seq2~ID, Source~
        +Links() iter.Seq2~ID, Link~
        +Outgoing(id) iter.Seq~Link~
        +Incoming(id) iter.Seq~Link~
        +Children(id) iter.Seq~ID~
        +Ancestors(id) iter.Seq~ID~
        +Scopes(id) map~ID, int~
        +InScope(scope, id) bool
        +ScopeElements(scope) iter.Seq~ID~
        +Monitored(id) bool
        +Path(id) string
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
        +Merge(sourceID, facts map~ID, sdk.Facts~) Changed
        +Drop(ids []ID) Changed
        +DropSource(sourceID) Changed
        +Get(id, key) (any, bool)
        +Element(id) sdk.Facts
        +Snapshot() map~ID, sdk.Facts~
    }
    class Liveness {
        <<state>>
        +Starting(sourceID, at)
        +Started(sourceID, heartbeat, at)
        +Connected(sourceID, at)
        +SnapshotApplied(sourceID, at)
        +Disconnected(sourceID, err, at)
        +Touch(sourceID, at)
        +Failed(sourceID, err, at)
        +Stopping(sourceID, at)
        +Stuck(sourceID, at)
        +Remove(sourceID)
        +Live(sourceID, now) bool
        +Status(sourceID, now) (Status, bool)
        +Unreachable(now) []ID
    }
    class Changed {
        <<map ID → []string>>
    }
    class SourceSpec {
        <<struct>>
        +SourceID ID
        +DeviceID ID
        +Driver string
        +Address string
        +Config json.RawMessage
        +SecretID ID
    }
    class Supervisor {
        <<interface, beim Core>>
        +Start(spec SourceSpec)
        +Stop(sourceID)
    }
    class Secrets {
        <<interface, beim Core>>
        +Replace(ctx, actor, id, value json.RawMessage) error
    }
    class Evaluator {
        <<evaluation>>
        +Load(rules []Rule, active []Finding, health []ElementHealth)
        +Evaluate(ctx, now, elements []ID, s State) ([]FindingChange, error)
        +Alarms(ctx, now, sourceID, alarms map~ID, []sdk.Alarm~, replace bool, s State) ([]FindingChange, error)
        +Tick(ctx, now, s State) ([]FindingChange, error)
        +Discovery(ctx, now, sourceID, err error, s State) ([]FindingChange, error)
        +CreateRule(ctx, now, actor, r Rule, s State) ([]FindingChange, error)
        +UpdateRule(ctx, now, actor, r Rule, s State) ([]FindingChange, error)
        +DeleteRule(ctx, now, actor, id, s State) ([]FindingChange, error)
        +AckFinding(ctx, now, actor, id) (Finding, error)
        +Rules() *Rules
        +Findings() map~ID, Finding~
        +Health(s State, elements []ID) []ElementHealth
        +Healths() map~ID, ElementHealth~
    }
    class State {
        <<interface, evaluation>>
        +Graph() *inventory.Graph
        +Facts(elementID) sdk.Facts
        +Live(sourceID) bool
        +Unreachable(sourceID) (reason string, ok bool)
        +Heartbeat(sourceID) time.Duration
    }
    class Rules {
        <<evaluation, unveränderlich>>
        +Rule(id) (Rule, bool)
        +All() iter.Seq~Rule~
        +EffectiveRules(s State, element ID) []RuleSet
    }
    class RuleSet {
        <<struct>>
        +Fact string
        +Direction Direction
        +Rules []Rule
        +Overridden []Rule
    }
    class FindingChange {
        <<struct>>
        +Raised *Finding
        +Ended ID
        +Reason ClearReason
        +CauseSet ID
        +CausedBy ID
    }

    Core *-- Inventory
    Core *-- Facts
    Core *-- Liveness
    Core *-- Evaluator
    Core ..> Supervisor : Start / Stop
    Core ..> Secrets : ReplaceSecret
    Supervisor ..> SourceSpec
    Core ..> View : veröffentlicht
    Core ..> Event : verteilt
    Core ..> Command : nimmt an
    Core ..> Message : nimmt an
    View *-- SourceStatus
    View *-- Graph
    Inventory ..> Graph
    Inventory ..> Diff
    Facts ..> Changed
    Evaluator ..> FindingChange
    Evaluator *-- Rules
    View *-- Rules
    Rules ..> RuleSet
```

| Typ | Bedeutung |
|---|---|
| `Core.Reporter(sourceID)` | die Umsetzung von `sdk.Reporter` für eine Quelle: verpackt jede Meldung als Nachricht an die Schleife |
| `Core.Do(ctx, cmd)` | Befehl der API; wartet, bis die Schleife ihn verarbeitet hat, und liefert den Fehler. Die Rechteprüfung liegt **vor** `Do`, in der API |
| `Core.Post(m)` | Nachricht eines Dienstes ohne Warten auf die Verarbeitung; blockiert höchstens kurz beim Einreihen. Supervisor: `SourceStarted`, `SourceStopped`, `SourceFailed{err, panic}`, `SourceStuck`; Executor: `RunChanged{run}`; Influx-Schreiber: `InfluxStatus`; Datenbank-Wächter: `DBPing{ok, err}` |
| `View.Graph` | Elemente, Quellen und Links mit Index pro Element (Kinder, Vorfahren, Mitgliedschaften, Links ausgehend und eingehend); für `GET /state`, Geltungsbereiche, Ursache/Symptom und die Anzeige von Verbindungen über `represents`. Baut das Inventar nach jeder Änderung neu (`Inventory.Graph`), sonst wird er übernommen |
| `Graph.Scopes(id)` | alle Geltungsbereiche, die das Element umfassen, mit Abstand: das Element selbst (0), seine Vorfahren über `parent_id` und die Gruppen, die es oder einen Vorfahren rekursiv enthalten; der Abstand zählt Schritte über Eltern und Mitgliedschaften zusammen (Präzedenz der Regeln) |
| `Graph.ScopeElements(scope)` | alle Elemente im Geltungsbereich, jedes genau einmal, mit dem Geltungsbereich selbst; `uuid.Nil` = alle |
| `Graph.Monitored(id)` | effektives `monitored`: das Element und alle Vorfahren überwacht, die Quelle aktiviert |
| `View.Rules`, `View.Runs` | alle gültigen Regeln, geprüft und unveränderlich (`evaluation.Rules`, vom Evaluator bei jeder Änderung ersetzt); laufende Läufe (ein Lauf fällt mit seinem Endzustand heraus) |
| Prüfung einer Regel | gegen den Fakt-Katalog (`evaluation.RegistryFactTypes`: Vokabular, `present` als `bool`, rohe Fakten beim Treiber ihres Präfixes): Fakt bekannt, Operator passt zum Typ (`lt` … `gte` nur für Zahlen, `in`/`not_in` mit nicht leerer Liste), Wert passt zum Fakt, Hysterese nur bei Ober- oder Untergrenze und auf der guten Seite (`lte 80` mit `clear_value 75`), dazu Schwere, Haltezeiten ≥ 0, bekannte `match_kind`/`match_transport`. Sonst `ErrInvalidRule`. Regeln aus der Datenbank, die nicht mehr passen (Treiber nicht mehr registriert), bleiben beim Laden draußen und werden geloggt |
| Vergleich | ganze Zahlen exakt als `int64`, sonst `float64`; Fakt und Wert vom falschen Typ: Die Regel gilt nicht |
| `View.InScope(scope, element)` | = `Graph.InScope`; **die** Auflösung von Geltungsbereichen für Regeln, Rechte, Weiterleitung, Verlauf und Watch: NULL = alles; Element = es selbst und alle Nachfahren über `parent_id`; Gruppe = Mitglieder rekursiv, jeweils mit Nachfahren |
| `View.EffectiveRules(element)` | = `Rules.EffectiveRules(View, element)`: pro Fakt und Richtung die geltenden (gewinnende Stufe) und die überschriebenen Regeln (für die UI); dieselbe Auswahl verwendet der Evaluator |
| `SourceStatus.State` | `starting`, `connecting`, `live`, `backoff`, `unreachable`, `stopping`, `stuck`, `disabled`; aus `Reporter` und Nachrichten des Supervisors. `Heartbeat` ist `Source.Heartbeat()` der laufenden Instanz |
| `View.DB` | Datenbank erreichbar: letzter Ping **und** letzte Schreibvorgänge erfolgreich, höchstens 5 s alt. Ein kleiner Wächter (eigene Goroutine des Core) pingt alle 5 s mit 2 s Timeout und meldet `DBPing`. Ist er falsch, meldet die API `503` an `GET /healthz` und die UI zeigt ein Banner; `/healthz` liest nur diesen Wert |
| `View.History` | Zustand des Influx-Schreibers (`influx.md`); nur Anzeige, ohne Wirkung auf `/healthz` |
| `View.Stats` | Kennzahlen der Schleife, bei jeder Veröffentlichung gesetzt: `LoopLag` (Wartezeit der ältesten Nachricht), `LoopQueue`, `Subscribers`, `SubscribersDropped` und `Events` (beide Zähler seit dem Start). Der Influx-Schreiber schreibt sie mit seinen eigenen und dem Zähler der SSE-Ströme der API in das Measurement `monika` (`docs/OPERATIONS.md`) |
| `State` | was die Bewertung vom Zustand liest. Ein Interface beim Verwender, weil `core` `evaluation` importiert und nicht umgekehrt. Die Schleife reicht ihren **aktuellen** Stand (Inventar, Fakten, Lebenszeichen) als `State`, nicht die zuletzt veröffentlichte `View`; `View.EffectiveRules` nutzt einen Adapter über die `View` |
| Paketgrenzen | Dienste importieren den Core, nie umgekehrt. Was der Core von ihnen braucht, definiert er selbst: `Supervisor{Start(SourceSpec), Stop(id)}` und `SourceSpec`, `Secrets{Replace(ctx, actor, id, value)}` (erfüllt von `secret.Service`). `View.Runs` und `RunChanged` verwenden `action.Run`; `internal/action` enthält deshalb nur Typen |
| `Reporter` | nie blockierend: pro Quelle ein Postfach mit geordneter Liste aus `Connected`, `Snapshot`, `Disconnected` und `Update`. Ein `Update` wird in ein direkt davor stehendes `Update` gemischt (neuester Wert pro Element und Fakt, neueste Alarmliste pro Element), nie über ein anderes hinweg. Die Quelle steht höchstens einmal in der Warteschlange der Schleife, bis diese ihr Postfach geleert hat. Jeder Start bekommt ein neues Postfach (`Reporter(sourceID)`); Meldungen einer früheren Instanz verwirft die Schleife. `SourceStarted`, `SourceStopped`, `SourceFailed` und `SourceStuck` stellt `Post` in das Postfach der aktuellen Instanz, damit sie in Reihenfolge mit deren Meldungen verarbeitet werden |
| Warteschlange | eine gemeinsame für `Do`, `Post` und Postfächer, Puffer 1024; `Post` blockiert nur, wenn sie voll ist; `Do` wartet auf die Antwort oder `ctx`. Jeder Eintrag trägt den Zeitpunkt des Einreihens (`LoopLag`) |
| Veröffentlichen | Die Schleife arbeitet alle gerade wartenden Einträge ab und veröffentlicht **dann einmal** eine neue `View`; alle Ereignisse dieser Runde tragen deren `Version`. `Do` antwortet erst danach, die API sieht also die neue `View` |
| Ereignisse | `ElementsChanged{Added, Changed, Missing, Returned, Deleted, LinksChanged}`, `FactsChanged{Changed (Schlüssel pro Element), Facts (neue Fakten der Elemente)}`, `FindingRaised{Finding}`, `FindingEnded{Finding mit ClearedAt, Reason}`, `FindingAcked{Finding}`, `FindingCauseChanged{FindingID, CausedBy}`, `HealthChanged{[]ElementHealth}`, `RunChanged{Run}` (derselbe Typ wie die Nachricht des Executors), `SourceStatusChanged{SourceID, Status}` |
| `Evaluator.Alarms` | Gerätealarme einer Quelle: bei `Snapshot` (`replace`) ersetzen sie alle Alarme der Quelle, bei `Update` nur die der genannten Elemente. Ein neuer Alarm erzeugt sofort einen Befund, ein fehlender beendet ihn als behoben |
| Schreiben der Befunde | nur der Evaluator ruft `evaluation.Store` (`CLAUDE.md`), jeder Aufruf mit 2 s Timeout. **Entstehen gescheitert:** Der Befund gilt noch nicht als aktiv (weder in der `View` noch als Ereignis), `Tick` wiederholt; fällt sein Grund vorher weg, wird er verworfen. **Enden gescheitert:** Der Befund bleibt aktiv, bis `Tick` das Schreiben wiederholt hat; `ErrNotFound` zählt als Erfolg. Die Methoden liefern die gelungenen Änderungen und die gesammelten Fehler |
| Abgleich | Pro Element berechnet der Evaluator die Befunde, die sein sollen: Regel (verletzte Regeln der gewinnenden Stufe), Gerät (Codes der zuletzt gemeldeten Alarme), System am Zugang (`source.unreachable` laut `State.Unreachable`, `source.discovery_failed` laut letztem `Discovery`). Regel- und Gerätebefunde nur, wenn die Quelle `Live` ist, sonst bleiben sie eingefroren (auch eine abgelaufene Haltezeit wartet); Systembefunde immer, `Tick` prüft dafür jede Sekunde alle Zugänge. Ein verschwundenes Element hat für die Bewertung nur `present` und keine Alarme. Pro Element kommen erst die beendeten, dann die neuen Befunde |
| Grund des Zurückziehens | der erste passende in dieser Reihenfolge: `deleted` (Element nicht mehr im Graph), `rule_deleted`, `rule_disabled`, `source_disabled`, `unmonitored`, `element_missing`, `fact_missing`, sonst `rule_changed` (Geltungsbereich, Filter oder Präzedenz). `deleted`, `rule_deleted` und `rule_disabled` hat der Store schon in derselben Transaktion geschrieben; der Evaluator beendet sie nur im Speicher und meldet sie |
| `Evaluator.Discovery` | Ergebnis jedes Versuchs, einen `Snapshot` zu speichern: Fehler erzeugt `source.discovery_failed` am Zugang (Fehlertext in `details`), Erfolg beendet ihn als behoben |
| `Evaluator.AckFinding` | quittiert einen aktiven Befund (`ErrNotFound` sonst); die erste Quittierung bleibt bestehen |
| Regelbefehle | `CreateRule`, `UpdateRule`, `DeleteRule` prüfen die Regel (auch: Geltungsbereich existiert, `match_driver` registriert), schreiben sie und bewerten alle Elemente im alten und neuen Geltungsbereich neu, damit auch überschriebene oder wieder geltende Regeln stimmen. IDs vergibt die API |
| Regelbefund | `message` z. B. `temp lte 80`; `details` mit dem Snapshot der Regel (Name, Fakt, Operator, Wert, `when`, Hysterese) und dem gemessenen Wert, damit der Befund nach dem Löschen der Regel lesbar bleibt; `element_path` aus `Graph.Path` |
| `Evaluator.Health` | Health von Elementen; bei Gerät und Gruppe über die verschiedenen Elemente in ihrem Geltungsbereich (`ScopeElements`, ohne Gerät- und Gruppen-Elemente), jedes genau einmal. Berechnet die übergebenen Elemente, dazu selbst alle mit geänderten Befunden und danach die Rollups ihrer Geräte und Gruppen; nach einer Änderung am Graph oder an den Regeln alles. Die Schleife übergibt die Elemente mit geänderten Fakten und alle Elemente einer Quelle, deren `Live` sich geändert hat. Liefert, was sich geändert hat; `Tick` schreibt es gesammelt (`SaveHealth`), gescheitert bleibt es offen bis zum nächsten Takt |
| `Evaluator.Healths` | Health und Rollup aller Elemente für die `View`; copy-on-write, nie verändert |
| `View` | unveränderlich; jede Verarbeitung erzeugt eine neue (`Version` steigt). Wer sie hält, sieht einen stimmigen Stand, auch wenn die Schleife weiterläuft |
| `Inventory.FactsByID` | übersetzt die Fakten einer Meldung von `external_id`s in Element-IDs; unbekannte `external_id`s kommen zurück, die Schleife loggt sie einmal pro Quelle |
| `Inventory.Path`, `Graph.Path` | `element_path` eines Elements für Befunde und Läufe (Label, sonst Name, vom Gerät bis zum Element, ohne Zugang, getrennt durch „ / “) |
| `Diff` | was die Discovery bzw. ein Befehl am Inventar geändert hat; `Missing`/`Returned` sind Wechsel von `present` |
| `Changed` | pro Element die Schlüssel der Fakten, die sich wirklich geändert haben, neu sind oder weggefallen sind |
| `Facts` | copy-on-write: Die Fakten eines Elements werden nie verändert, sondern ersetzt; `Snapshot` kopiert nur die äußere Map, die `View` teilt den Rest (C7) |
| `Liveness` | Phase jeder laufenden Quelle aus `Reporter` und Supervisor; `Live` entscheidet, ob bewertet wird; `Unreachable(now)` ist die Menge der Quellen, für die `source.unreachable` gilt (3 × Heartbeat seit dem letzten Lebenszeichen bzw. dem Start, bei Heartbeat 0 nur `backoff` und `stuck`) |
| `Evaluator.Tick` | regelmäßig: Haltezeiten (`hold_seconds`, `clear_hold_seconds`) laufen ab, Lebenszeichen fehlen, offene Schreibvorgänge werden wiederholt |
| `FindingChange.CauseSet` | neue Zuordnung von Ursache und Symptom (auch auf „keine Ursache“), siehe unten |

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
    L->>E: Alarms(sourceID, s.Alarms, replace, View)
    L->>E: Evaluate(Diff ∪ Changed, View)
    E->>S: RaiseFinding / EndFinding / SetFindingCause
    E-->>L: FindingChanges
    L->>E: Health(betroffene Elemente und ihre Geräte, Gruppen)
    E-->>L: geänderte Health
    L->>L: neue View veröffentlichen
    L->>B: ElementsChanged, FactsChanged, FindingRaised/Ended, HealthChanged
    Note over L,S: Health geht gesammelt im Takt an SaveHealth, nicht pro Meldung
```

- `ApplySnapshot` setzt für `ParentID ""` das Zugangs-Element der Quelle als Eltern und `Snapshot.GlobalRef` an den Zugang.
- **Ungültiges verwirft es mit `Warn`-Log:** Elemente, die `Driver.CheckElement` ablehnt, doppelte `external_id`s, Elemente ohne gültigen Weg zum Zugang (unbekannte oder verworfene Eltern, Zyklus) samt ihren Kindern; Links mit unbekanntem Typ, mit verworfenem oder unbekanntem Endpunkt und `represents` ohne `GlobalRef` am Ziel. Verworfene Elemente gelten als nicht gemeldet, verschwinden also.
- **Konflikt der `global_ref`:** Kommt eine `global_ref` im `Snapshot` doppelt vor oder gehört sie einem Element einer anderen Quelle, liefert `ApplySnapshot` `ErrGlobalRefConflict`, ohne den Store aufzurufen; die Schleife behandelt das wie einen Fehler von `ApplyDiscovery`.
- Erst schreibt der Store, dann ändert sich der Speicher; schlägt der Store fehl, bleibt der Speicher unverändert. Der Store bekommt die Elemente mit den Eltern vor den Kindern.
- **Struktur unverändert:** Der Core vergleicht den `Snapshot` im Speicher mit dem vorigen. Ohne Strukturänderung schreibt er nur `last_seen_at`, höchstens alle 5 min, und übernimmt nur die Fakten.
- `Replace` ersetzt alle Fakten der Quelle: Was im `Snapshot` fehlt, ist weg. `Update` dagegen ergänzt (`Merge`).
- Mit dem ersten übernommenen `Snapshot` nach `Connected` ist die Quelle `Live`; erst dann wird bewertet.
- Bewertet werden nur betroffene Elemente: geänderte Fakten, neue, verschwundene und zurückgekehrte Elemente, sowie Elemente, deren Links sich geändert haben (Ursache und Symptom).
- **Schlägt `ApplyDiscovery` fehl** (Datenbank, Konflikt der `global_ref`), merkt sich die Schleife den `Snapshot` pro Quelle. Folgende `Update`s mischt sie in den gemerkten Stand, ein neuerer `Snapshot` ersetzt ihn. Sie wiederholt mit steigender Wartezeit (höchstens 1 min) und erzeugt `source.discovery_failed` am Zugang (`system`, `error`, Fehlertext in `details`), der mit dem ersten Erfolg als behoben endet. Bis dahin ist die Quelle nicht `Live`. Das Gerät wird dafür nicht erneut abgefragt.

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
    L->>F: Merge(sourceID, u.Facts)
    F-->>L: Changed (nur echte Änderungen)
    opt Quelle Live und etwas hat sich geändert
        L->>E: Alarms(sourceID, u.Alarms, View)
        L->>E: Evaluate(Changed, View)
        E->>S: Befunde schreiben
        E-->>L: FindingChanges
        L->>L: Health, neue View
        L->>B: Ereignisse
    end

    loop Takt der Schleife (1 s)
        L->>F: Unreachable(now)
        F-->>L: Quellen nicht Live seit 3 × Heartbeat nach dem letzten Lebenszeichen
        L->>E: Tick(now, View)
        E->>S: Befunde schreiben, SaveHealth (gesammelt)
        E-->>L: FindingChanges (source.unreachable, abgelaufene Haltezeiten, Wiederholungen)
        L->>B: Ereignisse
    end
```

- Ein `Update` einer Quelle, die nicht `Live` ist, wird übernommen (Fakten „zuletzt gemeldet“), aber nicht bewertet; vor dem ersten `Snapshot` nach `Connected` wird es verworfen. Fakten an einer unbekannten `external_id` verwirft die Schleife mit `Warn`-Log einmal pro Quelle und `external_id` bis zum nächsten `Snapshot`.
- Ist die Quelle nicht `Live`, sind ihre Elemente außer dem Zugang `unknown`; ihre aktiven Befunde bleiben unverändert.

**Zusagen bei Last.** Läuft die Schleife hinterher, fasst der Reporter aufeinanderfolgende `Update`s einer Quelle zusammen. Wie er das tut, ist Sache der Implementierung; zugesagt ist:

- **Es gilt immer der neueste Wert** pro Element und Fakt, bei Alarmen die neueste Liste pro Element.
- **Zwischenwerte können entfallen.** Ein kurzer Wechsel (`state.signal_present` false und gleich wieder true) ist für die Bewertung dann womöglich nicht sichtbar. Regeln mit `hold_seconds` sollen so kurze Wechsel ohnehin nicht melden.
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
    L->>I: DeleteDevice(actor, id)
    I->>S: DeleteDevice(actor, id)
    S-->>I: ok bzw. InUseError
    I-->>L: Diff (Deleted, Missing)
    L-)L: Supervisor beauftragen: Quellen des Geräts stoppen
    L->>E: Befunde und Health der betroffenen Elemente
    L->>L: neue View
    L->>B: Ereignisse (u. a. FindingEnded mit deleted)
    L-->>-A: nil bzw. Fehler
```

- Jeder Befehl ist eine Transaktion im Store **und** ein Schritt der Schleife. Die API sieht nach `Do` die neue `View`.
- **Befehle sind Structs nur im Core** (`internal/core`), denn nur dort überqueren sie die Grenze zwischen Goroutinen (`Do` → Schleife). Die Schleife übersetzt sie per Typ-Switch in Methoden der Komponenten (`Inventory.DeleteDevice` …); die Komponenten kennen keine Befehlstypen.
- **Neue IDs vergibt die API** (UUIDv7) und gibt sie im Befehl mit, denn `Do` liefert nur den Fehler. Nur die ID des Zugangs-Elements einer neuen Quelle vergibt das Inventar.
- Das Inventar prüft Vorbedingungen gegen den Speicher, bevor es den Store aufruft: `ErrNotFound` (unbekannt oder falsche Art), `ErrUnknownDriver`, `ErrNotAGroup`, `ErrMissingID`. Beim Löschen liefert es alle wegfallenden Elemente in `Diff.Deleted` und entfernt ihre Links aus dem Speicher wie die Datenbank per CASCADE.
- Befehle, die Quellen betreffen (Gerät löschen, Quelle anlegen, ändern oder löschen, Secret ersetzen), beauftragen den Supervisor mit Start bzw. Stopp der Quelle (`sdk.md`, Stoppen). Die Schleife wartet nicht darauf; Start, Stopp, Fehler, Panics und hängende Quellen meldet der Supervisor per `Post` zurück.
- `ReplaceSecret` schreibt über `secret.Service` und startet danach alle Quellen neu, die das Secret verwenden (aus der `View`). Anlegen und Löschen von Secrets betreffen keine laufende Quelle und gehen direkt an `secret.Service`.

Prüfungen im Core, zusätzlich zur Datenbank:

| Befehl | Prüfung |
|---|---|
| `CreateSource`, `UpdateSource` | Gerät existiert; Treiber registriert; Neustart beim Supervisor |
| `DeleteDevice`, `DeleteSource`, `DeleteGroup` | kein Preset-Schritt zielt darauf (bei Gerät und Quelle: auf eines ihrer Elemente); aktive Befunde werden vorher zurückgezogen (`deleted`) |
| `AddGroupMember`, `UpdateGroupMember` | Ziel hat `kind = group`; Zyklusprüfung im Store |
| `CreateRule`, `UpdateRule` | Operator und Werttyp gegen den Fakt-Katalog |

## Abonnenten

Abonnenten (Verteiler der API für alle Browserfenster, Weiterleitung, Aktions-Executor, Preset-Runner) bekommen Ereignisse über einen gepufferten Kanal. Die Schleife wartet nie auf einen Abonnenten und verwirft nie still ein Ereignis: Ist ein Puffer voll, **hängt sie den Abonnenten ab** und schließt seinen Kanal. Er merkt das, meldet sich neu an und liest die vollständige `View`.

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
- **UI-Push:** Die API hält **ein** Abo und verteilt an die Browser-Ströme (`api.md`); ein abgehängter Verteiler schickt allen Strömen `resync`.
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
    Active --> Clearing: erfüllt (auch durch unerfülltes when), bei Hysterese erst ab clear_value
    Clearing --> Active: wieder verletzt
    Clearing --> Ok: clear_hold_seconds abgelaufen (EndFinding resolved)
    Active --> Ok: erfüllt, clear_hold_seconds = 0 (EndFinding resolved)
    Active --> Ended: Regel gilt nicht mehr (EndFinding withdrawn)
    Clearing --> Ended: Regel gilt nicht mehr (EndFinding withdrawn)
    Active --> Active: Quelle nicht Live (keine Bewertung, Health unknown)
    Ended --> [*]
```

- **Regel gilt** am Element: Geltungsbereich und Filter passen, Element überwacht, `fact` (bei `when` auch `when_fact`) vorhanden, und sie gehört zur gewinnenden Stufe für Fakt und Richtung (`docs/ARCHITECTURE.md` Abschnitt 8). Gründe für das Zurückziehen: `rule_deleted`, `rule_disabled`, `rule_changed`, `unmonitored`, `element_missing`, `fact_missing`, `source_disabled`, `deleted`.
- `Pending` und `Clearing` existieren nur im Speicher. Nach einem Neustart beginnen Haltezeiten neu; ein aktiver Befund bleibt so lange aktiv.
- Befunde aus Gerätealarmen entstehen ohne Haltezeit, sobald der Alarm in der Liste steht, und enden (behoben), sobald er fehlt.

## Ursache und Symptom

Die Zuordnung ist eine Ableitung über den Link-Graphen der `View`, kein Zustand vom Entstehen.

- **Auslöser:** Ein Befund entsteht oder endet, der Graph ändert sich (Links, Eltern), oder ein früheres `SetFindingCause` ist gescheitert. Der Evaluator leitet dann die Zuordnung aller aktiven Befunde neu ab (Breitensuche flussaufwärts: `feeds` A→B wirkt auf B, `uses` A→B auf A, `parent` auf die Kinder) und schreibt nur, was sich geändert hat. Das ergibt dasselbe wie eine Neuzuordnung nur flussabwärts und bleibt bei wenigen Ebenen billig.
- Ein neuer Befund wird mit seiner Zuordnung zum Zeitpunkt des Entstehens geschrieben (`caused_by_id` in `RaiseFinding`).
- `FindingChange{CauseSet, CausedBy}`: Befund mit neuer Zuordnung, `CausedBy` = Wurzel bzw. `uuid.Nil` für „selbst Ursache“. Scheitert das Schreiben, bleibt der Speicher unverändert und `Tick` versucht es erneut.
- **Regel:** Ein aktiver Befund ist Symptom, wenn flussaufwärts ein aktiver Befund mit `warning` oder `error` liegt; `caused_by_id` zeigt auf die Wurzel. Bei mehreren Wurzeln gewinnt die schwerere, dann die ältere. Kreise sind abgefangen, dort ist der älteste Befund die Wurzel.
- Jede Änderung wird als `FindingChange.CauseSet` geschrieben (`SetFindingCause`) und als `FindingCauseChanged` verteilt; die Weiterleitung verwendet es nicht.

## Entscheidungen

| # | Frage | Entscheidung |
|---|---|---|
| C1 | Nebenläufigkeit | eine Schleife besitzt den gesamten Zustand im Speicher; `inventory`, `state`, `evaluation` sind Logik ohne eigene Goroutinen; Dienste (Supervisor, Executor …) laufen nebenher und sprechen nur über Nachrichten, Ereignisse und die `View` |
| C2 | Lesen | unveränderliche `View` nach jeder Verarbeitung, ohne Locks lesbar |
| C3 | Schreiben in die Datenbank | Inventar und Befunde sofort und synchron in der Schleife, mit Timeout; Befunde werden bei Fehlern im Takt wiederholt; Health gesammelt im Takt; `last_seen_at` ohne Strukturänderung höchstens alle 5 min |
| C4 | Rückstau bei Last | Reporter fasst `Update`s zusammen (neuester Wert gilt, Zwischenwerte können entfallen, Reihenfolge bleibt); das Verfahren ist Sache der Implementierung |
| C5 | Takt der Schleife | 1 s für Lebenszeichen, Haltezeiten und `SaveHealth`; InfluxDB über einen eigenen Schreiber aus der `View` |
| C6 | Langsame Abonnenten | werden abgehängt, melden sich neu an und lesen die `View` (über `Version` lückenlos); Weiterleitung mit großem Puffer |
| C7 | Größe der `View` | strukturell geteilt: unveränderte Teile werden übernommen, nicht kopiert |
| C8 | Ursache und Symptom | bei jeder Befund- und Link-Änderung neu abgeleitet; `caused_by_id` zeigt auf die Wurzel und wird persistiert |
| C9 | Gescheiterter `Snapshot` | gemerkt und mit steigender Wartezeit wiederholt, ohne das Gerät; `source.discovery_failed` am Zugang |
| C10 | Datenbank nicht erreichbar | Monika läuft weiter, Zustandswechsel warten; ein Wächter pingt alle 5 s, `View.DB` ist höchstens 5 s alt; sichtbar über Banner und `GET /healthz` |
| C11 | Nachrichten der Dienste | `Post` ohne Warten; `Do` nur für Befehle mit Fehlerrückgabe |
