-- name: CreateDevice :exec
-- Ein Gerät ist ein Element mit kind = device; device_id verweist auf sich selbst.
WITH e AS (
  INSERT INTO element (id, kind, device_id, label, notes)
  VALUES (@id::uuid, 'device', @id::uuid, @label, @notes)
  RETURNING *
)
INSERT INTO audit_log (actor_id, verb, object_type, object_id, after)
SELECT sqlc.narg(actor_id)::uuid, 'create', 'element', e.id, to_jsonb(e) FROM e;

-- name: UpdateSourceIdentity :execrows
-- Identität des Geräts, wie diese Quelle sie meldet.
UPDATE device_source
SET vendor     = @vendor,
    model      = @model,
    serial     = @serial,
    firmware   = @firmware,
    updated_at = now()
WHERE id = @id;

-- name: CreateDeviceSource :execrows
-- Legt die Quelle und ihr Zugangs-Element (kind = access) in einem Statement an.
-- Ist das Gerät unbekannt, wird nichts angelegt (0 Zeilen).
WITH src AS (
  INSERT INTO device_source (id, device_id, driver, address, config, secret_id, enabled)
  SELECT @id::uuid, e.id, @driver::text, @address, @config::jsonb, @secret_id, @enabled::boolean
  FROM element e
  WHERE e.id = @device_id::uuid AND e.kind = 'device'
  RETURNING *
), access AS (
  INSERT INTO element (id, kind, device_id, parent_id, source_id, name)
  SELECT @access_id::uuid, 'access', src.device_id, src.device_id, src.id, src.driver
  FROM src
)
INSERT INTO audit_log (actor_id, verb, object_type, object_id, after)
SELECT sqlc.narg(actor_id)::uuid, 'create', 'device_source', src.id, to_jsonb(src) FROM src;

-- name: UpdateDeviceSource :execrows
-- Treiber und Gerät einer Quelle stehen fest; sonst würden ihre Elemente den Besitzer wechseln.
WITH before AS (
  SELECT * FROM device_source WHERE id = @id::uuid
), upd AS (
  UPDATE device_source
  SET address    = @address,
      config     = @config,
      secret_id  = @secret_id,
      enabled    = @enabled,
      updated_at = now()
  WHERE device_source.id = @id::uuid
  RETURNING *
)
INSERT INTO audit_log (actor_id, verb, object_type, object_id, before, after)
SELECT sqlc.narg(actor_id)::uuid, 'update', 'device_source', upd.id, to_jsonb(before), to_jsonb(upd)
FROM upd JOIN before ON before.id = upd.id;

-- name: LockDevice :one
-- Sperrt das Geräte-Element bis zum Ende der Transaktion.
SELECT id
FROM element
WHERE id = @id AND kind = 'device'
FOR UPDATE;

-- name: LockSource :one
-- Sperrt die Quelle bis zum Ende der Transaktion.
SELECT id
FROM device_source
WHERE id = @id
FOR UPDATE;

-- name: ListPresetsUsingDevice :many
-- Presets mit Schritten, die auf ein Element des Geräts zielen; sie blockieren das Löschen.
SELECT DISTINCT p.id, p.name
FROM preset p
JOIN preset_step s ON s.preset_id = p.id
JOIN element e ON e.id = s.element_id
WHERE e.device_id = @device_id::uuid
ORDER BY p.name, p.id;

-- name: ListPresetsUsingSource :many
-- Presets mit Schritten, die auf ein Element der Quelle zielen; sie blockieren das Löschen.
SELECT DISTINCT p.id, p.name
FROM preset p
JOIN preset_step s ON s.preset_id = p.id
JOIN element e ON e.id = s.element_id
WHERE e.source_id = @source_id::uuid
ORDER BY p.name, p.id;

-- name: WithdrawFindings :execrows
-- Zieht alle aktiven Befunde an den angegebenen Elementen zurück (cleared_reason deleted).
-- Die Befunde bleiben als Verlauf; element_id verweist ohne Fremdschlüssel weiter auf das Element.
UPDATE finding
SET cleared_at = now(),
    details    = COALESCE(details, '{}'::jsonb) || '{"cleared_reason": "deleted"}'::jsonb
WHERE cleared_at IS NULL
  AND element_id = ANY (@element_ids::uuid[]);

