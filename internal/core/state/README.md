# internal/core/state

Aktueller Zustand aller Elemente im Speicher.

- Fakten (flache Map mit Punkt-Schlüsseln) und Gerätealarme übernehmen; Fakten liegen nur im Speicher
- im festen Takt alle aktuellen Werte nach InfluxDB schreiben
- Lebenszeichen der Quellen verfolgen; bleiben sie aus → `source.unreachable`, Elemente der Quelle `unknown`
- reservierter Fakt `present` aus dem Inventar

Siehe `docs/ARCHITECTURE.md`, Abschnitt 8 und 14.
