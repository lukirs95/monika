# migrations

SQL-Migrationen für PostgreSQL ≥ 18 im Format von [goose](https://github.com/pressly/goose).

| Datei | Herkunft |
|---|---|
| `00001_init.sql` | **generiert** aus `docs/schema/monika.dbml` mit `make db-generate` – nicht von Hand ändern |
| `00002_finding_active_uq.sql` | handgeschrieben: Teilindex, den DBML nicht ausdrücken kann |

Prüfen: `make db-validate` spielt alle Migrationen in eine frische PostgreSQL in Docker ein.

Solange Monika nirgends produktiv läuft, wird `00001_init.sql` bei jeder Schemaänderung neu generiert. Danach wird die Init-Migration eingefroren; Änderungen an der DBML bekommen dann zusätzlich eine eigene, handgeschriebene Migration.

Siehe `docs/ARCHITECTURE.md`, Abschnitt 15.
