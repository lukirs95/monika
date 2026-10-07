# internal/core/state

Aktueller Zustand aller Elemente im Speicher.

- Fakten (flache Map mit Punkt-Schlüsseln) und Gerätealarme übernehmen
- in `element_state` nur bei Änderung schreiben, jeden Wert nach InfluxDB
- Staleness erkennen (älter als etwa dreifacher Takt → `unknown`)

Siehe `docs/ARCHITECTURE.md`, Abschnitt 8 und 14.
