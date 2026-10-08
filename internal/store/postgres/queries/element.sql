-- name: ListElements :many
-- Lädt das Inventar beim Start in den Speicher des Cores.
SELECT * FROM element
ORDER BY id;

-- name: UpsertDriverElement :batchone
-- Discovery: legt ein Treiber-Element an oder aktualisiert es anhand von (source_id, external_id).
-- Übernimmt vom User vorab angelegte Elemente (first_seen_at = NULL) und behält deren id.
-- Eine leere external_id meint das Zugangs-Element der Quelle (gespeichert als NULL).
-- Audit (actor NULL): create für neue Elemente, update present false → true für zurückkehrende
-- und übernommene; Änderungen an Attributen werden nicht protokolliert.
WITH prev AS (
  SELECT p.present
  FROM element p
  WHERE p.source_id = @source_id::uuid
    AND p.external_id IS NOT DISTINCT FROM NULLIF(@external_id::text, '')
), up AS (
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
  SET kind             = excluded.kind,
      parent_id        = excluded.parent_id,
      global_ref       = excluded.global_ref,
      transport        = excluded.transport,
      direction        = excluded.direction,
      name             = excluded.name,
      attributes       = excluded.attributes,
      position         = excluded.position,
      actions          = excluded.actions,
      critical_actions = excluded.critical_actions,
      settings         = excluded.settings,
      present          = true,
      first_seen_at    = COALESCE(element.first_seen_at, now()),
      last_seen_at     = now(),
      updated_at       = now()
  RETURNING *
), audit AS (
  INSERT INTO audit_log (verb, object_type, object_id, before, after)
  SELECT 'create', 'element', up.id, NULL, to_jsonb(up)
  FROM up WHERE NOT EXISTS (SELECT 1 FROM prev)
  UNION ALL
  SELECT 'update', 'element', up.id, '{"present": false}'::jsonb, '{"present": true}'::jsonb
  FROM up, prev WHERE NOT prev.present
)
SELECT up.id FROM up;

-- name: FinishDiscovery :many
-- Abschluss einer Discovery, ein Statement pro Lauf und Quelle: gemeldete Elemente sind
-- gesehen (last_seen_at), alle übrigen der Quelle verschwinden (present = false).
-- Das Zugangs-Element (external_id NULL) gilt immer als gesehen; sein last_seen_at zeigt
-- die letzte vollständige Discovery.
-- Elemente werden nie gelöscht; kommen sie zurück, sind Label und Gruppen wieder da.
-- Audit (actor NULL): jeder Wechsel von present, in beide Richtungen.
-- Liefert die IDs der in diesem Lauf verschwundenen Elemente.
WITH seen AS (
  SELECT e.id, e.present AS was_present,
         (e.external_id IS NULL OR e.id = ANY (COALESCE(@seen_ids::uuid[], '{}'))) AS seen
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
  RETURNING element.id, element.present, seen.was_present
), audit AS (
  INSERT INTO audit_log (verb, object_type, object_id, before, after)
  SELECT 'update', 'element', updated.id,
         jsonb_build_object('present', updated.was_present), jsonb_build_object('present', updated.present)
  FROM updated
  WHERE updated.present IS DISTINCT FROM updated.was_present
)
SELECT updated.id FROM updated
WHERE NOT updated.present;

-- name: CreatePlannedElement :exec
-- Vom User vorab angelegtes Treiber-Element, das der Treiber noch nie gesehen hat.
WITH e AS (
  INSERT INTO element (id, kind, parent_id, device_id, source_id, external_id, label, notes, present)
  VALUES (@id::uuid, @kind::text, @parent_id::uuid, @device_id::uuid, @source_id::uuid, @external_id::text, @label, @notes, false)
  RETURNING *
)
INSERT INTO audit_log (actor_id, verb, object_type, object_id, after)
SELECT sqlc.narg(actor_id)::uuid, 'create', 'element', e.id, to_jsonb(e) FROM e;

-- name: CreateGroup :exec
WITH e AS (
  INSERT INTO element (id, kind, label, notes)
  VALUES (@id::uuid, 'group', @label, @notes)
  RETURNING *
)
INSERT INTO audit_log (actor_id, verb, object_type, object_id, after)
SELECT sqlc.narg(actor_id)::uuid, 'create', 'element', e.id, to_jsonb(e) FROM e;

-- name: UpdateElementUserFields :execrows
-- Protokolliert nur die User-Spalten, nicht die ganze Zeile.
WITH before AS (
  SELECT id, label, notes, monitored FROM element WHERE id = @id::uuid
), upd AS (
  UPDATE element
  SET label      = @label,
      notes      = @notes,
      monitored  = @monitored,
      updated_at = now()
  WHERE element.id = @id::uuid
  RETURNING element.id, element.label, element.notes, element.monitored
)
INSERT INTO audit_log (actor_id, verb, object_type, object_id, before, after)
SELECT sqlc.narg(actor_id)::uuid, 'update', 'element', upd.id, to_jsonb(before), to_jsonb(upd)
FROM upd JOIN before ON before.id = upd.id;

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
-- Audit: ein Eintrag für die Gruppe und je einer pro mitgelöschtem Objekt.
WITH before AS (
  SELECT * FROM element WHERE id = @group_id::uuid
), rules AS (
  DELETE FROM rule WHERE scope_element_id = @group_id::uuid RETURNING *
), grants AS (
  DELETE FROM permission_grant WHERE scope_element_id = @group_id::uuid RETURNING *
), forwards AS (
  DELETE FROM forward_target WHERE scope_element_id = @group_id::uuid RETURNING *
), memberships AS (
  DELETE FROM element_link
  WHERE type = 'member' AND (from_id = @group_id::uuid OR to_id = @group_id::uuid)
  RETURNING *
), upd AS (
  UPDATE element
  SET deleted_at = now(), updated_at = now()
  WHERE element.id = @group_id::uuid
  RETURNING element.*
)
INSERT INTO audit_log (actor_id, verb, object_type, object_id, before, after)
SELECT sqlc.narg(actor_id)::uuid, 'delete', 'element', upd.id, to_jsonb(before), to_jsonb(upd)
FROM upd JOIN before ON before.id = upd.id
UNION ALL
SELECT sqlc.narg(actor_id)::uuid, 'delete', 'rule', rules.id, to_jsonb(rules), NULL FROM rules
UNION ALL
SELECT sqlc.narg(actor_id)::uuid, 'delete', 'permission_grant', grants.id, to_jsonb(grants), NULL FROM grants
UNION ALL
SELECT sqlc.narg(actor_id)::uuid, 'delete', 'forward_target', forwards.id, to_jsonb(forwards), NULL FROM forwards
UNION ALL
SELECT sqlc.narg(actor_id)::uuid, 'delete', 'element_link', memberships.id, to_jsonb(memberships), NULL FROM memberships;
