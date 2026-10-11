# Arbeitsstand

Zwischenstand der Implementierung, damit die Arbeit in einer neuen Sitzung nahtlos weitergeht.
Zu Beginn einer Sitzung lesen, am Ende aktualisieren.

Stand: 2026-10-11

## Erledigt

- **Projektregeln:** `CLAUDE.md` (Go, make, Docker, kleine Commits, Persistenz-Muster), `.gitignore`.
- **Schema:** Alle Constraints, Ausdrucksindizes und `ON DELETE`-Regeln stehen direkt in `schema/monika.dbml`.
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
- **Zustand** (2026-10-08): `evaluation.Store` mit `LoadHealth`/`SaveHealth` für `element_state`; unveränderte Werte lassen die Zeile unberührt.
- **Bewertung** (2026-10-09): Regeln und Befunde in `evaluation.Store`; Regeln mit Audit, Löschen/Deaktivieren zieht aktive Befunde zurück; Befunde mit `ErrAlreadyActive`, Ende mit Grund, Ursache, Quittierung.
- **Presets** (2026-10-09): `preset.Store`; Ziele und ihre Geräte werden beim Speichern gesperrt und auf Löschung geprüft (Race mit `DeleteDevice`/`DeleteGroup` per Test abgesichert); Schritte behalten beim Ändern ihre IDs; Audit mit Schritten am Ende der Transaktion. **Damit ist der Store vorerst vollständig.**
- **Verträge** (2026-10-09): `docs/contracts/` – SDK, Core, Abläufe, Dienste, Anmeldung, API (OpenAPI 3.0.3); `make docs-check`, `make api-check`.
- **Schema und Stores aus den Verträgen** (2026-10-09): Tabellen `user_session`, `user_totp`, `user_recovery_code`, `api_token.read_only`; `action.Store`, `forward.Store`, `auth.Store` (Benutzer, Rollen, Rechte, Sitzungen, Tokens, TOTP); `secret.Vault` für die gemeinsame Rotation von Secrets und TOTP.
- **Entscheidungen vom 2026-10-07:** ~~Geräte weich löschen~~ (überholt durch das Audit); `FinishDiscovery` (ein Statement pro Lauf); Gruppen-Zyklenprüfung unter Advisory Lock.
- **Architektur-Audit** (2026-10-10): alle Befunde mit dem User durchgegangen und in ARCHITECTURE, DBML und allen Verträgen nachgezogen (siehe unten). Nur Dokumente; der Code folgt in Schritt 2.

## Architektur-Durchsicht (2026-10-08)

> **Überholt.** Dieser Abschnitt beschreibt den Stand vom 2026-10-08. Mehrere Punkte sind durch das Architektur-Audit (2026-10-10) und die Vervollständigung (2026-10-11) ersetzt, u. a. weiches Löschen, Health bei leerer Menge und `preset_step.driver` als Pflicht. Verbindlich sind `docs/ARCHITECTURE.md` und die Verträge.

Alle Entscheidungen stehen in `docs/ARCHITECTURE.md` und `schema/monika.dbml`. Kurzfassung:

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

## Architektur-Audit (2026-10-10)

Ein externes Audit hat Widersprüche und Lücken gefunden; alle Punkte sind entschieden und dokumentiert. Kurzfassung:

