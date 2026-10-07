-- +goose Up
-- Generiert aus docs/schema/monika.dbml mit `make db-generate`. Nicht von Hand ändern.
-- +goose StatementBegin
CREATE TYPE "link_type" AS ENUM (
  'feeds',
  'uses',
  'member',
  'represents'
);

CREATE TYPE "port_direction" AS ENUM (
  'in',
  'out',
  'bidir'
);

CREATE TYPE "health" AS ENUM (
  'ok',
  'warning',
  'error',
  'unknown'
);

CREATE TYPE "severity" AS ENUM (
  'info',
  'warning',
  'error'
);

CREATE TYPE "rule_op" AS ENUM (
  'eq',
  'ne',
  'lt',
  'lte',
  'gt',
  'gte',
  'in',
  'not_in'
);

CREATE TYPE "action_status" AS ENUM (
  'pending',
  'sent',
  'rejected',
  'failed',
  'accepted',
  'confirmed',
  'unconfirmed',
  'cancelled'
);

CREATE TYPE "permission" AS ENUM (
  'view',
  'acknowledge',
  'operate',
  'configure',
  'critical',
  'inventory_edit',
  'rule_edit',
  'preset_run',
  'preset_edit',
  'admin'
);

CREATE TYPE "forward_kind" AS ENUM (
  'syslog',
  'webhook'
);

CREATE TYPE "finding_origin" AS ENUM (
  'rule',
  'device',
  'system'
);

CREATE TABLE "element" (
  "id" uuid PRIMARY KEY DEFAULT (uuidv7()),
  "kind" text NOT NULL,
  "parent_id" uuid,
  "device_id" uuid,
  "source_id" uuid,
  "external_id" text,
  "global_ref" text UNIQUE,
  "transport" text,
  "direction" port_direction,
  "name" text,
  "attributes" jsonb NOT NULL DEFAULT ('{}'::jsonb),
  "position" int,
  "actions" text[] NOT NULL DEFAULT ('{}'),
  "settings" jsonb,
  "present" boolean NOT NULL DEFAULT true,
  "first_seen_at" timestamptz,
  "last_seen_at" timestamptz,
  "label" text,
  "notes" text,
  "monitored" boolean NOT NULL DEFAULT true,
  "created_at" timestamptz NOT NULL DEFAULT (now()),
  "updated_at" timestamptz NOT NULL DEFAULT (now()),
  CONSTRAINT "element_class_check" CHECK ((kind = 'device'
       AND device_id = id AND parent_id IS NULL
       AND source_id IS NULL AND external_id IS NULL)
    OR
    (kind = 'group'
       AND device_id IS NULL AND parent_id IS NULL
       AND source_id IS NULL AND external_id IS NULL)
    OR
    (kind NOT IN ('device', 'group')
       AND device_id IS NOT NULL AND parent_id IS NOT NULL
       AND source_id IS NOT NULL AND external_id IS NOT NULL))
);

CREATE TABLE "device" (
  "element_id" uuid PRIMARY KEY,
  "vendor" text,
  "model" text,
  "serial" text,
  "firmware" text
);

CREATE TABLE "device_source" (
  "id" uuid PRIMARY KEY DEFAULT (uuidv7()),
  "device_id" uuid NOT NULL,
  "driver" text NOT NULL,
  "address" text,
  "config" jsonb NOT NULL DEFAULT ('{}'::jsonb),
  "secret_id" uuid,
  "enabled" boolean NOT NULL DEFAULT true,
  "created_at" timestamptz NOT NULL DEFAULT (now()),
  "updated_at" timestamptz NOT NULL DEFAULT (now())
);

CREATE TABLE "element_link" (
  "id" uuid PRIMARY KEY DEFAULT (uuidv7()),
  "type" link_type NOT NULL,
  "source_id" uuid,
  "from_id" uuid,
  "from_ref" text,
  "to_id" uuid,
  "to_ref" text,
  "position" int,
  "label" text,
  "created_at" timestamptz NOT NULL DEFAULT (now()),
  CONSTRAINT "element_link_endpoints_check" CHECK ((from_id IS NOT NULL OR from_ref IS NOT NULL)
    AND (to_id IS NOT NULL OR to_ref IS NOT NULL)),
  CONSTRAINT "element_link_owner_check" CHECK ((type = 'member' AND source_id IS NULL)
    OR (type <> 'member' AND source_id IS NOT NULL))
);

