# internal/core/state

Aktueller Zustand aller Elemente im Speicher.

- `Facts`: aktuelle Fakten aller Elemente, nur im Speicher; `Replace` (Snapshot), `Merge` (Update), `Drop`; liefert, was sich geändert hat; copy-on-write für die `View`
- `Liveness`: Phase jeder laufenden Quelle; Live nur nach `Connected` und Snapshot mit aktuellen Lebenszeichen; `source.unreachable` nach 3 × Heartbeat seit dem letzten Lebenszeichen
- Logik ohne Goroutinen und Locks; Besitzer ist die Schleife des Core

Siehe `docs/ARCHITECTURE.md`, Abschnitt 8 und 14.
