# Arbeitsstand

Zwischenstand der Implementierung, damit die Arbeit in einer neuen Sitzung nahtlos weitergeht.
Zu Beginn einer Sitzung lesen, am Ende aktualisieren.

Stand: 2026-10-08

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
- **Schema und Store an die Durchsicht angepasst** (2026-10-08): Tabelle `device` entfernt, Identität pro Quelle, `element.deleted_at`, `external_id` pro Quelle (Teilindex mit `NULLS NOT DISTINCT`, Migration `00003`), Zugangs-Element mit jeder Quelle, `FinishDiscovery` mit Zugangs-Element, `DeleteDevice` und `DeleteGroup` vollständig (Befunde zurückziehen, Konfiguration mit Geltungsbereich löschen, Mitgliedschaften entfernen, Presets blockieren).
- **Audit des Inventars** (2026-10-08): Store-Methoden, die der User auslöst, bekommen den Akteur als expliziten Parameter (`uuid.Nil` = Monika). Änderung und Audit-Eintrag laufen im selben SQL-Statement. Discovery protokolliert neue Elemente, Wechsel von `present` und Links mit `actor_id = NULL`; Löschen schreibt einen Eintrag pro Gerät/Gruppe und je einen pro Mitgelöschtem.
- **Secret-Store** (2026-10-08): `secret.Store` in `postgres.Store` mit Audit ohne Werte; `secret.Key` (AES-256-GCM, `key_id` = Fingerabdruck, nie geloggt); `secret.Service` mit `VerifyAndRotate` beim Start. Erste Unit-Tests mit In-Memory-Attrappe (`internal/secret`).
- **Entscheidungen vom 2026-10-07:** Geräte weich löschen; `FinishDiscovery` (ein Statement pro Lauf); Gruppen-Zyklenprüfung unter Advisory Lock.

## Architektur-Durchsicht (2026-10-08)

Alle Entscheidungen stehen in `docs/ARCHITECTURE.md` und `docs/schema/monika.dbml`. Kurzfassung:

- **Zugangs-Element** (`kind = access`, ohne `external_id`) pro Quelle, vom Core beim Anlegen der Quelle erzeugt; trägt Fakten und Aktionen für das ganze Gerät und `source.unreachable`.
- **Tabelle `device` entfällt:** Gerät = Element mit `kind = device`; Identität pro Quelle in `device_source`.
- **Tabelle `device_source_state` entfällt:** Verbindung über `source.unreachable`, Lebenszeichen im Speicher.
- **`element_state`** nur noch `health` (nullable), `rollup`, `updated_at`; Fakten nur im Speicher und im festen Takt nach InfluxDB.
- **`external_id` eindeutig pro Quelle** (`element_source_external_uq`, `NULLS NOT DISTINCT`, handgeschrieben).
- **Weiches Löschen** für Geräte und Gruppen über `element.deleted_at`; mitgelöscht werden Mitgliedschaften und Konfiguration mit diesem Geltungsbereich; Löschen/Anlegen erfordert `inventory_edit` global.
- **Befunde enden** als behoben (`finding.cleared`) oder zurückgezogen (`finding.withdrawn`, `details.cleared_reason`); `data.stale` entfällt.
- **Health:** neutral bei nicht überwacht/verschwunden, `unknown` wenn die Quelle nicht liefert, Zusammenfassen `error` > `warning` > `unknown` > `ok`, leere Menge `unknown`.
- `monitored` vererbt sich entlang `parent_id`; `present` ist reservierter Fakt; Zähler über `<fakt>.rate`.
- **Sende-Ziele** (verallgemeinert aus dem HTTP-Treiber): nur `trigger`, kein `set`, Fakten nur über Aufrufe.
- Neue Spalten `element.critical_actions`, `preset_step.driver`; Regel-Präzedenz und Geltungsbereich der Rechte präzisiert.

## Nächste Schritte

1. **Zustand:** Queries und Store für `element_state` (Health und Rollup, nur bei Änderung).
2. **Bewertung:** Queries und Store für Regeln und Befunde (aktive laden, anlegen, aufheben, quittieren).
3. **Presets:** Queries und Store; beim Anlegen von Schritten gelöschte Geräte/Gruppen ablehnen und die Zeile mitsperren; `preset_step.driver`.
4. **Bootstrap in `cmd/monika`:** Konfiguration aus Umgebung/`.env` (`MONIKA_MASTER_KEY` per `secret.ParseKey`), `postgres.Open`, `postgres.Migrate`, `secret.Service.VerifyAndRotate` (bei Fehler kein Start), Verdrahtung.
5. **Treiber-SDK (`sdk/`):** Interfaces sind noch offen; Voraussetzung für den Discovery-Sync im Core (`sdk` → `inventory`). Vertrag: vollständiger Stand, Lebenszeichen, Zähler-Helfer, reservierte Werte.

## Bekannte Lücken

- `CreatePlannedElement` nimmt auch Elemente gelöschter Geräte an und prüft keine reservierten `kind`-Werte (Prüfung gehört in den Core).
- Der Core muss beim Löschen die zurückgezogenen Befunde selbst als `finding.withdrawn` melden; der Store gibt sie nicht zurück.
- Preset-Schritte: Wer einen Schritt anlegt, muss prüfen, ob das Ziel gelöscht ist, und die Zeile sperren (sonst Race mit dem Löschen).
- `CreateMemberLink` prüft nicht, dass das Ziel der Mitgliedschaft wirklich eine Gruppe (`kind = group`) ist.
- Kein Linter festgelegt: `CLAUDE.md` verlangt einen, es gibt aber noch kein `make lint` (Vorschlag: golangci-lint).
- testcontainers schreibt viele Logzeilen in die Testausgabe.

## Offene Fragen an den User

- Sprachkonvention: Kommentare bisher Deutsch, Fehlermeldungen Englisch. Noch nicht bestätigt; bei Zustimmung in `CLAUDE.md` aufnehmen.

## Hinweise

- gopls meldet in Dateien mit Build-Tag `integration` fälschlich „go mod tidy“. Abhilfe: `"buildFlags": ["-tags=integration"]` in den gopls-Einstellungen.
- PostgreSQL 18 meldet Verstöße gegen `ON DELETE RESTRICT` mit `23001`, nicht `23503`.
- sqlc hängt Kommentare am Dateianfang an die erste Query; Query-Dateien daher ohne Datei-Kommentar.
