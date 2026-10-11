-- Aufbewahrung: löscht alten Verlauf in Batches (docs/contracts/services.md, Aufbewahrung).
-- Nur über postgres.Store für retention.Store aufrufen. Aktive Befunde und laufende Läufe nie.

-- name: PurgeFindings :execrows
-- Beendete Befunde; caused_by_id anderer Befunde wird per Fremdschlüssel NULL.
DELETE FROM finding
WHERE id IN (
  SELECT f.id FROM finding f
  WHERE f.cleared_at < @ended_before
  ORDER BY f.cleared_at
  LIMIT @batch
);

-- name: PurgeRuns :execrows
-- Abgeschlossene Lauf-Bäume über ihre Wurzel; die Nachfahren folgen per CASCADE. Eine Wurzel
-- endet erst, wenn alle ihre Kinder geendet haben. Zählt die gelöschten Wurzeln.
DELETE FROM action_run
WHERE id IN (
  SELECT r.id FROM action_run r
  WHERE r.parent_id IS NULL AND r.finished_at < @finished_before
  ORDER BY r.finished_at
  LIMIT @batch
);

-- name: PurgeAudit :execrows
DELETE FROM audit_log
WHERE id IN (
  SELECT a.id FROM audit_log a
  WHERE a.at < @before
  ORDER BY a.at
  LIMIT @batch
);

-- name: PurgeSessions :execrows
-- Abgelaufene, widerrufene und lange unbenutzte Sitzungen.
DELETE FROM user_session
WHERE expires_at < @before OR revoked_at < @before OR last_seen_at < @before;

-- name: PurgeRecoveryCodes :execrows
-- Benutzte Wiederherstellungscodes.
DELETE FROM user_recovery_code
WHERE used_at < @before;
