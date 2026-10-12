-- name: ListRoles :many
SELECT * FROM role ORDER BY name, id;

-- name: ListGrants :many
SELECT * FROM permission_grant ORDER BY role_id, id;

-- name: ListRoleMembers :many
SELECT * FROM role_member ORDER BY role_id, user_id;

-- name: ListUsers :many
-- Mit TOTP eingerichtet (bestätigt), für die Verwaltung.
SELECT sqlc.embed(app_user), (user_totp.confirmed_at IS NOT NULL)::boolean AS totp_enabled
FROM app_user LEFT JOIN user_totp ON user_totp.user_id = app_user.id
ORDER BY app_user.username;

-- name: CountUsers :one
SELECT count(*) FROM app_user;

-- name: GetUserByName :one
SELECT * FROM app_user WHERE username = @username;

-- name: GetUserByID :one
SELECT * FROM app_user WHERE id = @id;

-- name: CreateUser :exec
-- Audit ohne password_hash.
WITH u AS (
  INSERT INTO app_user (id, username, display_name, email, password_hash, is_service)
  VALUES (@id::uuid, @username::text, @display_name, @email, @password_hash, @is_service::boolean)
  RETURNING *
)
INSERT INTO audit_log (actor_id, verb, object_type, object_id, after)
SELECT sqlc.narg(actor_id)::uuid, 'create', 'app_user', u.id, to_jsonb(u) - 'password_hash' FROM u;

-- name: UpdateUser :execrows
-- Deaktivieren beendet alle Sitzungen im selben Statement. Audit ohne password_hash.
WITH before AS (
  SELECT * FROM app_user WHERE id = @id::uuid
), u AS (
  UPDATE app_user
  SET display_name = @display_name,
      email        = @email,
      disabled     = @disabled::boolean
  WHERE app_user.id = @id::uuid
  RETURNING *
), sessions AS (
  UPDATE user_session
  SET revoked_at = now()
  WHERE user_id = @id::uuid AND revoked_at IS NULL AND @disabled::boolean
)
INSERT INTO audit_log (actor_id, verb, object_type, object_id, before, after)
SELECT sqlc.narg(actor_id)::uuid, 'update', 'app_user', u.id,
       to_jsonb(before) - 'password_hash', to_jsonb(u) - 'password_hash'
FROM u JOIN before ON before.id = u.id;

-- name: SetPasswordHash :execrows
-- Beendet alle Sitzungen des Benutzers außer except_session (bei eigener Änderung die aktuelle).
-- Audit vermerkt nur, dass sich das Passwort geändert hat.
WITH u AS (
  UPDATE app_user SET password_hash = @password_hash::text
  WHERE app_user.id = @id::uuid
  RETURNING app_user.id
), sessions AS (
  UPDATE user_session
  SET revoked_at = now()
  WHERE user_id = @id::uuid AND revoked_at IS NULL
    AND id IS DISTINCT FROM sqlc.narg(except_session)::uuid
)
INSERT INTO audit_log (actor_id, verb, object_type, object_id, after)
SELECT sqlc.narg(actor_id)::uuid, 'update', 'app_user', u.id, '{"password": "changed"}'::jsonb FROM u;

-- name: RecordLogin :exec
-- Audit login bzw. login_failed; bei Erfolg auch last_login_at. user_id NULL = unbekannter Name.
WITH u AS (
  UPDATE app_user SET last_login_at = now()
  WHERE app_user.id = sqlc.narg(user_id)::uuid AND @success::boolean
)
INSERT INTO audit_log (actor_id, verb, object_type, object_id, after)
VALUES (
  CASE WHEN @success::boolean THEN sqlc.narg(user_id)::uuid END,
  CASE WHEN @success::boolean THEN 'login' ELSE 'login_failed' END,
  'app_user', sqlc.narg(user_id)::uuid,
  jsonb_build_object('username', @username::text, 'remote_addr', sqlc.narg(remote_addr)::text, 'reason', sqlc.narg(reason)::text)
);

-- name: CreateRole :exec
WITH r AS (
  INSERT INTO role (id, name, description, external_ref)
  VALUES (@id::uuid, @name::text, @description, @external_ref)
  RETURNING *
)
INSERT INTO audit_log (actor_id, verb, object_type, object_id, after)
SELECT sqlc.narg(actor_id)::uuid, 'create', 'role', r.id, to_jsonb(r) FROM r;

-- name: UpdateRole :execrows
WITH before AS (
  SELECT * FROM role WHERE id = @id::uuid
), r AS (
  UPDATE role
  SET name = @name::text, description = @description, external_ref = @external_ref
  WHERE role.id = @id::uuid
  RETURNING *
)
INSERT INTO audit_log (actor_id, verb, object_type, object_id, before, after)
SELECT sqlc.narg(actor_id)::uuid, 'update', 'role', r.id, to_jsonb(before), to_jsonb(r)
FROM r JOIN before ON before.id = r.id;

-- name: DeleteRole :execrows
-- Rechte und Mitgliedschaften entfallen (CASCADE).
WITH r AS (
  DELETE FROM role WHERE role.id = @id::uuid RETURNING *
)
INSERT INTO audit_log (actor_id, verb, object_type, object_id, before)
SELECT sqlc.narg(actor_id)::uuid, 'delete', 'role', r.id, to_jsonb(r) FROM r;

-- name: RoleSnapshot :one
-- Rolle mit Rechten und Mitgliedern als JSON, für Vorher/Nachher im Audit.
SELECT jsonb_build_object(
  'role', to_jsonb(r),
  'grants', COALESCE((SELECT jsonb_agg(jsonb_build_object('permission', g.permission, 'scope_element_id', g.scope_element_id)
                      ORDER BY g.permission, g.scope_element_id) FROM permission_grant g WHERE g.role_id = r.id), '[]'::jsonb),
  'members', COALESCE((SELECT jsonb_agg(jsonb_build_object('user_id', m.user_id, 'origin', m.origin)
                       ORDER BY m.user_id) FROM role_member m WHERE m.role_id = r.id), '[]'::jsonb)
)::jsonb AS snapshot
FROM role r
WHERE r.id = @id;

-- name: DeleteGrants :exec
DELETE FROM permission_grant WHERE role_id = @role_id;

-- name: InsertGrant :batchexec
INSERT INTO permission_grant (role_id, permission, scope_element_id)
VALUES (@role_id, @permission, @scope_element_id);

-- name: DeleteManualMembersExcept :exec
-- Mitgliedschaften aus dem IdP (origin = idp) bleiben unberührt.
DELETE FROM role_member
WHERE role_id = @role_id AND origin = 'manual'
  AND NOT (user_id = ANY (COALESCE(@keep_user_ids::uuid[], '{}')));

-- name: InsertManualMember :batchexec
INSERT INTO role_member (role_id, user_id, origin)
VALUES (@role_id, @user_id, 'manual')
ON CONFLICT (role_id, user_id) DO NOTHING;