CREATE TABLE "rule" (
  "id" uuid PRIMARY KEY DEFAULT (uuidv7()),
  "name" text NOT NULL,
  "enabled" boolean NOT NULL DEFAULT true,
  "severity" severity NOT NULL DEFAULT 'error',
  "scope_element_id" uuid,
  "match_kind" text,
  "match_transport" text,
  "match_driver" text,
  "fact" text NOT NULL,
  "op" rule_op NOT NULL,
  "value" jsonb NOT NULL,
  "when_fact" text,
  "when_op" rule_op,
  "when_value" jsonb,
  "clear_value" jsonb,
  "hold_seconds" int NOT NULL DEFAULT 0,
  "created_at" timestamptz NOT NULL DEFAULT (now()),
  "updated_at" timestamptz NOT NULL DEFAULT (now()),
  CONSTRAINT "rule_when_check" CHECK ((when_fact IS NULL AND when_op IS NULL AND when_value IS NULL)
    OR (when_fact IS NOT NULL AND when_op IS NOT NULL AND when_value IS NOT NULL)),
  CONSTRAINT "rule_hold_check" CHECK (hold_seconds >= 0)
);

CREATE TABLE "finding" (
  "id" uuid PRIMARY KEY DEFAULT (uuidv7()),
  "element_id" uuid NOT NULL,
  "origin" finding_origin NOT NULL,
  "rule_id" uuid,
  "code" text NOT NULL,
  "severity" severity NOT NULL,
  "message" text,
  "details" jsonb,
  "caused_by_id" uuid,
  "raised_at" timestamptz NOT NULL,
  "cleared_at" timestamptz,
  "acked_at" timestamptz,
  "acked_by_id" uuid
);

CREATE TABLE "preset" (
  "id" uuid PRIMARY KEY DEFAULT (uuidv7()),
  "name" text NOT NULL,
  "description" text,
  "created_by_id" uuid,
  "created_at" timestamptz NOT NULL DEFAULT (now()),
  "updated_at" timestamptz NOT NULL DEFAULT (now())
);

CREATE TABLE "preset_step" (
  "id" uuid PRIMARY KEY DEFAULT (uuidv7()),
  "preset_id" uuid NOT NULL,
  "stage" int NOT NULL,
  "position" int NOT NULL,
  "delay_ms" int NOT NULL DEFAULT 0,
  "element_id" uuid NOT NULL,
  "action" text NOT NULL,
  "params" jsonb,
  "continue_on_error" boolean NOT NULL DEFAULT false,
  CONSTRAINT "preset_step_check" CHECK (stage >= 1 AND delay_ms >= 0)
);

CREATE TABLE "action_run" (
  "id" uuid PRIMARY KEY DEFAULT (uuidv7()),
  "parent_id" uuid,
  "preset_id" uuid,
  "preset_step_id" uuid,
  "element_id" uuid,
  "source_id" uuid,
  "action" text NOT NULL,
  "params" jsonb,
  "status" action_status NOT NULL DEFAULT 'pending',
  "critical" boolean NOT NULL DEFAULT false,
  "verify" jsonb,
  "response" jsonb,
  "requested_by_id" uuid NOT NULL,
  "requested_at" timestamptz NOT NULL DEFAULT (now()),
  "sent_at" timestamptz,
  "responded_at" timestamptz,
  "finished_at" timestamptz,
  CONSTRAINT "action_run_target_check" CHECK (element_id IS NOT NULL
    OR (preset_id IS NOT NULL AND parent_id IS NULL))
);

CREATE TABLE "app_user" (
  "id" uuid PRIMARY KEY DEFAULT (uuidv7()),
  "username" text UNIQUE NOT NULL,
  "display_name" text,
  "email" text,
  "password_hash" text,
  "is_service" boolean NOT NULL DEFAULT false,
  "disabled" boolean NOT NULL DEFAULT false,
  "created_at" timestamptz NOT NULL DEFAULT (now()),
  "last_login_at" timestamptz
);

CREATE TABLE "user_identity" (
  "id" uuid PRIMARY KEY DEFAULT (uuidv7()),
  "user_id" uuid NOT NULL,
  "issuer" text NOT NULL,
  "subject" text NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT (now()),
  "last_used_at" timestamptz
);

