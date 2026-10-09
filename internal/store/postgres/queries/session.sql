-- name: CreateSession :exec
INSERT INTO user_session (id, user_id, kind, token_id, token_hash, expires_at, user_agent, remote_addr)
VALUES (@id, @user_id, @kind, @token_id, @token_hash, @expires_at, @user_agent, @remote_addr);

-- name: GetSessionByHash :one
-- Sitzung mit Benutzer und, bei Kiosk-Sitzungen, ihrem Token; gültig prüft auth.Service.
SELECT sqlc.embed(s), sqlc.embed(u),
       t.revoked_at AS token_revoked_at, t.expires_at AS token_expires_at, t.read_only AS token_read_only
FROM user_session s
JOIN app_user u ON u.id = s.user_id
LEFT JOIN api_token t ON t.id = s.token_id
WHERE s.token_hash = @token_hash;

-- name: ListActiveSessions :many
SELECT * FROM user_session
WHERE user_id = @user_id AND revoked_at IS NULL AND expires_at > now()
ORDER BY created_at DESC;

-- name: TouchSession :batchexec
UPDATE user_session SET last_seen_at = @at
WHERE id = @id AND last_seen_at < @at;

-- name: RevokeSession :execrows
UPDATE user_session SET revoked_at = now()
WHERE id = @id AND user_id = @user_id AND revoked_at IS NULL;

-- name: CreateToken :exec
-- Audit ohne token_hash.
WITH t AS (
  INSERT INTO api_token (id, user_id, name, token_hash, read_only, expires_at)
  VALUES (@id::uuid, @user_id::uuid, @name::text, @token_hash::text, @read_only::boolean, @expires_at)
  RETURNING *
)
INSERT INTO audit_log (actor_id, verb, object_type, object_id, after)
SELECT sqlc.narg(actor_id)::uuid, 'create', 'api_token', t.id, to_jsonb(t) - 'token_hash' FROM t;

-- name: GetTokenByHash :one
SELECT sqlc.embed(t), sqlc.embed(u)
FROM api_token t
JOIN app_user u ON u.id = t.user_id
WHERE t.token_hash = @token_hash;

-- name: ListTokens :many
SELECT * FROM api_token
WHERE user_id = @user_id
ORDER BY created_at DESC;

-- name: TouchToken :batchexec
UPDATE api_token SET last_used_at = @at
WHERE id = @id AND (last_used_at IS NULL OR last_used_at < @at);

-- name: RevokeToken :execrows
-- Beendet auch alle Kiosk-Sitzungen dieses Tokens. Audit ohne token_hash.
WITH t AS (
  UPDATE api_token SET revoked_at = now()
  WHERE api_token.id = @id::uuid AND revoked_at IS NULL
  RETURNING *
), sessions AS (
  UPDATE user_session SET revoked_at = now()
  WHERE token_id = @id::uuid AND revoked_at IS NULL
)
INSERT INTO audit_log (actor_id, verb, object_type, object_id, after)
SELECT sqlc.narg(actor_id)::uuid, 'update', 'api_token', t.id, to_jsonb(t) - 'token_hash' FROM t;