-- name: ListDeviceElementIDs :many
-- Gerät, seine Zugänge und alle Treiber-Elemente.
SELECT id FROM element WHERE device_id = @device_id::uuid;

-- name: ListSourceElementIDs :many
-- Zugang und Treiber-Elemente einer Quelle.
SELECT id FROM element WHERE source_id = @source_id::uuid;

-- name: DeleteScopedConfig :exec
-- Löscht Regeln, Rechte, Weiterleitungen und Mitgliedschaften, die an den angegebenen Elementen
-- hängen; ohne ihr Ziel sind sie bedeutungslos. Die Fremdschlüssel würden sie ohnehin löschen
-- (CASCADE); dieses Statement schreibt vorher je einen Audit-Eintrag. Vorher die Befunde
-- zurückziehen.
WITH rules AS (
  DELETE FROM rule WHERE scope_element_id = ANY (@element_ids::uuid[]) RETURNING *
), grants AS (
  DELETE FROM permission_grant WHERE scope_element_id = ANY (@element_ids::uuid[]) RETURNING *
), forwards AS (
  DELETE FROM forward_target WHERE scope_element_id = ANY (@element_ids::uuid[]) RETURNING *
), memberships AS (
  DELETE FROM element_link
  WHERE type = 'member' AND (from_id = ANY (@element_ids::uuid[]) OR to_id = ANY (@element_ids::uuid[]))
  RETURNING *
)
INSERT INTO audit_log (actor_id, verb, object_type, object_id, before)
SELECT sqlc.narg(actor_id)::uuid, 'delete', 'rule', rules.id, to_jsonb(rules) FROM rules
UNION ALL
SELECT sqlc.narg(actor_id)::uuid, 'delete', 'permission_grant', grants.id, to_jsonb(grants) FROM grants
UNION ALL
SELECT sqlc.narg(actor_id)::uuid, 'delete', 'forward_target', forwards.id, to_jsonb(forwards) FROM forwards
UNION ALL
SELECT sqlc.narg(actor_id)::uuid, 'delete', 'element_link', memberships.id, to_jsonb(memberships) FROM memberships;

-- name: DeleteDevice :exec
-- Löscht das Gerät hart; seine Quellen, Elemente, Links und element_state folgen per CASCADE.
-- Audit: ein Eintrag für das Gerät; die mitgelöschten Quellen und Elemente stehen nur dort.
-- Nur über postgres.Store.DeleteDevice aufrufen (Sperre, Presets, Befunde, Konfiguration).
WITH del AS (
  DELETE FROM element WHERE id = @device_id::uuid AND kind = 'device' RETURNING *
)
INSERT INTO audit_log (actor_id, verb, object_type, object_id, before)
SELECT sqlc.narg(actor_id)::uuid, 'delete', 'element', del.id, to_jsonb(del) FROM del;

-- name: DeleteSource :exec
-- Löscht die Quelle hart; ihr Zugang, ihre Elemente und Links folgen per CASCADE.
-- Audit: ein Eintrag für die Quelle; die mitgelöschten Elemente stehen nur dort.
-- Nur über postgres.Store.DeleteSource aufrufen (Sperre, Presets, Befunde, Konfiguration).
WITH del AS (
  DELETE FROM device_source WHERE id = @source_id::uuid RETURNING *
)
INSERT INTO audit_log (actor_id, verb, object_type, object_id, before)
SELECT sqlc.narg(actor_id)::uuid, 'delete', 'device_source', del.id, to_jsonb(del) FROM del;

-- name: ListDeviceSources :many
-- Mit der ID des Zugangs-Elements (genau eines pro Quelle).
SELECT sqlc.embed(s), a.id AS access_element_id
FROM device_source s
JOIN element a ON a.source_id = s.id AND a.kind = 'access'
ORDER BY s.id;

-- name: AccessElementID :one
SELECT id FROM element WHERE source_id = @source_id::uuid AND kind = 'access';

-- name: SetAccessGlobalRef :execrows
-- global_ref des Zugangs-Elements: die Identität des Geräts aus Sicht dieser Quelle.
UPDATE element
SET global_ref = @global_ref, updated_at = now()
WHERE source_id = @source_id::uuid AND kind = 'access'
  AND global_ref IS DISTINCT FROM @global_ref;
