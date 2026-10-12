-- Rein lesende Queries für den Verlauf (docs/contracts/api.md, Filter im Verlauf). Neueste
-- zuerst über die UUIDv7-ID; leere Filter (NULL) filtern nicht.

-- name: ListAuditHistory :many
SELECT * FROM audit_log
WHERE (sqlc.narg(before)::uuid IS NULL OR id < sqlc.narg(before))
  AND (sqlc.narg(from_at)::timestamptz IS NULL OR at >= sqlc.narg(from_at))
  AND (sqlc.narg(to_at)::timestamptz IS NULL OR at < sqlc.narg(to_at))
  AND (sqlc.narg(object_type)::text IS NULL OR object_type = sqlc.narg(object_type))
  AND (sqlc.narg(object_id)::uuid IS NULL OR object_id = sqlc.narg(object_id))
  AND (sqlc.narg(actor_id)::uuid IS NULL OR actor_id = sqlc.narg(actor_id))
  AND (NOT @system_only::boolean OR actor_id IS NULL)
  AND (sqlc.narg(verb)::text IS NULL OR verb = sqlc.narg(verb))
ORDER BY id DESC
LIMIT @lim;

-- name: ListFindingHistory :many
SELECT * FROM finding
WHERE (sqlc.narg(before)::uuid IS NULL OR id < sqlc.narg(before))
  AND (sqlc.narg(from_at)::timestamptz IS NULL OR raised_at >= sqlc.narg(from_at))
  AND (sqlc.narg(to_at)::timestamptz IS NULL OR raised_at < sqlc.narg(to_at))
  AND (@all_visible::boolean OR element_id = ANY(@visible::uuid[]))
  AND (@any_element::boolean OR element_id = ANY(@elements::uuid[]))
  AND (sqlc.narg(code)::text IS NULL OR code = sqlc.narg(code))
  AND (sqlc.narg(severity)::text IS NULL OR severity::text = sqlc.narg(severity))
  AND (sqlc.narg(origin)::text IS NULL OR origin::text = sqlc.narg(origin))
  AND (sqlc.narg(rule_id)::uuid IS NULL OR rule_id = sqlc.narg(rule_id))
  AND (sqlc.narg(state)::text IS NULL
       OR (sqlc.narg(state) = 'active' AND cleared_at IS NULL)
       OR (sqlc.narg(state) = 'cleared' AND cleared_at IS NOT NULL AND details->>'cleared_reason' = 'resolved')
       OR (sqlc.narg(state) = 'withdrawn' AND cleared_at IS NOT NULL AND details->>'cleared_reason' IS DISTINCT FROM 'resolved'))
ORDER BY id DESC
LIMIT @lim;

-- name: ListRunHistory :many
-- Wurzel-Läufe von Presets haben kein Ziel; sie zählen nur mit @roots.
SELECT * FROM action_run
WHERE (sqlc.narg(before)::uuid IS NULL OR id < sqlc.narg(before))
  AND (sqlc.narg(from_at)::timestamptz IS NULL OR requested_at >= sqlc.narg(from_at))
  AND (sqlc.narg(to_at)::timestamptz IS NULL OR requested_at < sqlc.narg(to_at))
  AND CASE WHEN element_id IS NULL THEN @roots::boolean
           ELSE @all_visible::boolean OR element_id = ANY(@visible::uuid[]) END
  AND (@any_element::boolean OR element_id = ANY(@elements::uuid[]))
  AND (sqlc.narg(status)::text IS NULL OR status::text = sqlc.narg(status))
  AND (sqlc.narg(action)::text IS NULL OR action = sqlc.narg(action))
  AND (sqlc.narg(preset_id)::uuid IS NULL OR preset_id = sqlc.narg(preset_id))
  AND (sqlc.narg(requested_by)::uuid IS NULL OR requested_by_id = sqlc.narg(requested_by))
ORDER BY id DESC
LIMIT @lim;

-- name: GetRun :one
SELECT * FROM action_run WHERE id = @id;

-- name: ListChildRuns :many
SELECT * FROM action_run WHERE parent_id = @parent_id ORDER BY id;
