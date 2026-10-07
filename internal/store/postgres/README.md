# internal/store/postgres

Persistenz in PostgreSQL. Schema: `docs/schema/monika.dbml`, Migrationen: `migrations/`.

IDs als UUIDv7, in Go erzeugt (ganze Discovery-Bäume in einem Batch).

Siehe `docs/ARCHITECTURE.md`, Abschnitt 14 und 15.
