-- name: DeferConstraints :exec
-- Verzögert Fremdschlüssel bis zum Ende der Transaktion, z. B. damit Eltern und Kinder in
-- beliebiger Reihenfolge angelegt werden können.
SET CONSTRAINTS ALL DEFERRED;

-- name: InsertRun :batchexec
INSERT INTO action_run (
  id, parent_id, preset_id, preset_step_id, element_id, element_path, source_id, action, params,
  status, critical, verify, requested_by_id, requested_at
) VALUES (
  @id, @parent_id, @preset_id, @preset_step_id, @element_id, @element_path, @source_id, @action, @params,
  @status, @critical, @verify, @requested_by_id, @requested_at
);

-- name: UpdateRun :execrows
-- Ändert, was sich im Lebenszyklus eines Laufs ändert; Ziel, Aktion, critical und verify bleiben.
UPDATE action_run
SET status       = @status,
    source_id    = @source_id,
    response     = @response,
    sent_at      = @sent_at,
    responded_at = @responded_at,
    finished_at  = @finished_at
WHERE id = @id;

-- name: FinishInterruptedRuns :execrows
-- Beim Start: Läufe, die ein Neustart unterbrochen hat, abschließen, ohne etwas zu senden.
-- pending und sent werden failed, accepted wird unconfirmed; der Grund steht in response.
UPDATE action_run
SET status      = CASE WHEN status = 'accepted' THEN 'unconfirmed'::action_status ELSE 'failed'::action_status END,
    finished_at = now(),
    response    = COALESCE(response, '{}'::jsonb) || jsonb_build_object('reason', @reason::text)
WHERE finished_at IS NULL
  AND status IN ('pending', 'sent', 'accepted');