CREATE TABLE "role" (
  "id" uuid PRIMARY KEY DEFAULT (uuidv7()),
  "name" text UNIQUE NOT NULL,
  "description" text,
  "external_ref" text UNIQUE
);

CREATE TABLE "role_member" (
  "role_id" uuid NOT NULL,
  "user_id" uuid NOT NULL,
  "origin" text NOT NULL DEFAULT 'manual',
  "created_at" timestamptz NOT NULL DEFAULT (now()),
  PRIMARY KEY ("role_id", "user_id")
);

CREATE TABLE "permission_grant" (
  "id" uuid PRIMARY KEY DEFAULT (uuidv7()),
  "role_id" uuid NOT NULL,
  "permission" permission NOT NULL,
  "scope_element_id" uuid,
  "created_at" timestamptz NOT NULL DEFAULT (now()),
  CONSTRAINT "permission_grant_global_check" CHECK (permission NOT IN ('preset_run', 'preset_edit', 'admin')
    OR scope_element_id IS NULL)
);

CREATE TABLE "api_token" (
  "id" uuid PRIMARY KEY DEFAULT (uuidv7()),
  "user_id" uuid NOT NULL,
  "name" text NOT NULL,
  "token_hash" text UNIQUE NOT NULL,
  "expires_at" timestamptz,
  "revoked_at" timestamptz,
  "last_used_at" timestamptz,
  "created_at" timestamptz NOT NULL DEFAULT (now())
);

CREATE TABLE "audit_log" (
  "id" uuid PRIMARY KEY DEFAULT (uuidv7()),
  "actor_id" uuid,
  "at" timestamptz NOT NULL DEFAULT (now()),
  "verb" text NOT NULL,
  "object_type" text NOT NULL,
  "object_id" uuid,
  "before" jsonb,
  "after" jsonb
);

CREATE TABLE "forward_target" (
  "id" uuid PRIMARY KEY DEFAULT (uuidv7()),
  "name" text NOT NULL,
  "kind" forward_kind NOT NULL,
  "enabled" boolean NOT NULL DEFAULT true,
  "config" jsonb NOT NULL DEFAULT ('{}'::jsonb),
  "secret_id" uuid,
  "events" text[] NOT NULL,
  "min_severity" severity,
  "scope_element_id" uuid,
  "last_sent_at" timestamptz,
  "last_error" text,
  "created_at" timestamptz NOT NULL DEFAULT (now()),
  "updated_at" timestamptz NOT NULL DEFAULT (now())
);

CREATE TABLE "secret" (
  "id" uuid PRIMARY KEY DEFAULT (uuidv7()),
  "name" text UNIQUE NOT NULL,
  "description" text,
  "ciphertext" bytea NOT NULL,
  "nonce" bytea NOT NULL,
  "key_id" text NOT NULL,
  "created_by_id" uuid,
  "updated_by_id" uuid,
  "created_at" timestamptz NOT NULL DEFAULT (now()),
  "updated_at" timestamptz NOT NULL DEFAULT (now())
);

CREATE TABLE "element_state" (
  "element_id" uuid PRIMARY KEY,
  "facts" jsonb NOT NULL DEFAULT ('{}'::jsonb),
  "alarms" jsonb,
  "health" health NOT NULL DEFAULT 'unknown',
  "rollup" jsonb,
  "reported_at" timestamptz,
  "updated_at" timestamptz NOT NULL DEFAULT (now())
);

CREATE TABLE "device_source_state" (
  "source_id" uuid PRIMARY KEY,
  "connected" boolean NOT NULL DEFAULT false,
  "last_contact_at" timestamptz,
  "last_sync_at" timestamptz,
  "last_error" text,
  "updated_at" timestamptz NOT NULL DEFAULT (now())
);

CREATE UNIQUE INDEX "element_device_external_uq" ON "element" ("device_id", "external_id");

CREATE INDEX ON "element" ("parent_id");

CREATE INDEX ON "element" ("device_id");

CREATE INDEX ON "element" ("source_id");

CREATE INDEX ON "element" ("kind", "transport");

CREATE INDEX ON "element" USING GIN ("actions");

CREATE INDEX ON "device" ("vendor", "model");

CREATE INDEX ON "device" ("firmware");

CREATE UNIQUE INDEX ON "device_source" ("device_id", "driver");

