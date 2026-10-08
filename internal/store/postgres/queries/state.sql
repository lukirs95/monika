-- name: ListElementHealth :many
-- Lädt Health und Rollup beim Start, damit der Core danach nur Änderungen schreibt.
SELECT element_id, health, rollup
FROM element_state
ORDER BY element_id;

-- name: UpsertElementHealth :batchexec
-- Schreibt Health und Rollup eines Elements. Ohne echte Änderung bleibt die Zeile unberührt,
-- auch updated_at.
INSERT INTO element_state (element_id, health, rollup, updated_at)
VALUES (@element_id, @health, @rollup, now())
ON CONFLICT (element_id) DO UPDATE
SET health     = excluded.health,
    rollup     = excluded.rollup,
    updated_at = now()
WHERE element_state.health IS DISTINCT FROM excluded.health
   OR element_state.rollup IS DISTINCT FROM excluded.rollup;
