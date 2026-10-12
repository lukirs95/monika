# deploy

Lokale Umgebung und Deployment (z. B. PostgreSQL und InfluxDB für die Entwicklung).

- `compose.yaml`: lokale Entwicklung (PostgreSQL), über `make db-up` und `make db-down`.
- `compose.prod.yaml`, `Caddyfile`: Produktivbetrieb (Monika, PostgreSQL, InfluxDB, Caddy) mit `deploy/.env` aus `.env.example`.
- `Dockerfile`: Image aus dem lokal gebauten Binary (`make image`).
- `roles.sql`: Rollen `monika_reporting` und `monika_backup`, einmal vom Betreiber eingespielt.

Ablauf und Variablen: `docs/OPERATIONS.md`.
