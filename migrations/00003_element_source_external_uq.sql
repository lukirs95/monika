-- +goose Up
-- external_id eindeutig pro Quelle, mit genau einem Zugangs-Element (external_id NULL) pro Quelle.
-- Teilindex, weil Geräte und Gruppen weder Quelle noch external_id haben und sonst kollidieren.
-- Handgeschrieben, weil DBML weder WHERE noch NULLS NOT DISTINCT kennt. Deckt auch Abfragen nach
-- source_id ab.
CREATE UNIQUE INDEX element_source_external_uq
  ON element (source_id, external_id) NULLS NOT DISTINCT
  WHERE source_id IS NOT NULL;

-- +goose Down
DROP INDEX element_source_external_uq;
