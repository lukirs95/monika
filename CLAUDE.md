# CLAUDE.md

Arbeitsregeln für Claude in diesem Repository.

## Projektstand

- Verbindlich sind `docs/ARCHITECTURE.md` (Verhalten, Architekturentscheidungen) und `docs/schema/monika.dbml` (Datenbankschema).
- Eine Go-spezifische Architektur (Pakete, Interfaces, Typen) gibt es noch nicht. Sie entsteht schrittweise; größere Entscheidungen werden vorher abgestimmt, nicht stillschweigend getroffen.
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

## Build und Tests

- `make` ist das einzige Build- und Testsystem. Alles, was regelmäßig ausgeführt wird (Build, Unit-Tests, Integrationstests, Lint, Migrationen, lokale Umgebung), bekommt ein Make-Target. Keine losen Shell-Skripte als Einstiegspunkt.
- Alle Integrationstests laufen mit Docker (PostgreSQL, InfluxDB usw. in Containern). Keine Abhängigkeit von lokal installierten Diensten.
- Unit-Tests laufen ohne Docker und ohne Netzwerk und bleiben schnell. Integrationstests sind davon getrennt (z. B. per Build-Tag `integration`) und haben ein eigenes Make-Target.

## Änderungen und Git

- Dateien **niemals komplett neu schreiben**. Bestehende Dateien gezielt mit kleinen Edits ändern. Ein Neuschreiben ganzer Dateien ist nur nach ausdrücklicher Zustimmung erlaubt.
- Änderungen klein und reviewbar halten. Ein Commit soll keine hunderte Zeilen Code enthalten (Richtwert: deutlich unter 500 Zeilen). Größere Vorhaben in mehrere sinnvolle Schritte aufteilen.
- Nicht jede Änderung braucht einen eigenen Commit. Zusammengehörige Kleinigkeiten gemeinsam committen; nur committen, wenn ein in sich abgeschlossener, funktionierender Schritt erreicht ist oder der Nutzer es verlangt.