CREATE INDEX ON "device_source" ("secret_id");

CREATE INDEX ON "element_link" ("from_id");

CREATE INDEX ON "element_link" ("to_id");

CREATE INDEX ON "element_link" ("source_id");

CREATE INDEX ON "element_link" ("from_ref");

CREATE INDEX ON "element_link" ("to_ref");

CREATE UNIQUE INDEX "element_link_uq" ON "element_link" ((type), (COALESCE(from_id::text, from_ref)), (COALESCE(to_id::text, to_ref)));

CREATE INDEX ON "rule" ("scope_element_id");

CREATE INDEX ON "rule" ("fact");

CREATE INDEX ON "finding" ("element_id");

CREATE INDEX ON "finding" ("rule_id");

CREATE INDEX ON "finding" ("caused_by_id");

CREATE INDEX ON "finding" ("raised_at");

CREATE INDEX ON "preset_step" ("preset_id", "stage", "position");

CREATE INDEX ON "preset_step" ("element_id");

CREATE INDEX ON "action_run" ("element_id");

CREATE INDEX ON "action_run" ("parent_id");

CREATE INDEX ON "action_run" ("preset_id");

CREATE INDEX ON "action_run" ("requested_at");

CREATE INDEX ON "action_run" ("status");

CREATE INDEX ON "action_run" ("requested_by_id");

CREATE UNIQUE INDEX ON "user_identity" ("issuer", "subject");

CREATE INDEX ON "user_identity" ("user_id");

CREATE INDEX ON "role_member" ("user_id");

CREATE INDEX ON "permission_grant" ("role_id");

CREATE INDEX ON "permission_grant" ("scope_element_id");

CREATE UNIQUE INDEX "permission_grant_uq" ON "permission_grant" ((role_id), (permission), (COALESCE(scope_element_id, '00000000-0000-0000-0000-000000000000'::uuid)));

CREATE INDEX ON "api_token" ("user_id");

CREATE INDEX ON "audit_log" ("object_type", "object_id");

CREATE INDEX ON "audit_log" ("actor_id");

CREATE INDEX ON "audit_log" ("at");

CREATE INDEX ON "element_state" ("health");

COMMENT ON TABLE "element" IS 'Hinweis: device_id = id beim Gerät erfordert, dass die id vor dem Insert
vergeben wird (in Go erzeugen statt per Default).
';

COMMENT ON COLUMN "element"."kind" IS 'device, group, module, port, network_interface, clock, sender, receiver, peer, tunnel, processing, sensor, trigger';

COMMENT ON COLUMN "element"."parent_id" IS 'nur physische/strukturelle Hierarchie';

COMMENT ON COLUMN "element"."device_id" IS 'zugehöriges Gerät; beim Gerät selbst = eigene id; NULL bei Gruppen';

COMMENT ON COLUMN "element"."source_id" IS 'besitzende Quelle; NULL bei Gerät und Gruppe';

COMMENT ON COLUMN "element"."external_id" IS 'stabile ID innerhalb des Geräts, z.B. Slot-Pfad oder UnitID';

COMMENT ON COLUMN "element"."global_ref" IS 'global eindeutige ID (XLink-UnitID, NMOS-UUID). Nur am Element im eigenen Baum, nie an Stellvertretern';

COMMENT ON COLUMN "element"."transport" IS 'sdi, madi, analog, aes3, st2110-20, st2110-30, st2110-40, aes67, xlink, srt, ndi, ...';

COMMENT ON COLUMN "element"."direction" IS 'Fähigkeit des Anschlusses; die aktuelle Richtung steht in den Fakten';

COMMENT ON COLUMN "element"."name" IS 'Name, wie das Gerät ihn meldet';

COMMENT ON COLUMN "element"."attributes" IS 'statische Treiber-Attribute, nur zur Anzeige';

COMMENT ON COLUMN "element"."position" IS 'Sortierung innerhalb des Parents';

COMMENT ON COLUMN "element"."actions" IS 'unterstützte Befehle, z.B. {start,stop,reboot,vxl.reset_video_buffer}';

COMMENT ON COLUMN "element"."settings" IS 'schreibbare Fakt-Schlüssel mit Typ, Grenzen und optional critical, z.B. {"bitrate": {"type": "int", "min": 1, "max": 100}, "eth0.ip": {"type": "ipv4", "critical": true}}';

