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
INSERT INTO element_link (id, type, from_id, to_id, position, label)
VALUES (@id, 'member', @group_id::uuid, @member_id::uuid, @position, @label);

-- name: UpdateMemberLink :execrows
UPDATE element_link
SET position = @position,
    label    = @label
WHERE id = @id AND type = 'member';

-- name: DeleteMemberLink :execrows
DELETE FROM element_link
WHERE id = @id AND type = 'member';

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
