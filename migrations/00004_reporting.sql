-- +goose Up
-- Schnittstelle für externe Werkzeuge (Grafana, Reporting, Skripte): lesende Views im Schema
-- reporting. Intern darf sich das Schema ändern, solange diese Views gleich bleiben.
-- Handgeschrieben, weil DBML keine Views kennt. Die Rolle monika_reporting legt der Betreiber an
-- (docs/OPERATIONS.md); sie sieht alles, ohne Monikas Rechte.
CREATE SCHEMA reporting;

-- Elemente mit Anzeigepfad: Label, sonst Name, vom Gerät bis zum Element, getrennt durch " / ";
-- das Zugangs-Element wird ausgelassen. Dieselbe Regel nutzt Monika für element_path.
CREATE VIEW reporting.element AS
WITH RECURSIVE chain (element_id, id, parent_id, kind, name, depth) AS (
  SELECT e.id, e.id, e.parent_id, e.kind, COALESCE(e.label, e.name), 0
  FROM public.element e
  UNION ALL
  SELECT c.element_id, p.id, p.parent_id, p.kind, COALESCE(p.label, p.name), c.depth + 1
  FROM chain c
  JOIN public.element p ON p.id = c.parent_id
)
SELECT e.id, e.kind, e.device_id, e.source_id, e.label, e.name, e.present,
       (SELECT string_agg(c.name, ' / ' ORDER BY c.depth DESC)
        FROM chain c
        WHERE c.element_id = e.id AND c.kind <> 'access' AND c.name IS NOT NULL) AS path
FROM public.element e;

-- Befunde mit Pfad: der aktuelle, solange das Element existiert, sonst der beim Entstehen gespeicherte.
CREATE VIEW reporting.finding AS
SELECT f.id, f.element_id,
       COALESCE(e.path, f.element_path) AS element_path,
       e.id IS NULL AS element_deleted,
       f.origin, f.code, f.severity, f.message, f.rule_id, f.caused_by_id,
       f.raised_at, f.cleared_at, f.details ->> 'cleared_reason' AS cleared_reason,
       f.acked_at, f.acked_by_id
FROM public.finding f
LEFT JOIN reporting.element e ON e.id = f.element_id;

-- +goose Down
DROP SCHEMA reporting CASCADE;
