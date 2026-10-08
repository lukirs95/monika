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
-- Ist das Gerät unbekannt oder gelöscht, wird nichts angelegt (0 Zeilen).
WITH src AS (
  INSERT INTO device_source (id, device_id, driver, address, config, secret_id, enabled)
  SELECT @id::uuid, e.id, @driver::text, @address, @config::jsonb, @secret_id, @enabled::boolean
  FROM element e
  WHERE e.id = @device_id::uuid AND e.kind = 'device' AND e.deleted_at IS NULL
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
-- Quellen gelöschter Geräte bleiben unverändert (0 Zeilen).
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
    AND device_source.device_id IN (SELECT e.id FROM element e WHERE e.kind = 'device' AND e.deleted_at IS NULL)
  RETURNING *
)
INSERT INTO audit_log (actor_id, verb, object_type, object_id, before, after)
SELECT sqlc.narg(actor_id)::uuid, 'update', 'device_source', upd.id, to_jsonb(before), to_jsonb(upd)
FROM upd JOIN before ON before.id = upd.id;

-- name: LockDevice :one
-- Sperrt das Geräte-Element bis zum Ende der Transaktion; liefert deleted_at.
SELECT deleted_at
FROM element
WHERE id = @id AND kind = 'device'
FOR UPDATE;

-- name: ListPresetsUsingDevice :many
-- Presets mit Schritten, die auf ein Element des Geräts zielen; sie blockieren das Löschen.
SELECT DISTINCT p.id, p.name
FROM preset p
JOIN preset_step s ON s.preset_id = p.id
JOIN element e ON e.id = s.element_id
WHERE e.device_id = @device_id::uuid
ORDER BY p.name, p.id;

-- name: WithdrawDeviceFindings :execrows
-- Zieht alle aktiven Befunde an Elementen des Geräts zurück (cleared_reason device_deleted).
UPDATE finding
SET cleared_at = now(),
    details    = COALESCE(details, '{}'::jsonb) || '{"cleared_reason": "device_deleted"}'::jsonb
WHERE cleared_at IS NULL
  AND element_id IN (SELECT e.id FROM element e WHERE e.device_id = @device_id::uuid);

-- name: DeleteDeviceScopedConfig :exec
-- Löscht Regeln, Rechte und Weiterleitungen, deren Geltungsbereich das Gerät oder eines seiner
-- Elemente ist; ohne ihr Ziel sind sie bedeutungslos. Vorher die Befunde zurückziehen.
WITH device_elements AS (
  SELECT e.id FROM element e WHERE e.device_id = @device_id::uuid
), rules AS (
  DELETE FROM rule WHERE scope_element_id IN (SELECT id FROM device_elements)
), grants AS (
  DELETE FROM permission_grant WHERE scope_element_id IN (SELECT id FROM device_elements)
)
DELETE FROM forward_target WHERE scope_element_id IN (SELECT id FROM device_elements);

-- name: DeleteDevice :exec
-- Weiches Löschen: Quellen deaktivieren, alle Elemente des Geräts verschwinden (present = false),
-- das Gerät wird stillgelegt (monitored vererbt sich) und aus allen Gruppen entfernt.
-- Die Zeilen bleiben, damit Läufe, Befunde und Audit lesbar bleiben.
-- Nur über postgres.Store.DeleteDevice aufrufen (Sperre und Prüfung der Presets).
WITH sources AS (
  UPDATE device_source
  SET enabled = false, updated_at = now()
  WHERE device_id = @device_id::uuid
), memberships AS (
  DELETE FROM element_link l
  USING element e
  WHERE l.type = 'member' AND l.to_id = e.id AND e.device_id = @device_id::uuid
)
UPDATE element
SET present    = false,
    monitored  = CASE WHEN id = @device_id::uuid THEN false ELSE monitored END,
    deleted_at = CASE WHEN id = @device_id::uuid THEN now() ELSE deleted_at END,
    updated_at = now()
WHERE device_id = @device_id::uuid;

-- name: ListDeviceSources :many
SELECT * FROM device_source
ORDER BY id;
