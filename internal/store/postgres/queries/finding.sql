-- name: ListActiveFindings :many
-- Lädt die aktiven Befunde beim Start in den Speicher des Core.
SELECT * FROM finding
WHERE cleared_at IS NULL
ORDER BY raised_at, id;

-- name: RaiseFinding :exec
-- Schlägt mit 23505 (finding_active_uq) fehl, wenn derselbe Befund schon aktiv ist.
INSERT INTO finding (id, element_id, element_path, origin, rule_id, code, severity, message, details, caused_by_id, raised_at)
VALUES (@id, @element_id, @element_path, @origin, @rule_id, @code, @severity, @message, @details, @caused_by_id,
        COALESCE(sqlc.narg(raised_at)::timestamptz, now()));

-- name: EndFinding :execrows
-- Beendet einen aktiven Befund, behoben oder zurückgezogen; der Grund steht in details.cleared_reason.
UPDATE finding
SET cleared_at = now(),
    details    = COALESCE(details, '{}'::jsonb) || jsonb_build_object('cleared_reason', @reason::text)
WHERE id = @id AND cleared_at IS NULL;

-- name: SetFindingCause :execrows
-- caused_by_id NULL = Ursache, sonst Symptom des angegebenen Befunds.
UPDATE finding
SET caused_by_id = @caused_by_id
WHERE id = @id AND cleared_at IS NULL;

-- name: AckFinding :execrows
-- Quittiert einen aktiven Befund; die erste Quittierung bleibt bestehen.
UPDATE finding
SET acked_at    = COALESCE(acked_at, now()),
    acked_by_id = COALESCE(acked_by_id, @acked_by_id)
WHERE id = @id AND cleared_at IS NULL;
