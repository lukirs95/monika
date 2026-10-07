# internal/core

Herz von Monika. Hält Inventar, Zustand und Link-Graph im Speicher und bewertet dort. PostgreSQL dient der Persistenz, nicht der Bewertungslogik.

- `inventory/` – Discovery-Sync
- `state/` – Fakten und Staleness
- `evaluation/` – Regeln, Befunde, Ursache/Symptom, Health

Siehe `docs/ARCHITECTURE.md`, Abschnitt 3 (Datenfluss: Beobachten).
