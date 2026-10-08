-- name: ListElements :many
-- Lädt das Inventar beim Start in den Speicher des Cores.
SELECT * FROM element
ORDER BY id;

-- name: UpsertDriverElement :batchone
-- Discovery: legt ein Treiber-Element an oder aktualisiert es anhand von (source_id, external_id).
-- Übernimmt vom User vorab angelegte Elemente (first_seen_at = NULL) und behält deren id.
-- Eine leere external_id meint das Zugangs-Element der Quelle (gespeichert als NULL).
INSERT INTO element (
  id, kind, parent_id, device_id, source_id, external_id, global_ref,
  transport, direction, name, attributes, position, actions, critical_actions, settings,
  present, first_seen_at, last_seen_at
) VALUES (
  @id, @kind, @parent_id::uuid, @device_id::uuid, @source_id::uuid, NULLIF(@external_id::text, ''), @global_ref,
  @transport, @direction, @name, @attributes, @position, @actions, @critical_actions, @settings,
  true, now(), now()
)
ON CONFLICT (source_id, external_id) WHERE source_id IS NOT NULL DO UPDATE
SET kind          = excluded.kind,
    parent_id     = excluded.parent_id,
    global_ref    = excluded.global_ref,
    transport     = excluded.transport,
    direction     = excluded.direction,
    name          = excluded.name,
    attributes    = excluded.attributes,
    position      = excluded.position,
    actions       = excluded.actions,
    critical_actions = excluded.critical_actions,
    settings      = excluded.settings,
    present       = true,
    first_seen_at = COALESCE(element.first_seen_at, now()),
    last_seen_at  = now(),
    updated_at    = now()
RETURNING id;

-- name: FinishDiscovery :many
-- Abschluss einer Discovery, ein Statement pro Lauf und Quelle: gemeldete Elemente sind
-- gesehen (last_seen_at), alle übrigen der Quelle verschwinden (present = false).
-- Das Zugangs-Element (external_id NULL) gilt immer als gesehen; sein last_seen_at zeigt
-- die letzte vollständige Discovery.
-- Elemente werden nie gelöscht; kommen sie zurück, sind Label und Gruppen wieder da.
-- Liefert die IDs der in diesem Lauf verschwundenen Elemente.
WITH seen AS (
  SELECT e.id, (e.external_id IS NULL OR e.id = ANY (COALESCE(@seen_ids::uuid[], '{}'))) AS seen
  FROM element e
  WHERE e.source_id = @source_id::uuid
), updated AS (
  UPDATE element
  SET present       = seen.seen,
      first_seen_at = CASE WHEN element.external_id IS NULL THEN COALESCE(first_seen_at, now()) ELSE first_seen_at END,
      last_seen_at  = CASE WHEN seen.seen THEN now() ELSE last_seen_at END,
      updated_at    = CASE WHEN seen.seen THEN updated_at ELSE now() END
  FROM seen
  WHERE element.id = seen.id
    AND (element.present OR seen.seen)
  RETURNING element.id, element.present
)
SELECT id FROM updated
WHERE NOT present;

-- name: CreatePlannedElement :exec
-- Vom User vorab angelegtes Treiber-Element, das der Treiber noch nie gesehen hat.
INSERT INTO element (id, kind, parent_id, device_id, source_id, external_id, label, notes, present)
VALUES (@id, @kind, @parent_id::uuid, @device_id::uuid, @source_id::uuid, @external_id::text, @label, @notes, false);

-- name: CreateGroup :exec
INSERT INTO element (id, kind, label, notes)
VALUES (@id, 'group', @label, @notes);

-- name: UpdateElementUserFields :execrows
UPDATE element
SET label      = @label,
    notes      = @notes,
    monitored  = @monitored,
    updated_at = now()
WHERE id = @id;

-- name: LockGroup :one
-- Sperrt das Gruppen-Element bis zum Ende der Transaktion; liefert deleted_at.
SELECT deleted_at
FROM element
WHERE id = @id AND kind = 'group'
FOR UPDATE;

-- name: ListPresetsUsingGroup :many
-- Presets mit Schritten, die auf die Gruppe zielen; sie blockieren das Löschen.
SELECT DISTINCT p.id, p.name
FROM preset p
JOIN preset_step s ON s.preset_id = p.id
WHERE s.element_id = @group_id::uuid
ORDER BY p.name, p.id;

-- name: WithdrawGroupRuleFindings :execrows
-- Zieht die aktiven Befunde der Regeln mit dem Geltungsbereich der Gruppe zurück
-- (cleared_reason rule_deleted). Diese Befunde hängen an den Mitgliedern.
UPDATE finding
SET cleared_at = now(),
    details    = COALESCE(details, '{}'::jsonb) || '{"cleared_reason": "rule_deleted"}'::jsonb
WHERE cleared_at IS NULL
  AND rule_id IN (SELECT r.id FROM rule r WHERE r.scope_element_id = @group_id::uuid);

-- name: DeleteGroup :exec
-- Weiches Löschen: Regeln, Rechte und Weiterleitungen mit dem Geltungsbereich der Gruppe und
-- alle Mitgliedschaften in beide Richtungen entfernen. Vorher die Befunde zurückziehen.
-- Nur über postgres.Store.DeleteGroup aufrufen (Sperre und Prüfung der Presets).
WITH rules AS (
  DELETE FROM rule WHERE scope_element_id = @group_id::uuid
), grants AS (
  DELETE FROM permission_grant WHERE scope_element_id = @group_id::uuid
), forwards AS (
  DELETE FROM forward_target WHERE scope_element_id = @group_id::uuid
), memberships AS (
  DELETE FROM element_link
  WHERE type = 'member' AND (from_id = @group_id::uuid OR to_id = @group_id::uuid)
)
UPDATE element
SET deleted_at = now(), updated_at = now()
WHERE id = @group_id::uuid;