COMMENT ON COLUMN "element"."present" IS 'false = verschwunden oder noch nie gesehen; Zeile bleibt erhalten';

COMMENT ON COLUMN "element"."first_seen_at" IS 'NULL = vom User vorab angelegt, vom Treiber noch nie gesehen';

COMMENT ON COLUMN "element"."monitored" IS 'false = von allen Regeln ausgenommen (unbenutzt)';

COMMENT ON TABLE "device" IS '1:1-Erweiterung von element (kind = device). Wie das Gerät erreicht wird, steht in device_source.';

COMMENT ON TABLE "device_source" IS 'Ein Gerät kann mehrere Quellen haben (z.B. XLink-API und IPMI). Jede Quelle besitzt ihre eigenen Elemente und Links; ein Re-Discovery fasst nur diese an.';

COMMENT ON COLUMN "device_source"."driver" IS 'videoxlink, embrionix, directout-ravio, ipmi, ...';

COMMENT ON COLUMN "device_source"."address" IS 'Management-Host/IP';

COMMENT ON COLUMN "device_source"."config" IS 'treiberspezifische Verbindungsparameter, keine Geheimnisse';

COMMENT ON COLUMN "device_source"."secret_id" IS 'Zugangsdaten aus dem Secret-Store; viele Quellen können dasselbe Secret nutzen';

COMMENT ON TABLE "element_link" IS 'Regeln:
- Treiber-Links (feeds, uses, represents) werden bei Wegfall gelöscht,
  nicht weich markiert; ihre Historie gehört nach InfluxDB/Audit.
- Ein Link über Gerätegrenzen wird von der Seite gemeldet, die ihn
  konfiguriert (bei XLink: der Decoder).
- member-Links dürfen keine Zyklen bilden (Prüfung in der Anwendung).
- element_link_uq: keine doppelten Links, auch solange unaufgelöst.
';

COMMENT ON COLUMN "element_link"."source_id" IS 'meldende Quelle; NULL = vom User (member)';

COMMENT ON COLUMN "element_link"."from_ref" IS 'global_ref der Quelle, solange nicht aufgelöst';

COMMENT ON COLUMN "element_link"."to_ref" IS 'global_ref des Ziels, solange nicht aufgelöst';

COMMENT ON COLUMN "element_link"."position" IS 'Position in der Gruppe, z.B. Stagebox-Kanal 5';

COMMENT ON COLUMN "element_link"."label" IS 'Label in diesem Kontext';

COMMENT ON TABLE "rule" IS 'Alle Regeln kommen vom User; es gibt keine mitgelieferten Standardregeln.
Operator und Typ von value werden in Go beim Speichern geprüft.
';

COMMENT ON COLUMN "rule"."scope_element_id" IS 'Element, Gerät (inkl. Kinder) oder Gruppe (inkl. Mitglieder, rekursiv)';

COMMENT ON COLUMN "rule"."fact" IS 'running, signal_present, format.rate, ptp_offset, vxl.cpu_temp, ...';

COMMENT ON COLUMN "rule"."value" IS 'true | 50 | "i" | [50, 59.94]';

COMMENT ON COLUMN "rule"."clear_value" IS 'Hysterese, nur numerisch: Befund endet erst, wenn dieser Wert erreicht ist';

COMMENT ON COLUMN "rule"."hold_seconds" IS 'Verletzung muss so lange anliegen, bevor ein Befund entsteht';

COMMENT ON TABLE "finding" IS 'Höchstens ein aktiver Befund pro Element, Code und Regel. DBML kennt keine
Teilindizes; finding_active_uq steht deshalb in migrations/00002_finding_active_uq.sql.
';

COMMENT ON COLUMN "finding"."rule_id" IS 'bei origin = rule; wird beim Löschen der Regel NULL, damit die Historie bleibt';

COMMENT ON COLUMN "finding"."code" IS 'rule: rule.violated · device: Alarmcode des Geräts · system: source.unreachable, data.stale';

COMMENT ON COLUMN "finding"."message" IS 'lesbarer Text, inkl. Regelname zum Zeitpunkt des Befunds';

COMMENT ON COLUMN "finding"."details" IS 'Sollwert, Istwert, Regel-Snapshot';