- **Health:** Element ohne geltende Regel ist *ungeprüft* (keine Health, Rollup `unchecked`); Zugang mit Heartbeat > 0 gilt als geprüft. Deaktivierte Quelle = nicht überwacht. Leere Menge bei Gerät/Gruppe = keine Health; `unknown` nur aus einer Quelle, die nicht Live ist.
- **Live:** Elemente sind `unknown`, sobald ihre Quelle nicht Live ist; Fakten dann nicht bewertet, nicht nach InfluxDB. `source.unreachable` nach 3 × Heartbeat seit dem letzten Lebenszeichen, für jeden Anlass gleich.
- **SDK:** `Source.Heartbeat()` statt `Driver.Heartbeat`; Fakt-Katalog `Driver.Facts` mit festem Typ; `Snapshot.GlobalRef` am Zugang; `global_ref` mit Namensraum; `ParentID ""` = Zugang (echte Wurzel); `Setting.WriteOnly`; keine Einstellungen für die eigene Adresse und keine geheimen Werte; Treibername = Paketname (`ravio`).
- **Bewertung:** Regel gilt, wenn Fakt (und `when_fact`) existieren; unerfülltes `when` = erfüllt; Präzedenz pro Element, Fakt und Richtung; neue Gründe `fact_missing`, `deleted` (statt `device_deleted`); `clear_hold_seconds`; Ursache/Symptom bei jeder Änderung neu abgeleitet (Wurzel). Ein Geltungsbereich für alles (`View.InScope`), Regeln vererben an Kinder.
- **Löschen:** keine Vorab-Elemente mehr; Geräte, Gruppen und Quellen werden **hart** gelöscht (CASCADE); Historie ohne Fremdschlüssel mit `element_path`. `deleted_at` entfällt.
- **Rechte:** neues globales Recht `source_edit` für Quellen und Secret-Zuweisung.
- **Aktionen:** `device_level` + `driver` als Filter; `set` nur an Elemente mit allen Schlüsseln; Preset-Ziele beim Start eingefroren; Abbrechen über Runner bzw. Executor, abgebrochene übergeordnete Läufe `cancelled`; Stopp nach F8 (`sent` wartet ≤ 5 s), harter Abbruch nach F2 beim nächsten Start.
- **Core:** `Post`, Nachrichten des Supervisors, `View` mit Links, Runs, Rules, `DB`; Ereignisse `RunChanged`, `FindingCauseChanged`; Befehle `DeleteSource`, `UpdateGroupMember`, `ReplaceSecret`; gescheiterte Snapshots gemerkt und wiederholt (`source.discovery_failed`); DB-Timeouts, `/healthz`.
- **API:** Verteiler mit einem Abo, sofort schreiben und bei Rückstau zusammenfassen, Ringpuffer (100, ohne Fakten) für Netzaussetzer.
- **Bewusst akzeptiert:** PostgreSQL in der Schleife (mit Absicherung), wiederverwendete IDs (nur dokumentiert), Rechte an Stellvertretern folgen dem Weg; Management-IP und Zugangsdaten ändern später als gekoppelte Abläufe (ARCHITECTURE §17).

## Beschreibung vervollständigen (2026-10-11)

Kein Code mehr, bis das ganze System beschrieben und in sich schlüssig ist. Erledigt:

