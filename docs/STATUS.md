# Arbeitsstand

Zwischenstand der Implementierung, damit die Arbeit in einer neuen Sitzung nahtlos weitergeht.
Zu Beginn einer Sitzung lesen, am Ende aktualisieren.

Stand: 2026-10-11

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
- **Zustand** (2026-10-08): `evaluation.Store` mit `LoadHealth`/`SaveHealth` für `element_state`; unveränderte Werte lassen die Zeile unberührt.
- **Bewertung** (2026-10-09): Regeln und Befunde in `evaluation.Store`; Regeln mit Audit, Löschen/Deaktivieren zieht aktive Befunde zurück; Befunde mit `ErrAlreadyActive`, Ende mit Grund, Ursache, Quittierung.
- **Presets** (2026-10-09): `preset.Store`; Ziele und ihre Geräte werden beim Speichern gesperrt und auf Löschung geprüft (Race mit `DeleteDevice`/`DeleteGroup` per Test abgesichert); Schritte behalten beim Ändern ihre IDs; Audit mit Schritten am Ende der Transaktion. **Damit ist der Store vorerst vollständig.**
- **Verträge** (2026-10-09): `docs/contracts/` – SDK, Core, Abläufe, Dienste, Anmeldung, API (OpenAPI 3.0.3); `make docs-check`, `make api-check`.
- **Schema und Stores aus den Verträgen** (2026-10-09): Tabellen `user_session`, `user_totp`, `user_recovery_code`, `api_token.read_only`; `action.Store`, `forward.Store`, `auth.Store` (Benutzer, Rollen, Rechte, Sitzungen, Tokens, TOTP); `secret.Vault` für die gemeinsame Rotation von Secrets und TOTP.
- **Entscheidungen vom 2026-10-07:** ~~Geräte weich löschen~~ (überholt durch das Audit); `FinishDiscovery` (ein Statement pro Lauf); Gruppen-Zyklenprüfung unter Advisory Lock.
- **Architektur-Audit** (2026-10-10): alle Befunde mit dem User durchgegangen und in ARCHITECTURE, DBML und allen Verträgen nachgezogen (siehe unten). Nur Dokumente; der Code folgt in Schritt 2.

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

## Architektur-Audit (2026-10-10)

Ein externes Audit hat Widersprüche und Lücken gefunden; alle Punkte sind entschieden und dokumentiert. Kurzfassung:

- **Health:** Element ohne geltende Regel ist *ungeprüft* (keine Health, Rollup `unchecked`); Zugang mit Heartbeat > 0 gilt als geprüft. Deaktivierte Quelle = nicht überwacht. Leere Menge bei Gerät/Gruppe = keine Health; `unknown` nur aus einer Quelle, die nicht Live ist.
- **Live:** Elemente sind `unknown`, sobald ihre Quelle nicht Live ist; Fakten dann nicht bewertet, nicht nach InfluxDB. `source.unreachable` nach 3 × Heartbeat seit dem letzten Lebenszeichen, für jeden Anlass gleich.
- **SDK:** `Source.Heartbeat()` statt `Driver.Heartbeat`; Fakt-Katalog `Driver.Facts` mit festem Typ; `Snapshot.GlobalRef` am Zugang; `global_ref` mit Namensraum; `ParentID ""` = Zugang (echte Wurzel); `Setting.WriteOnly`; keine Einstellungen für die eigene Adresse und keine geheimen Werte; Treibername = Paketname (`ravio`).
- **Bewertung:** Regel gilt, wenn Fakt (und `when_fact`) existieren; unerfülltes `when` = erfüllt; Präzedenz pro Element, Fakt und Richtung; neue Gründe `fact_missing`, `deleted` (statt `device_deleted`); `clear_hold_seconds`; Ursache/Symptom bei jeder Änderung neu abgeleitet (Wurzel). Ein Geltungsbereich für alles (`View.InScope`), Regeln vererben an Kinder.
- **Löschen:** keine Vorab-Elemente mehr; Geräte, Gruppen und Quellen werden **hart** gelöscht (CASCADE); Historie ohne Fremdschlüssel mit `element_path`. `deleted_at` entfällt.
- **Rechte:** neues globales Recht `source_edit` für Quellen und Secret-Zuweisung.
- **Aktionen:** `device_level` + `driver` als Filter; `set` nur an Elemente mit allen Schlüsseln; Preset-Ziele beim Start eingefroren; Abbrechen über Runner bzw. Executor, abgebrochene übergeordnete Läufe `cancelled`; Stopp wie Neustart (`sent` wartet ≤ 5 s).
- **Core:** `Post`, Nachrichten des Supervisors, `View` mit Links, Runs, Rules, `DB`; Ereignisse `RunChanged`, `FindingCauseChanged`; Befehle `DeleteSource`, `UpdateGroupMember`, `ReplaceSecret`; gescheiterte Snapshots gemerkt und wiederholt (`source.discovery_failed`); DB-Timeouts, `/healthz`.
- **API:** Verteiler mit einem Abo, sofort schreiben und bei Rückstau zusammenfassen, Ringpuffer (100, ohne Fakten) für Netzaussetzer.
- **Bewusst akzeptiert:** PostgreSQL in der Schleife (mit Absicherung), wiederverwendete IDs (nur dokumentiert), Rechte an Stellvertretern folgen dem Weg; Management-IP und Zugangsdaten ändern später als gekoppelte Abläufe (ARCHITECTURE §17).

## Beschreibung vervollständigen (2026-10-11)