COMMENT ON COLUMN "finding"."caused_by_id" IS 'NULL = Ursache, sonst Symptom';

COMMENT ON COLUMN "finding"."cleared_at" IS 'NULL = aktiv';

COMMENT ON COLUMN "finding"."acked_at" IS 'Quittierung ändert nicht die Health, nur Anzeige und Meldung';

COMMENT ON TABLE "preset" IS 'Gespeicherte Befehlsfolge, kein Sollzustand. Stufen laufen nacheinander, Schritte einer Stufe parallel.';

COMMENT ON TABLE "preset_step" IS 'Ein Element mit Preset-Schritten ist nicht löschbar: Presets verlieren keine Schritte stillschweigend.';

COMMENT ON COLUMN "preset_step"."stage" IS 'gleiche Stufe = parallel; Stufen nacheinander, jede wartet auf Endzustand aller Läufe der vorigen';

COMMENT ON COLUMN "preset_step"."position" IS 'Sortierung innerhalb der Stufe (nur Anzeige)';

COMMENT ON COLUMN "preset_step"."delay_ms" IS 'Wartezeit ab Beginn der Stufe, bevor dieser Schritt startet';

COMMENT ON COLUMN "preset_step"."element_id" IS 'Element, Gerät oder Gruppe (Verteilung auf Mitglieder)';

COMMENT ON COLUMN "preset_step"."action" IS 'start, stop, set, reboot, trigger, vxl.*';

COMMENT ON COLUMN "preset_step"."params" IS 'bei set: {"bitrate": 25, "codec": "h265"}; bei trigger: Overrides';

COMMENT ON COLUMN "preset_step"."continue_on_error" IS 'false = Fehler bricht das Preset ab';

COMMENT ON TABLE "action_run" IS 'Zugleich Audit-Log aller Aktionen: wer, was, wann, mit welchem Ergebnis.
Übergeordnete Läufe (Preset, Sammelaktion) fassen den Status ihrer Kinder zusammen.
Gelöschte Presets und Schritte lassen die Läufe stehen (preset_id, preset_step_id werden NULL).
';

COMMENT ON COLUMN "action_run"."parent_id" IS 'Hierarchie: Preset-Lauf → Schritt-Lauf → Element-Läufe (bei Gruppen/Geräten)';

COMMENT ON COLUMN "action_run"."preset_id" IS 'gesetzt am Wurzel-Lauf eines Presets';

COMMENT ON COLUMN "action_run"."element_id" IS 'Ziel; bei Sammelaktion die Gruppe/das Gerät; NULL nur am Preset-Wurzel-Lauf';

COMMENT ON COLUMN "action_run"."source_id" IS 'ausführende Quelle; NULL bei übergeordneten Läufen';

COMMENT ON COLUMN "action_run"."action" IS 'start, stop, set, reboot, vxl.*; am Preset-Wurzel-Lauf: preset';

COMMENT ON COLUMN "action_run"."params" IS 'bei set: zu setzende Werte; bei trigger: Overrides nur für diesen Aufruf';

COMMENT ON COLUMN "action_run"."critical" IS 'Snapshot: war der Lauf kritisch (Aktion oder ein gesetzter Schlüssel)';

COMMENT ON COLUMN "action_run"."verify" IS 'Snapshot der Prüfbedingung, z.B. [{"fact": "bitrate", "op": "eq", "value": 20}] mit timeout_s';

COMMENT ON COLUMN "action_run"."response" IS 'Antwort des Geräts, z.B. DeviceError mit Detail und abgelehnten Feldern';

COMMENT ON COLUMN "action_run"."requested_by_id" IS 'Mensch oder Dienstkonto (API-Token)';

COMMENT ON TABLE "app_user" IS '"user" ist in PostgreSQL reserviert, daher app_user.';

COMMENT ON COLUMN "app_user"."username" IS 'in Go auf Kleinschreibung normalisiert';

COMMENT ON COLUMN "app_user"."password_hash" IS 'lokale Anmeldung (argon2id); NULL = nur externe Anmeldung';

COMMENT ON COLUMN "app_user"."is_service" IS 'Dienstkonto: nur API-Tokens, keine interaktive Anmeldung';

COMMENT ON COLUMN "app_user"."disabled" IS 'Benutzer werden nie gelöscht, nur deaktiviert';

