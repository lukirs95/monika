-- name: GetTOTP :one
SELECT * FROM user_totp WHERE user_id = @user_id;

-- name: SaveTOTP :execrows
-- Neues, noch unbestätigtes Geheimnis; ein bestätigtes wird nie überschrieben (0 Zeilen).
INSERT INTO user_totp (user_id, ciphertext, nonce, key_id)
VALUES (@user_id, @ciphertext, @nonce, @key_id)
ON CONFLICT (user_id) DO UPDATE
SET ciphertext = excluded.ciphertext,
    nonce      = excluded.nonce,
    key_id     = excluded.key_id,
    last_step  = NULL,
    created_at = now()
WHERE user_totp.confirmed_at IS NULL;

-- name: ConfirmTOTP :execrows
UPDATE user_totp
SET confirmed_at = now(), last_step = @step
WHERE user_id = @user_id AND confirmed_at IS NULL;

-- name: DeleteRecoveryCodes :exec
DELETE FROM user_recovery_code WHERE user_id = @user_id;

-- name: InsertRecoveryCode :batchexec
INSERT INTO user_recovery_code (user_id, code_hash) VALUES (@user_id, @code_hash);

-- name: UseTOTPStep :execrows
-- Ein Code gilt nur einmal: der Zeitschritt muss größer sein als der zuletzt verwendete.
UPDATE user_totp
SET last_step = @step
WHERE user_id = @user_id AND confirmed_at IS NOT NULL
  AND (last_step IS NULL OR last_step < @step);

-- name: UseRecoveryCode :execrows
UPDATE user_recovery_code
SET used_at = now()
WHERE user_id = @user_id AND code_hash = @code_hash AND used_at IS NULL;

-- name: DeleteTOTP :execrows
DELETE FROM user_totp WHERE user_id = @user_id;

-- name: ListTOTPValues :many
-- Für Startprüfung und Schlüsselrotation (secret.Vault).
SELECT user_id, ciphertext, nonce, key_id FROM user_totp ORDER BY user_id;

-- name: ReencryptTOTP :execrows
UPDATE user_totp
SET ciphertext = @ciphertext, nonce = @nonce, key_id = @key_id
WHERE user_id = @user_id;
