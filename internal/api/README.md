# internal/api

HTTP-API für die Monika-UI und externe Systeme (Companion, Stream Deck, Node-RED über API-Tokens) sowie Live-Push von Zustand, Befunden und Läufen an die UI.

Jede Anfrage läuft durch den Guard (`guard.go`): Operation nach der Spezifikation, Anmeldung über `internal/auth`, `read_only`, Origin, Prüfung gegen die Spezifikation. Typen, Router und Server-Interface erzeugt `make api-gen` in `oapi/` (nicht von Hand ändern); Fehler übersetzt `problem.go` in Problem Details.

Siehe `docs/ARCHITECTURE.md`, Abschnitt 3 und 12.
