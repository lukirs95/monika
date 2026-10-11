# deploy

Lokale Umgebung und Deployment (z. B. PostgreSQL und InfluxDB für die Entwicklung).

- `compose.yaml`: lokale Entwicklung (PostgreSQL), über `make db-up` und `make db-down`.
- Für den Produktivbetrieb (Monika, PostgreSQL, InfluxDB, Caddy) siehe `docs/OPERATIONS.md`; die Dateien entstehen mit dem Code.
