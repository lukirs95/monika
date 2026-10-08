-- name: CreateSecret :exec
-- Audit nur mit Name und Beschreibung, nie mit dem Wert.
WITH s AS (
  INSERT INTO secret (id, name, description, ciphertext, nonce, key_id, created_by_id, updated_by_id)
  VALUES (@id::uuid, @name::text, @description, @ciphertext::bytea, @nonce::bytea, @key_id::text,
          sqlc.narg(actor_id)::uuid, sqlc.narg(actor_id)::uuid)
  RETURNING id, name, description
)
INSERT INTO audit_log (actor_id, verb, object_type, object_id, after)
SELECT sqlc.narg(actor_id)::uuid, 'create', 'secret', s.id, to_jsonb(s) FROM s;

-- name: GetSecretValue :one
SELECT id, ciphertext, nonce, key_id
FROM secret
WHERE id = @id;

-- name: ListSecrets :many
-- Für die Anzeige: liefert nie den verschlüsselten Wert.
SELECT id, name, description, created_at, updated_at
FROM secret
ORDER BY name;

-- name: ListSecretValues :many
-- Für Startprüfung und Schlüsselrotation.
SELECT id, ciphertext, nonce, key_id
FROM secret
ORDER BY id;

-- name: ReplaceSecretValue :execrows
-- Audit vermerkt nur, dass der Wert ersetzt wurde.
WITH s AS (
  UPDATE secret
  SET ciphertext    = @ciphertext,
      nonce         = @nonce,
      key_id        = @key_id,
      updated_by_id = sqlc.narg(actor_id)::uuid,
      updated_at    = now()
  WHERE secret.id = @id::uuid
  RETURNING secret.id, secret.name
)
INSERT INTO audit_log (actor_id, verb, object_type, object_id, after)
SELECT sqlc.narg(actor_id)::uuid, 'update', 'secret', s.id,
       jsonb_build_object('name', s.name, 'value', 'replaced')
FROM s;

-- name: ReencryptSecret :execrows
-- Schlüsselrotation durch Monika selbst: Wert unverändert, daher kein updated_by/updated_at.
UPDATE secret
SET ciphertext = @ciphertext,
    nonce      = @nonce,
    key_id     = @key_id
WHERE id = @id;

-- name: DeleteSecret :execrows
-- Schlägt mit 23001 (restrict_violation) fehl, solange eine Quelle oder Weiterleitung das Secret verwendet.
WITH s AS (
  DELETE FROM secret
  WHERE secret.id = @id::uuid
  RETURNING secret.id, secret.name, secret.description
)
INSERT INTO audit_log (actor_id, verb, object_type, object_id, before)
SELECT sqlc.narg(actor_id)::uuid, 'delete', 'secret', s.id, to_jsonb(s) FROM s;