- **A:** Konfiguration (öffentliche URL, Proxys, Sitzungen, Logging; TLS nur über Reverse-Proxy), `GET /elements/{id}/rules`, Format der Weiterleitung (JSON, Syslog RFC 5424, Webhook mit Secrets nur in `url`/`headers`), Audit-Verben `appear`/`disappear`, Spalte `element.params`.
- **B3:** `contracts/bootstrap.md` (eigener `.env`-Parser, unbekannte Variablen brechen ab, `errgroup`).
- **B1:** `contracts/influx.md` (Line Protocol, Measurements aus dem Vokabular, 1 s Takt, 1 min Puffer, Schema `reporting`).
- **C:** Aufbewahrung durch eigenen Dienst (180/180/730 Tage), Einzelinstanz-Sperre; Hochverfügbarkeit nicht in Version 1; Management-Adresse und Zugangsdaten werden **nie** über Aktionen geändert.
- **Komplettreview (2026-10-11):** Umfang von Version 1 in zwei Phasen (Backend, UI); README eines Treibers ist sein Vertrag vor dem Code; feste Fristen für alle sechs Phasen des Herunterfahrens (30 s, `stop_grace_period: 35s`); `db_ok` gleich `View.DB`.
- **Externes Audit der Dokumentation (2026-10-11):** TOTP-Einrichtung über die Challenge bis zur Sitzung; „erst Strom, dann Stand“ beim ersten Laden; Wiederaufnahme ab einschließlich der Version (Ereignisse idempotent); Herunterfahren in sechs Phasen mit Fristen statt gemeinsamem Abbruch (`stop_grace_period`, inzwischen 35 s); eigenes Schema `SkeletonElement`; Datenbank-Wächter für `View.DB` und `/healthz`; `verify` überall als `{timeout_ms, conditions}`; DBML-Notiz zu Sende-Zielen korrigiert.
- **Zweite Durchsicht:** Sichtbarkeit mit Gerüst (Vorfahren nur mit Name und Struktur, Links nur bei sichtbaren Enden, Ursache „nicht sichtbar“); `monika_reporting` sieht bewusst alles; Indizes für die Aufbewahrung.
- **D (Durchsicht):** Health und Rollup über die verschiedenen Elemente im Geltungsbereich (keine Doppelzählung); Regel `feeds` = fester, `uses` = wählbarer Signalfluss; `View.Stats` für die Kennzahlen; `Setting.Unit` gestrichen; Reste in allen Dokumenten nachgezogen.
- **B4:** `docs/OPERATIONS.md`: distroless-Image, lokal gebautes Binary, Stack aus Monika, PostgreSQL, InfluxDB und Caddy, `-healthcheck`, Migrationen nur vorwärts, Backup mit getrenntem Master-Key, Kennzahlen im Measurement `monika`, NTP, Datenbankrollen.
- **B2:** Treiber-Leitfaden `docs/DRIVERS.md` am erfundenen Gateway „Acme GW-4“, mit typischen Fehlern und Checkliste; Testbaukasten `sdk/sdktest` im SDK-Vertrag.
- **Vokabular:** `contracts/vocabulary.md`: ein Vokabular für Fakten und InfluxDB (Schlüssel `<measurement>.<feld>`, Einheit im Feldnamen, Richtung als Präfix), Kern-Measurement `state`, Domänen nach Broadcast-Normen, `stream` für alle Netz-Streams, Typen `bool`/`int`/`float`/`string` (echtes `int64`), Arten `status`/`gauge`/`counter`, Messpunkte und Pfade als Elemente, Präfix roher Fakten = Treibername.

## Nächste Schritte

0. ~~**Beschreibung vervollständigen**~~: für Phase 1 (Backend) abgeschlossen am 2026-10-11. Phase 2 (UI) bekommt nach dem Backend eine eigene Beschreibung (`docs/ARCHITECTURE.md` Abschnitt 1).
   - **Treiber für Phase 1**, jeder mit eigenem Vertrag (`drivers/<name>/README.md`) vor dem Code: `videoxlink` (Vertrag offen), `http` (Vertrag offen), `embrionix` (Vertrag offen), `ravio` (Vertrag offen).
1. **Verträge** in `docs/contracts/` (Mermaid), bevor weiterer Code außerhalb des Stores entsteht:
   1. ~~Treiber-SDK~~: abgestimmt am 2026-10-09, `docs/contracts/sdk.md`
   2. ~~Core-Komponenten~~: abgestimmt am 2026-10-09, `docs/contracts/core.md`
   3. ~~Abläufe~~: abgestimmt am 2026-10-09, `docs/contracts/flows.md`
   4. ~~Dienste~~ (`services.md`) und ~~Anmeldung/Rechte~~ (`auth.md`): abgestimmt am 2026-10-09
   5. ~~API~~: abgestimmt am 2026-10-09, `docs/contracts/api.md` und `api/openapi.yaml` (OpenAPI 3.0.3, `make api-check`)
