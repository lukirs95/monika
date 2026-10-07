# Arbeitsstand

Zwischenstand der Implementierung, damit die Arbeit in einer neuen Sitzung nahtlos weitergeht.
Zu Beginn einer Sitzung lesen, am Ende aktualisieren.

Stand: 2026-10-07 (letzter Commit `46fbb8f`)

## Erledigt

- **Projektregeln:** `CLAUDE.md` (Go, make, Docker, kleine Commits, Persistenz-Muster), `.gitignore`.
- **Schema:** Alle Constraints, Ausdrucksindizes und `ON DELETE`-Regeln stehen direkt in `docs/schema/monika.dbml`.
  - `make db-generate` erzeugt `migrations/00001_init.sql` (wird bis zum ersten produktiven Einsatz neu generiert, danach eingefroren).
  - `migrations/00002_finding_active_uq.sql` ist handgeschrieben (Teilindex, den DBML nicht kann).
  - `make db-validate` spielt alle Migrationen in eine frische PostgreSQL 18 ein.
- **Werkzeuge:** goose (Migrationen, per `embed` im Binary, `postgres.Migrate`), sqlc + pgx/v5 (`make db-gen-go`), testcontainers-go (`pgtest`, frische DB pro Test). sqlc und dbml2sql laufen als fertige Docker-Images, Go lokal.
- **Make-Targets:** `build`, `test`, `test-integration`, `vet`, `db-generate`, `db-gen-go`, `db-validate`, `db-up`, `db-down`, `db-migrate`.
- **Queries** (`internal/store/postgres/queries/`): `secret.sql`, `device.sql`, `element.sql`, `element_link.sql`.
- **Inventar nach dem Store-Muster:**
  - `internal/core/inventory`: Domänentypen, `inventory.Store`, Fehler (`ErrNotFound`, `ErrMemberCycle`, `DeviceInUseError`).
  - `postgres.Store` implementiert `inventory.Store` (`Load`, `ApplyDiscovery`, Geräte, Quellen, Gruppen).
- **Entscheidungen dieser Sitzung** (stehen auch in `docs/ARCHITECTURE.md`):
  - Geräte werden **weich gelöscht** (`device.deleted_at`, Quellen aus, `monitored = false`, raus aus Gruppen); abgelehnt, solange ein Preset-Schritt auf das Gerät zielt.
  - `FinishDiscovery`: ein Statement pro Discovery-Lauf setzt `last_seen_at` und `present`.
  - Gruppen-Zyklenprüfung unter Advisory Lock (`LockMemberLinks`).

## Nächste Schritte

1. **`secret` auf das Store-Muster umstellen:** `secret.Store`-Interface und Typen in `internal/secret`, Methoden in `postgres.Store`. Fehlercode `23001` (`restrict_violation`, Secret noch in Verwendung) auf einen fachlichen Fehler abbilden. Danach Verschlüsselung (AES-256-GCM), Master-Key aus der Umgebung, Rotation, Startprüfung.
2. **Zustand:** Queries und Store für `element_state` (nur bei Änderung schreiben) und `device_source_state`.
3. **Bewertung:** Queries und Store für Regeln und Befunde (aktive laden, anlegen, aufheben, quittieren).
4. **Presets:** Queries und Store; beim Anlegen von Schritten gelöschte Geräte ablehnen und die Geräte-Zeile mitsperren (siehe Lücken).
5. **Bootstrap in `cmd/monika`:** Konfiguration aus Umgebung/`.env`, `postgres.Open`, `postgres.Migrate`, Verdrahtung.
6. **Treiber-SDK (`sdk/`):** Interfaces sind noch offen; Voraussetzung für den Discovery-Sync im Core (`sdk` → `inventory`).

## Bekannte Lücken

- `CreateDeviceSource` und `CreatePlannedElement` nehmen auch gelöschte Geräte an.
- Preset-Schritte: Wer einen Schritt anlegt, muss prüfen, ob das Ziel-Gerät gelöscht ist, und die Geräte-Zeile sperren (sonst Race mit `DeleteDevice`).
- `CreateMemberLink` prüft nicht, dass das Ziel der Mitgliedschaft wirklich eine Gruppe (`kind = group`) ist.
- Kein Linter festgelegt: `CLAUDE.md` verlangt einen, es gibt aber noch kein `make lint` (Vorschlag: golangci-lint).
- testcontainers schreibt viele Logzeilen in die Testausgabe.

## Offene Fragen an den User

- Sprachkonvention: Kommentare bisher Deutsch, Fehlermeldungen Englisch. Noch nicht bestätigt; bei Zustimmung in `CLAUDE.md` aufnehmen.

## Hinweise

- gopls meldet in Dateien mit Build-Tag `integration` fälschlich „go mod tidy“. Abhilfe: `"buildFlags": ["-tags=integration"]` in den gopls-Einstellungen.
- PostgreSQL 18 meldet Verstöße gegen `ON DELETE RESTRICT` mit `23001`, nicht `23503`.
- sqlc hängt Kommentare am Dateianfang an die erste Query; Query-Dateien daher ohne Datei-Kommentar.
