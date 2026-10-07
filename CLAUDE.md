# CLAUDE.md

Arbeitsregeln für Claude in diesem Repository.

## Projektstand

- Verbindlich sind `docs/ARCHITECTURE.md` (Verhalten, Architekturentscheidungen) und `docs/schema/monika.dbml` (Datenbankschema).
- Die Go-Architektur entsteht schrittweise; größere Entscheidungen werden vorher abgestimmt, nicht stillschweigend getroffen. Bereits festgelegt ist das Muster für die Persistenz (siehe unten).
- Abhängigkeiten zeigen nur nach innen: `drivers/*` importiert nur `sdk/` (und die jeweilige Gerätelibrary), nie `internal/`.

## Go

- Das gesamte System wird in modernem, idiomatischem Go geschrieben (Version laut `go.mod`). Aktuelle Sprach- und Stdlib-Mittel nutzen, statt veraltete Muster nachzubauen.
- Typische Go-Muster einhalten, u. a.:
  - kleine Interfaces, definiert beim Verwender; konkrete Typen zurückgeben
  - `context.Context` als erster Parameter bei allem, was blockiert oder I/O macht
  - Fehler mit `%w` wrappen und mit `errors.Is`/`errors.As` prüfen; keine `panic` für normale Fehler
  - keine globalen Zustände außer der Treiber-Registry (`init()`-Registrierung, Telegraf-Modell)
  - Abhängigkeiten explizit über Konstruktoren verdrahten (in `cmd/monika`), kein DI-Framework
  - Goroutinen haben einen klaren Besitzer und ein klares Ende; kein Leak
  - Paketnamen kurz, klein, ohne `util`/`common`/`helpers`
  - strukturiertes Logging mit `log/slog`
  - table-driven Tests
- Code ist mit `gofmt`/`goimports` formatiert und besteht `go vet` sowie den Linter.

## Persistenz

Vorbild ist `internal/core/inventory` mit `internal/store/postgres`.

- **Schema:** `docs/schema/monika.dbml` ist die Quelle. `make db-generate` erzeugt daraus `migrations/00001_init.sql`; nur was DBML nicht ausdrücken kann (z. B. Teilindizes), steht in handgeschriebenen Migrationen.
- **Queries:** SQL in `internal/store/postgres/queries/`, `make db-gen-go` erzeugt mit sqlc das Paket `internal/store/postgres/db/`. Generierten Code nie von Hand ändern.
- **Interfaces beim Verwender:** Jedes Fachpaket definiert sein eigenes `Store`-Interface mit genau den Methoden, die es braucht (z. B. `inventory.Store`).
- **Domänentypen im Fachpaket:** Typen und fachliche Fehler (`ErrNotFound`, `ErrMemberCycle` …) liegen im Fachpaket, nicht in einem gemeinsamen `model`-Paket. Leere Werte bedeuten „nicht gesetzt“; Pointer nur, wo der leere Wert ein gültiger Wert ist.
- **Fachliche Operationen:** Jede Store-Methode ist eine in sich atomare fachliche Operation (z. B. `ApplyDiscovery`, `DeleteDevice`). Transaktionen öffnet und schließt nur der Store; Fachpakete sehen keine Transaktionen.
- **Eine Implementierung:** `postgres.Store` erfüllt alle Store-Interfaces, abgesichert per `var _ paket.Store = (*Store)(nil)`. Er übersetzt zwischen Domänentypen und sqlc-Typen (Helfer in `convert.go`).
- **sqlc-Typen verlassen `store/postgres` nie.** Fachpakete importieren weder `postgres` noch `db`; verdrahtet wird in `cmd/monika`.
- **Tests:** Der Store wird mit Integrationstests gegen PostgreSQL getestet, Fachpakete mit Unit-Tests gegen eine In-Memory-Attrappe ihres Interfaces.

## Build und Tests

- `make` ist das einzige Build- und Testsystem. Alles, was regelmäßig ausgeführt wird (Build, Unit-Tests, Integrationstests, Lint, Migrationen, lokale Umgebung), bekommt ein Make-Target. Keine losen Shell-Skripte als Einstiegspunkt.
- Go ist lokal vorausgesetzt; Go-Code wird nie in Docker gebaut oder getestet. Docker stellt nur die Dienste bereit (PostgreSQL, InfluxDB usw.). Keine Abhängigkeit von lokal installierten Diensten.
- Integrationstests starten ihre Dienste selbst per testcontainers-go (für PostgreSQL: `internal/store/postgres/pgtest`, eine frische Datenbank pro Test). Sie tragen das Build-Tag `integration` und laufen mit `make test-integration`.
- Unit-Tests laufen ohne Docker und ohne Netzwerk und bleiben schnell (`make test`).

## Änderungen und Git

- Dateien **niemals komplett neu schreiben**. Bestehende Dateien gezielt mit kleinen Edits ändern. Ein Neuschreiben ganzer Dateien ist nur nach ausdrücklicher Zustimmung erlaubt.
- Änderungen klein und reviewbar halten. Ein Commit soll keine hunderte Zeilen Code enthalten (Richtwert: deutlich unter 500 Zeilen). Größere Vorhaben in mehrere sinnvolle Schritte aufteilen.
- Nicht jede Änderung braucht einen eigenen Commit. Zusammengehörige Kleinigkeiten gemeinsam committen; nur committen, wenn ein in sich abgeschlossener, funktionierender Schritt erreicht ist oder der Nutzer es verlangt.
