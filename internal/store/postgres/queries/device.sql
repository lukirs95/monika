-- name: CreateDevice :exec
-- Legt Element und Geräte-Zeile in einem Statement an. element.device_id und
-- device.element_id verweisen aufeinander; die Fremdschlüssel werden erst am
-- Ende des Statements geprüft.
WITH e AS (
  INSERT INTO element (id, kind, device_id, label, notes)
  VALUES (@id, 'device', @id, @label, @notes)
  RETURNING id
)
INSERT INTO device (element_id)
SELECT id FROM e;

-- name: UpdateDeviceIdentity :execrows
-- Identität des Geräts, wie eine Quelle sie meldet.
WITH e AS (
  UPDATE element
  SET name          = @name,
      present       = true,
      first_seen_at = COALESCE(first_seen_at, now()),
      last_seen_at  = now(),
      updated_at    = now()
  WHERE id = @element_id AND kind = 'device'
)
UPDATE device
SET vendor   = @vendor,
    model    = @model,
    serial   = @serial,
    firmware = @firmware
WHERE element_id = @element_id;

-- name: ListDevices :many
SELECT * FROM device
ORDER BY element_id;

-- name: CreateDeviceSource :exec
INSERT INTO device_source (id, device_id, driver, address, config, secret_id, enabled)
VALUES (@id, @device_id, @driver, @address, @config, @secret_id, @enabled);

-- name: UpdateDeviceSource :execrows
-- Treiber und Gerät einer Quelle stehen fest; sonst würden ihre Elemente den Besitzer wechseln.
-- Quellen gelöschter Geräte bleiben unverändert (0 Zeilen).
UPDATE device_source
SET address    = @address,
    config     = @config,
    secret_id  = @secret_id,
    enabled    = @enabled,
    updated_at = now()
WHERE id = @id
  AND device_id IN (SELECT element_id FROM device WHERE deleted_at IS NULL);

-- name: LockDevice :one
-- Sperrt die Geräte-Zeile bis zum Ende der Transaktion; liefert deleted_at.
SELECT deleted_at
FROM device
WHERE element_id = @element_id
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
-- Weiches Löschen: Quellen deaktivieren, Elemente nicht mehr überwachen, aus allen Gruppen
-- entfernen. Die Zeilen bleiben, damit Läufe, Befunde und Audit lesbar bleiben.
-- Nur über postgres.DeleteDevice aufrufen (Sperre und Prüfung der Presets).
WITH sources AS (
  UPDATE device_source
  SET enabled = false, updated_at = now()
  WHERE device_id = @device_id::uuid
), elements AS (
  UPDATE element
  SET monitored = false, updated_at = now()
  WHERE device_id = @device_id::uuid
), memberships AS (
  DELETE FROM element_link l
  USING element e
  WHERE l.type = 'member' AND l.to_id = e.id AND e.device_id = @device_id::uuid
)
UPDATE device
SET deleted_at = now()
WHERE element_id = @device_id::uuid;

-- name: ListDeviceSources :many
SELECT * FROM device_source
ORDER BY id;
