-- name: ListRules :many
-- Lädt alle Regeln beim Start in den Speicher des Core.
SELECT * FROM rule
ORDER BY id;

-- name: CreateRule :exec
WITH r AS (
  INSERT INTO rule (
    id, name, enabled, severity, scope_element_id, match_kind, match_transport, match_driver,
    fact, op, value, when_fact, when_op, when_value, clear_value, hold_seconds, clear_hold_seconds
  ) VALUES (
    @id::uuid, @name::text, @enabled::boolean, @severity::severity, @scope_element_id, @match_kind, @match_transport, @match_driver,
    @fact::text, @op::rule_op, @value::jsonb, @when_fact, @when_op, @when_value, @clear_value, @hold_seconds::int, @clear_hold_seconds::int
  )
  RETURNING *
)
INSERT INTO audit_log (actor_id, verb, object_type, object_id, after)
SELECT sqlc.narg(actor_id)::uuid, 'create', 'rule', r.id, to_jsonb(r) FROM r;

-- name: UpdateRule :execrows
WITH before AS (
  SELECT * FROM rule WHERE id = @id::uuid
), r AS (
  UPDATE rule
  SET name             = @name::text,
      enabled          = @enabled::boolean,
      severity         = @severity::severity,
      scope_element_id = @scope_element_id,
      match_kind       = @match_kind,
      match_transport  = @match_transport,
      match_driver     = @match_driver,
      fact             = @fact::text,
      op               = @op::rule_op,
      value            = @value::jsonb,
      when_fact        = @when_fact,
      when_op          = @when_op,
      when_value       = @when_value,
      clear_value      = @clear_value,
      hold_seconds     = @hold_seconds::int,
      clear_hold_seconds = @clear_hold_seconds::int,
      updated_at       = now()
  WHERE rule.id = @id::uuid
  RETURNING *
)
INSERT INTO audit_log (actor_id, verb, object_type, object_id, before, after)
SELECT sqlc.narg(actor_id)::uuid, 'update', 'rule', r.id, to_jsonb(before), to_jsonb(r)
FROM r JOIN before ON before.id = r.id;

-- name: WithdrawRuleFindings :execrows
-- Zieht alle aktiven Befunde einer Regel zurück (Regel gelöscht oder deaktiviert).
UPDATE finding
SET cleared_at = now(),
    details    = COALESCE(details, '{}'::jsonb) || jsonb_build_object('cleared_reason', @reason::text)
WHERE cleared_at IS NULL
  AND rule_id = @rule_id::uuid;

-- name: DeleteRule :execrows
-- Vorher WithdrawRuleFindings aufrufen; finding.rule_id wird NULL, die Befunde bleiben.
WITH r AS (
  DELETE FROM rule WHERE rule.id = @id::uuid RETURNING *
)
INSERT INTO audit_log (actor_id, verb, object_type, object_id, before)
SELECT sqlc.narg(actor_id)::uuid, 'delete', 'rule', r.id, to_jsonb(r) FROM r;