Kein Code mehr, bis das ganze System beschrieben und in sich schlüssig ist. Erledigt:

- **A:** Konfiguration (öffentliche URL, Proxys, Sitzungen, Logging; TLS nur über Reverse-Proxy), `GET /elements/{id}/rules`, Format der Weiterleitung (JSON, Syslog RFC 5424, Webhook mit Secrets nur in `url`/`headers`), Audit-Verben `appear`/`disappear`, Spalte `element.params`.
- **B3:** `contracts/bootstrap.md` (eigener `.env`-Parser, unbekannte Variablen brechen ab, `errgroup`).
- **B1:** `contracts/influx.md` (Line Protocol, Measurements aus dem Vokabular, 1 s Takt, 1 min Puffer, Schema `reporting`).
- **C:** Aufbewahrung durch eigenen Dienst (180/180/730 Tage), Einzelinstanz-Sperre; Hochverfügbarkeit nicht in Version 1; Management-Adresse und Zugangsdaten werden **nie** über Aktionen geändert.
- **Vokabular:** `contracts/vocabulary.md`: ein Vokabular für Fakten und InfluxDB (Schlüssel `<measurement>.<feld>`, Einheit im Feldnamen, Richtung als Präfix), Kern-Measurement `state`, Domänen nach Broadcast-Normen, `stream` für alle Netz-Streams, Typen `bool`/`int`/`float`/`string` (echtes `int64`), Arten `status`/`gauge`/`counter`, Messpunkte und Pfade als Elemente, Präfix roher Fakten = Treibername.

## Nächste Schritte

0. **Beschreibung vervollständigen**, offen: Treiber-Leitfaden `docs/DRIVERS.md` mit erfundenem Beispielgerät (B2), Betrieb und Deployment (B4), danach eine vollständige Durchsicht aller Dokumente (D). Die UI folgt nach dem Backend.
1. **Verträge** in `docs/contracts/` (Mermaid), bevor weiterer Code außerhalb des Stores entsteht:
   1. ~~Treiber-SDK~~: abgestimmt am 2026-10-09, `docs/contracts/sdk.md`
   2. ~~Core-Komponenten~~: abgestimmt am 2026-10-09, `docs/contracts/core.md`
   3. ~~Abläufe~~: abgestimmt am 2026-10-09, `docs/contracts/flows.md`
   4. ~~Dienste~~ (`services.md`) und ~~Anmeldung/Rechte~~ (`auth.md`): abgestimmt am 2026-10-09
   5. ~~API~~: abgestimmt am 2026-10-09, `docs/contracts/api.md` und `api.yaml` (OpenAPI 3.0.3, `make api-check`)
2. **Code an das Audit anpassen** (Schema und Stores, bevor der Core entsteht):
   - `make db-generate`, `make db-gen-go`; Migration `00003` bleibt.
   - `deleted_at`, `CreatePlannedElement` entfernen; `DeleteDevice` und `DeleteGroup` hart löschen (CASCADE, Audit vorher), `DeleteSource` neu; Presets blockieren weiterhin.
   - `finding.element_path`, `action_run.element_path` (Namens-Snapshot), Queries ohne Fremdschlüssel auf `element`.
   - `rule.clear_hold_seconds`, `preset_step.device_level`, `permission` `source_edit` (`auth.Permission.Global`).
   - `ApplyDiscovery`: Eltern `""` = Zugang, `global_ref` am Zugang.
   - `evaluation.ClearReason`: `fact_missing`, `deleted`; `Rollup.Unchecked`.
   - `element.params`, Audit-Verben `appear`/`disappear`, `finding.caused_by_id` `SET NULL`, `action_run.parent_id` `CASCADE`, Migration für das Schema `reporting`.
3. **Umsetzung nach den Verträgen**, beginnend mit dem Bootstrap in `cmd/monika` (Konfiguration aus Umgebung/`.env`, `secret.ParseKey`, `postgres.Open`, `postgres.Migrate`, `VerifyAndRotate`, Verdrahtung).

## Bekannte Lücken

- `inventory.Source` kennt die ID ihres Zugangs-Elements nicht; die API (`Source.access_element_id`) braucht sie.
- Leseabfragen für den Verlauf (Audit, Befunde, Läufe mit Filtern) fehlen noch; sie entstehen mit der API.
- Der Core muss beim Löschen die zurückgezogenen Befunde selbst als `finding.withdrawn` melden; der Store gibt sie nicht zurück.
- `CreateMemberLink` prüft nicht, dass das Ziel der Mitgliedschaft wirklich eine Gruppe (`kind = group`) ist.
- Kein Linter festgelegt: `CLAUDE.md` verlangt einen, es gibt aber noch kein `make lint` (Vorschlag: golangci-lint).
- testcontainers schreibt viele Logzeilen in die Testausgabe.

## Offene Fragen an den User

- Sprachkonvention: Kommentare bisher Deutsch, Fehlermeldungen Englisch. Noch nicht bestätigt; bei Zustimmung in `CLAUDE.md` aufnehmen.

## Hinweise

- gopls meldet in Dateien mit Build-Tag `integration` fälschlich „go mod tidy“. Abhilfe: `"buildFlags": ["-tags=integration"]` in den gopls-Einstellungen.
- PostgreSQL 18 meldet Verstöße gegen `ON DELETE RESTRICT` mit `23001`, nicht `23503`.
- sqlc hängt Kommentare am Dateianfang an die erste Query; Query-Dateien daher ohne Datei-Kommentar.
