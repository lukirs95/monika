-- name: InsertAuditEntry :exec
-- Für Änderungen über mehrere Statements (z. B. Preset mit Schritten): am Ende derselben
-- Transaktion schreiben. Sonst steht der Audit-Eintrag im Statement der Änderung.
INSERT INTO audit_log (actor_id, verb, object_type, object_id, before, after)
VALUES (sqlc.narg(actor_id)::uuid, @verb::text, @object_type::text, @object_id::uuid, @before, @after);
