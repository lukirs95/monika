-- +goose Up
-- Höchstens ein aktiver Befund pro Element, Code und Regel.
-- Handgeschrieben, weil DBML keine Teilindizes (WHERE) kennt.
CREATE UNIQUE INDEX finding_active_uq ON finding (
  element_id,
  code,
  COALESCE(rule_id, '00000000-0000-0000-0000-000000000000'::uuid)
) WHERE cleared_at IS NULL;

-- +goose Down
DROP INDEX finding_active_uq;
