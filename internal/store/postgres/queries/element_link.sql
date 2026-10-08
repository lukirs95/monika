-- name: ListElementLinks :many
-- Lädt den Link-Graphen beim Start in den Speicher des Cores.
SELECT * FROM element_link
ORDER BY id;

-- name: InsertDriverLink :batchexec
-- Discovery: neuer Link einer Quelle (feeds, uses, represents). Ein noch unbekannter
-- Endpunkt steht als global_ref in from_ref bzw. to_ref.
INSERT INTO element_link (id, type, source_id, from_id, from_ref, to_id, to_ref)
VALUES (@id, @type, @source_id::uuid, @from_id, @from_ref, @to_id, @to_ref);

-- name: DeleteDriverLinks :execrows
-- Discovery: weggefallene Links einer Quelle werden gelöscht, nicht weich markiert.
DELETE FROM element_link
WHERE source_id = @source_id::uuid
  AND id = ANY (@ids::uuid[]);

-- name: ResolveLinkRef :execrows
-- Löst unaufgelöste Endpunkte auf, sobald ein Element mit dieser global_ref bekannt ist.
UPDATE element_link
SET from_id  = CASE WHEN from_ref = @global_ref::text THEN @element_id::uuid ELSE from_id END,
    from_ref = CASE WHEN from_ref = @global_ref::text THEN NULL ELSE from_ref END,
    to_id    = CASE WHEN to_ref = @global_ref::text THEN @element_id::uuid ELSE to_id END,
    to_ref   = CASE WHEN to_ref = @global_ref::text THEN NULL ELSE to_ref END
WHERE from_ref = @global_ref::text
   OR to_ref = @global_ref::text;

-- name: LockMemberLinks :exec
-- Serialisiert Änderungen an Gruppenmitgliedschaften bis zum Ende der Transaktion, damit
-- zwei parallele Änderungen die Zyklenprüfung nicht gemeinsam umgehen.
SELECT pg_advisory_xact_lock(hashtext('element_link.member'));

-- name: CreateMemberLink :exec
-- Gruppenmitgliedschaft (vom User). Nur über AddGroupMember aufrufen (Lock und Zyklenprüfung).
WITH l AS (
  INSERT INTO element_link (id, type, from_id, to_id, position, label)
  VALUES (@id::uuid, 'member', @group_id::uuid, @member_id::uuid, @position, @label)
  RETURNING *
)
INSERT INTO audit_log (actor_id, verb, object_type, object_id, after)
SELECT sqlc.narg(actor_id)::uuid, 'create', 'element_link', l.id, to_jsonb(l) FROM l;

-- name: UpdateMemberLink :execrows
WITH before AS (
  SELECT * FROM element_link WHERE id = @id::uuid
), upd AS (
  UPDATE element_link
  SET position = @position,
      label    = @label
  WHERE element_link.id = @id::uuid AND element_link.type = 'member'
  RETURNING *
)
INSERT INTO audit_log (actor_id, verb, object_type, object_id, before, after)
SELECT sqlc.narg(actor_id)::uuid, 'update', 'element_link', upd.id, to_jsonb(before), to_jsonb(upd)
FROM upd JOIN before ON before.id = upd.id;

-- name: DeleteMemberLink :execrows
WITH l AS (
  DELETE FROM element_link
  WHERE id = @id::uuid AND type = 'member'
  RETURNING *
)
INSERT INTO audit_log (actor_id, verb, object_type, object_id, before)
SELECT sqlc.narg(actor_id)::uuid, 'delete', 'element_link', l.id, to_jsonb(l) FROM l;

-- name: MemberLinkWouldCycle :one
-- true, wenn group_id gleich member_id ist oder bereits (rekursiv) Mitglied von member_id.
WITH RECURSIVE below (id) AS (
  SELECT @member_id::uuid
  UNION
  SELECT l.to_id FROM element_link l
  JOIN below b ON l.from_id = b.id
  WHERE l.type = 'member'
)
SELECT EXISTS (SELECT 1 FROM below WHERE id = @group_id::uuid) AS would_cycle;
