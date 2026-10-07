-- Secret-Store (docs/ARCHITECTURE.md, Abschnitt 14).
-- Werte werden nur in Go ver- und entschlüsselt; Abfragen für die Anzeige liefern nie ciphertext.

-- name: CreateSecret :exec
INSERT INTO secret (id, name, description, ciphertext, nonce, key_id, created_by_id, updated_by_id)
VALUES (@id, @name, @description, @ciphertext, @nonce, @key_id, @created_by_id, @created_by_id);

-- name: GetSecretValue :one
SELECT id, ciphertext, nonce, key_id
FROM secret
WHERE id = @id;

-- name: ListSecrets :many
SELECT id, name, description, created_at, updated_at
FROM secret
ORDER BY name;

-- name: ListSecretValues :many
-- Für Startprüfung und Schlüsselrotation.
SELECT id, ciphertext, nonce, key_id
FROM secret
ORDER BY id;

-- name: ReplaceSecretValue :execrows
UPDATE secret
SET ciphertext    = @ciphertext,
    nonce         = @nonce,
    key_id        = @key_id,
    updated_by_id = @updated_by_id,
    updated_at    = now()
WHERE id = @id;

-- name: ReencryptSecret :execrows
-- Schlüsselrotation durch Monika selbst: Wert unverändert, daher kein updated_by/updated_at.
UPDATE secret
SET ciphertext = @ciphertext,
    nonce      = @nonce,
    key_id     = @key_id
WHERE id = @id;

-- name: DeleteSecret :execrows
-- Schlägt mit 23001 (restrict_violation) fehl, solange eine Quelle oder Weiterleitung das Secret verwendet.
DELETE FROM secret
WHERE id = @id;
