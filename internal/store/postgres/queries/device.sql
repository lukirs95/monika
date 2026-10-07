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
UPDATE device_source
SET address    = @address,
    config     = @config,
    secret_id  = @secret_id,
    enabled    = @enabled,
    updated_at = now()
WHERE id = @id;

-- name: ListDeviceSources :many
SELECT * FROM device_source
ORDER BY id;
