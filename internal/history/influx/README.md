# internal/history/influx

Schreibt im festen Takt die historisierten Fakten, Health, Rollups und eigene Kennzahlen (`monika`) als Line Protocol nach InfluxDB; puffert höchstens 1 min.

Tags: `element_id`, `device_id`, `source_id`, `driver`, `kind`, `transport`. Nie Labels als Tags.

Vertrag: `docs/contracts/influx.md`.