2. ~~**Code an das Audit anpassen**~~: erledigt am 2026-10-11 in drei Commits: Schema neu erzeugt und Inventar mit hartem Löschen (`DeleteSource` neu, `global_ref` am Zugang, `Source.AccessElementID`, Audit `appear`/`disappear`, `element.params`); Befunde und Läufe mit `element_path`, `ClearReason`, `Rollup.Unchecked`, `clear_hold_seconds`; `preset_step.device_level`, Recht `source_edit`, Migration `00004_reporting.sql`. Für den Core bleibt: Eltern `""` = Zugang beim Übersetzen der Discovery, `element_path` nach der Regel aus `ARCHITECTURE.md` Abschnitt 5 bilden.
3. **Erst der ganze Kern, dann Treiber** (Entscheidung vom 2026-10-11): Alles unter `internal/` (dazu das SDK ohne Treiber, Bootstrap und Deployment) entsteht lauffähig und getestet, bevor der erste echte Treiber geschrieben wird. Getestet wird mit einem programmierbaren Fake-Treiber aus `sdk/sdktest`. Reihenfolge:
   0. ~~`make lint`~~ (golangci-lint v2.14.0 per `go run`, `.golangci.yml`): erledigt, 0 Befunde
   1. ~~SDK ohne Treiber~~: Typen, Vokabular, Registry mit `Validate` und `CheckElement`, `Counter`, `sdktest` mit `Reporter`, `Scheduler`, `Conformance` und Fake-Treiber (`sdk/`): erledigt
   2. ~~Konfiguration~~ (`internal/config`): erledigt
   3. ~~Fakten und Lebenszeichen~~ (`internal/core/state`): erledigt
   4. Inventar-Logik: Snapshot → `Discovery`, `Diff`, Eltern `""` = Zugang (`internal/core/inventory`)
   5. Bewertung: Präzedenz, `when`, Befunde, Health und Rollup, Ursache/Symptom, `InScope` (`internal/core/evaluation`)
   6. Schleife: `View`, Ereignisse, Befehle, `Reporter`, `Post`, Datenbank-Wächter, `Stats` (`internal/core`)
   7. Supervisor und Scheduler (`internal/core/supervisor`)
   8. Executor und Preset-Runner (`internal/action`, `internal/preset`)
   9. Weiterleitung, Influx-Schreiber, Aufbewahrung (`internal/forward`, `internal/history/influx`, `internal/retention`)
   10. Anmeldung und Rechte (`internal/auth`)
   11. API mit `oapi-codegen`, Verteiler und SSE (`internal/api`)
   12. Bootstrap und Deployment-Dateien (`cmd/monika`, `deploy/`)
4. **Treiber**, jeder mit abgestimmtem Vertrag (`drivers/<name>/README.md`) vor dem Code, beginnend mit `videoxlink`.

## Bekannte Lücken

- Leseabfragen für den Verlauf (Audit, Befunde, Läufe mit Filtern) fehlen noch; sie entstehen mit der API.
- Der Core muss beim Löschen die zurückgezogenen Befunde selbst als `finding.withdrawn` melden; der Store gibt sie nicht zurück.
- `CreateMemberLink` prüft nicht, dass das Ziel der Mitgliedschaft wirklich eine Gruppe (`kind = group`) ist.
- testcontainers schreibt viele Logzeilen in die Testausgabe.

## Offene Fragen an den User

- Sprachkonvention: Kommentare bisher Deutsch, Fehlermeldungen Englisch. Noch nicht bestätigt; bei Zustimmung in `CLAUDE.md` aufnehmen.

## Hinweise

- gopls meldet in Dateien mit Build-Tag `integration` fälschlich „go mod tidy“. Abhilfe: `"buildFlags": ["-tags=integration"]` in den gopls-Einstellungen.
- PostgreSQL 18 meldet Verstöße gegen `ON DELETE RESTRICT` mit `23001`, nicht `23503`.
- sqlc hängt Kommentare am Dateianfang an die erste Query; Query-Dateien daher ohne Datei-Kommentar.