COMMENT ON TABLE "user_identity" IS 'Externe Anmeldewege (OIDC). Kommt ein IdP dazu, entstehen nur neue Zeilen.';

COMMENT ON COLUMN "user_identity"."issuer" IS 'OIDC-Issuer-URL des Identity Providers';

COMMENT ON COLUMN "user_identity"."subject" IS 'sub-Claim';

COMMENT ON COLUMN "role"."external_ref" IS 'Wert im Groups-Claim des IdP; Mitgliedschaft wird beim Login synchronisiert';

COMMENT ON COLUMN "role_member"."origin" IS 'manual | idp; idp-Mitgliedschaften werden beim Login überschrieben';

COMMENT ON COLUMN "permission_grant"."scope_element_id" IS 'Gerät oder Gruppe, rekursiv; NULL = überall';

COMMENT ON COLUMN "api_token"."user_id" IS 'meist ein Dienstkonto';

COMMENT ON COLUMN "api_token"."token_hash" IS 'SHA-256 eines zufälligen Tokens; Klartext nur einmal bei Erstellung angezeigt';

COMMENT ON TABLE "audit_log" IS 'Alle Änderungen außer Aktionen; Aktionen stehen in action_run.';

COMMENT ON COLUMN "audit_log"."actor_id" IS 'NULL = Monika selbst';

COMMENT ON COLUMN "audit_log"."verb" IS 'create | update | delete | login | login_failed';

COMMENT ON COLUMN "audit_log"."object_type" IS 'rule, preset, device_source, role, permission_grant, forward_target, ...';

COMMENT ON COLUMN "audit_log"."before" IS 'Geheimnisse geschwärzt';

COMMENT ON COLUMN "audit_log"."after" IS 'Geheimnisse geschwärzt';

COMMENT ON TABLE "forward_target" IS 'Best effort: wenige Wiederholungen im Speicher, danach last_error. Keine Outbox.';

COMMENT ON COLUMN "forward_target"."config" IS 'syslog: host, port, protocol, facility · webhook: url, method, headers, body (Vorlage)';

COMMENT ON COLUMN "forward_target"."secret_id" IS 'z.B. Token im Webhook-Header';

COMMENT ON COLUMN "forward_target"."events" IS 'finding.raised, finding.cleared, finding.acked, action.finished';

COMMENT ON COLUMN "forward_target"."min_severity" IS 'nur für finding.*; NULL = alle';

COMMENT ON COLUMN "forward_target"."scope_element_id" IS 'Gerät oder Gruppe, rekursiv; NULL = alles';

COMMENT ON TABLE "secret" IS 'Ver- und Entschlüsselung nur in Go, nie per pgcrypto (Schlüssel würde im SQL landen).
Associated Data = id, damit Werte nicht zwischen Zeilen vertauscht werden können.
Werte werden nie angezeigt, geloggt oder ins Audit geschrieben.
Anlegen/Ersetzen/Löschen: Recht admin. Zuweisen an eine Quelle: inventory_edit.
';

COMMENT ON COLUMN "secret"."name" IS 'z.B. xlink-admin; in der UI auswählbar';

COMMENT ON COLUMN "secret"."ciphertext" IS 'AES-256-GCM; Klartext ist JSON, Format legt der Treiber fest';

COMMENT ON COLUMN "secret"."nonce" IS '12 Byte, pro Verschlüsselung zufällig';

COMMENT ON COLUMN "secret"."key_id" IS 'Version des Master-Keys, mit dem verschlüsselt wurde';

COMMENT ON COLUMN "element_state"."facts" IS 'flache Map aller aktuellen Werte, z.B. {"running": true, "format.rate": 50}';

COMMENT ON COLUMN "element_state"."alarms" IS 'vom Gerät gemeldete Alarme → automatisch Befunde';

COMMENT ON COLUMN "element_state"."health" IS 'vom Core: schlimmster aktiver Befund am Element';

COMMENT ON COLUMN "element_state"."rollup" IS 'nur Gerät/Gruppe: {"ok": 12, "warning": 1, "error": 0, "unknown": 3}';

COMMENT ON COLUMN "element_state"."reported_at" IS 'Zeitpunkt der Messung am Gerät';

COMMENT ON COLUMN "element_state"."updated_at" IS 'Zeitpunkt des Schreibens';

