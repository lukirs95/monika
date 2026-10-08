-- name: CreateDevice :exec
-- Ein Gerät ist ein Element mit kind = device; device_id verweist auf sich selbst.
INSERT INTO element (id, kind, device_id, label, notes)
VALUES (@id, 'device', @id, @label, @notes);

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
  RETURNING id, device_id, driver
)
INSERT INTO element (id, kind, device_id, parent_id, source_id, name)
SELECT @access_id::uuid, 'access', src.device_id, src.device_id, src.id, src.driver
FROM src;

-- name: UpdateDeviceSource :execrows
-- Treiber und Gerät einer Quelle stehen fest; sonst würden ihre Elemente den Besitzer wechseln.
-- Quellen gelöschter Geräte bleiben unverändert (0 Zeilen).
UPDATE device_source
SET address    = @address,
    config     = @config,
    secret_id  = @secret_id,
    enabled    = @enabled,
    updated_at = now()
WHERE device_source.id = @id
  AND device_source.device_id IN (SELECT e.id FROM element e WHERE e.kind = 'device' AND e.deleted_at IS NULL);

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
