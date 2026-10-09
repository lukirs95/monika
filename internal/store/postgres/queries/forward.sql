-- name: ListForwardTargets :many
SELECT * FROM forward_target
ORDER BY name, id;

-- name: CreateForwardTarget :exec
WITH t AS (
  INSERT INTO forward_target (id, name, kind, enabled, config, secret_id, events, min_severity, scope_element_id)
  VALUES (@id::uuid, @name::text, @kind::forward_kind, @enabled::boolean, @config::jsonb, @secret_id,
          @events::text[], @min_severity, @scope_element_id)
  RETURNING *
)
INSERT INTO audit_log (actor_id, verb, object_type, object_id, after)
SELECT sqlc.narg(actor_id)::uuid, 'create', 'forward_target', t.id, to_jsonb(t) FROM t;

-- name: UpdateForwardTarget :execrows
WITH before AS (
  SELECT * FROM forward_target WHERE id = @id::uuid
), t AS (
  UPDATE forward_target
  SET name             = @name::text,
      kind             = @kind::forward_kind,
      enabled          = @enabled::boolean,
      config           = @config::jsonb,
      secret_id        = @secret_id,
      events           = @events::text[],
      min_severity     = @min_severity,
      scope_element_id = @scope_element_id,
      updated_at       = now()
  WHERE forward_target.id = @id::uuid
  RETURNING *
)
INSERT INTO audit_log (actor_id, verb, object_type, object_id, before, after)
SELECT sqlc.narg(actor_id)::uuid, 'update', 'forward_target', t.id, to_jsonb(before), to_jsonb(t)
FROM t JOIN before ON before.id = t.id;

-- name: DeleteForwardTarget :execrows
WITH t AS (
  DELETE FROM forward_target WHERE forward_target.id = @id::uuid RETURNING *
)
INSERT INTO audit_log (actor_id, verb, object_type, object_id, before)
SELECT sqlc.narg(actor_id)::uuid, 'delete', 'forward_target', t.id, to_jsonb(t) FROM t;

-- name: SetForwardTargetResult :batchexec
-- Ergebnis des letzten Versands, gesammelt im Takt geschrieben. Erfolg setzt last_sent_at und
-- leert last_error; ein Fehler setzt nur last_error.
UPDATE forward_target
SET last_sent_at = CASE WHEN sqlc.narg(error)::text IS NULL THEN @at::timestamptz ELSE last_sent_at END,
    last_error   = sqlc.narg(error)::text
WHERE id = @id::uuid;