COMMENT ON TABLE "device_source_state" IS 'Verbindungszustand von Monika zur Quelle. Getrennt von device_source, weil er sich ständig ändert.';

COMMENT ON COLUMN "device_source_state"."last_sync_at" IS 'letztes vollständiges Discovery';

ALTER TABLE "element" ADD FOREIGN KEY ("parent_id") REFERENCES "element" ("id") DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "element" ADD FOREIGN KEY ("device_id") REFERENCES "device" ("element_id") DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "element" ADD FOREIGN KEY ("source_id") REFERENCES "device_source" ("id") DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "device" ADD FOREIGN KEY ("element_id") REFERENCES "element" ("id") DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "device_source" ADD FOREIGN KEY ("device_id") REFERENCES "device" ("element_id") DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "device_source" ADD FOREIGN KEY ("secret_id") REFERENCES "secret" ("id") ON DELETE RESTRICT DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "element_link" ADD FOREIGN KEY ("source_id") REFERENCES "device_source" ("id") DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "element_link" ADD FOREIGN KEY ("from_id") REFERENCES "element" ("id") ON DELETE CASCADE DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "element_link" ADD FOREIGN KEY ("to_id") REFERENCES "element" ("id") ON DELETE CASCADE DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "rule" ADD FOREIGN KEY ("scope_element_id") REFERENCES "element" ("id") DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "finding" ADD FOREIGN KEY ("element_id") REFERENCES "element" ("id") DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "finding" ADD FOREIGN KEY ("caused_by_id") REFERENCES "finding" ("id") DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "finding" ADD FOREIGN KEY ("acked_by_id") REFERENCES "app_user" ("id") DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "finding" ADD FOREIGN KEY ("rule_id") REFERENCES "rule" ("id") ON DELETE SET NULL DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "preset" ADD FOREIGN KEY ("created_by_id") REFERENCES "app_user" ("id") DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "preset_step" ADD FOREIGN KEY ("preset_id") REFERENCES "preset" ("id") ON DELETE CASCADE DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "preset_step" ADD FOREIGN KEY ("element_id") REFERENCES "element" ("id") ON DELETE RESTRICT DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "action_run" ADD FOREIGN KEY ("parent_id") REFERENCES "action_run" ("id") DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "action_run" ADD FOREIGN KEY ("element_id") REFERENCES "element" ("id") DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "action_run" ADD FOREIGN KEY ("source_id") REFERENCES "device_source" ("id") DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "action_run" ADD FOREIGN KEY ("requested_by_id") REFERENCES "app_user" ("id") DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "action_run" ADD FOREIGN KEY ("preset_id") REFERENCES "preset" ("id") ON DELETE SET NULL DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "action_run" ADD FOREIGN KEY ("preset_step_id") REFERENCES "preset_step" ("id") ON DELETE SET NULL DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "user_identity" ADD FOREIGN KEY ("user_id") REFERENCES "app_user" ("id") DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "role_member" ADD FOREIGN KEY ("user_id") REFERENCES "app_user" ("id") DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "role_member" ADD FOREIGN KEY ("role_id") REFERENCES "role" ("id") ON DELETE CASCADE DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "permission_grant" ADD FOREIGN KEY ("role_id") REFERENCES "role" ("id") ON DELETE CASCADE DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "permission_grant" ADD FOREIGN KEY ("scope_element_id") REFERENCES "element" ("id") ON DELETE CASCADE DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "api_token" ADD FOREIGN KEY ("user_id") REFERENCES "app_user" ("id") DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "audit_log" ADD FOREIGN KEY ("actor_id") REFERENCES "app_user" ("id") DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "forward_target" ADD FOREIGN KEY ("scope_element_id") REFERENCES "element" ("id") DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "forward_target" ADD FOREIGN KEY ("secret_id") REFERENCES "secret" ("id") ON DELETE RESTRICT DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "secret" ADD FOREIGN KEY ("created_by_id") REFERENCES "app_user" ("id") DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "secret" ADD FOREIGN KEY ("updated_by_id") REFERENCES "app_user" ("id") DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "element_state" ADD FOREIGN KEY ("element_id") REFERENCES "element" ("id") DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "device_source_state" ADD FOREIGN KEY ("source_id") REFERENCES "device_source" ("id") DEFERRABLE INITIALLY IMMEDIATE;

-- +goose StatementEnd
