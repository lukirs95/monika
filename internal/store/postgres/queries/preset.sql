-- name: ListPresets :many
-- Presets ohne Schritte, z. B. für die Auswahl in der UI.
SELECT id, name, description, created_by_id
FROM preset
ORDER BY name, id;

-- name: GetPreset :one
SELECT id, name, description, created_by_id
FROM preset
WHERE id = @id;

-- name: ListPresetSteps :many
SELECT * FROM preset_step
WHERE preset_id = @preset_id
ORDER BY stage, position, id;

-- name: PresetSnapshot :one
-- Preset mit Schritten als JSON, für Vorher/Nachher im Audit.
SELECT jsonb_build_object(
  'preset', to_jsonb(p),
  'steps', COALESCE((SELECT jsonb_agg(to_jsonb(s) ORDER BY s.stage, s.position, s.id)
                     FROM preset_step s WHERE s.preset_id = p.id), '[]'::jsonb)
)::jsonb AS snapshot
FROM preset p
WHERE p.id = @id;

-- name: LockStepTargets :many
-- Sperrt die Ziel-Elemente und ihre Geräte (FOR SHARE, in fester Reihenfolge) gegen
-- gleichzeitiges Löschen und liefert die vorhandenen. DeleteDevice und DeleteGroup sperren
-- Gerät bzw. Gruppe FOR UPDATE; wer zuerst sperrt, sieht danach das Ergebnis des anderen.
WITH t AS (
  SELECT e.id, e.device_id FROM element e WHERE e.id = ANY (@ids::uuid[])
)
SELECT e.id
FROM element e
WHERE e.id IN (SELECT t.id FROM t UNION SELECT t.device_id FROM t WHERE t.device_id IS NOT NULL)
ORDER BY e.id
FOR SHARE;

-- name: LockStepSources :exec
-- Sperrt die Quellen der Ziel-Elemente (FOR SHARE) gegen gleichzeitiges DeleteSource.
SELECT s.id
FROM device_source s
WHERE s.id IN (SELECT e.source_id FROM element e WHERE e.id = ANY (@ids::uuid[]))
ORDER BY s.id
FOR SHARE;

-- name: CreatePresetRow :exec
INSERT INTO preset (id, name, description, created_by_id)
VALUES (@id, @name, @description, sqlc.narg(actor_id)::uuid);

-- name: UpdatePresetRow :execrows
UPDATE preset
SET name        = @name,
    description = @description,
    updated_at  = now()
WHERE id = @id;

-- name: DeleteRemovedPresetSteps :exec
-- Entfernt Schritte, die nicht mehr im Preset stehen; die übrigen behalten ihre IDs, damit
-- vergangene Läufe ihren Schritt nicht verlieren.
DELETE FROM preset_step
WHERE preset_id = @preset_id
  AND NOT (id = ANY (COALESCE(@keep_ids::uuid[], '{}')));

-- name: UpsertPresetStep :batchone
-- Ein Schritt wechselt nie das Preset: Gehört die ID zu einem anderen, kommt keine Zeile zurück.
INSERT INTO preset_step (id, preset_id, stage, position, delay_ms, element_id, action, params, continue_on_error, driver)
VALUES (@id, @preset_id, @stage, @position, @delay_ms, @element_id, @action, @params, @continue_on_error, @driver)
ON CONFLICT (id) DO UPDATE
SET stage             = excluded.stage,
    position          = excluded.position,
    delay_ms          = excluded.delay_ms,
    element_id        = excluded.element_id,
    action            = excluded.action,
    params            = excluded.params,
    continue_on_error = excluded.continue_on_error,
    driver            = excluded.driver
WHERE preset_step.preset_id = excluded.preset_id
RETURNING id;

-- name: DeletePresetRow :execrows
-- Schritte werden mitgelöscht (CASCADE), vergangene Läufe behalten preset_id NULL.
DELETE FROM preset
WHERE id = @id;
