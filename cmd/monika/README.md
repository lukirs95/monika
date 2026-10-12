# cmd/monika

Einstiegspunkt des Monolithen.

- `main.go`: Flags (`-version`, `-healthcheck`), `run(…) int`
- `serve.go`: Datenbank, Einzelinstanz-Sperre, Migrationen, Secrets prüfen und rotieren, initialer Admin, Komponenten verdrahten, Dienste in der Reihenfolge aus `flows.md` starten, in sechs Phasen herunterfahren
- Treiber kommen über `drivers/all`

Vertrag: `docs/contracts/bootstrap.md` und `flows.md` (Start, Herunterfahren).
