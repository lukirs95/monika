# internal/store/postgres

Persistenz in PostgreSQL. Schema: `schema/monika.dbml`, Migrationen: `migrations/`.

IDs als UUIDv7, in Go erzeugt (ganze Discovery-Bäume in einem Batch).

Abfragen als SQL in `queries/`; `make db-gen-go` erzeugt daraus mit sqlc das Paket `db/` (nicht von Hand ändern).

Siehe `docs/ARCHITECTURE.md`, Abschnitt 14 und 15.
