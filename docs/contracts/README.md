# Verträge

Verträge legen fest, *wie* Komponenten zusammenarbeiten: Interfaces, Methoden, Typen und Abläufe. Das *Warum* steht in `docs/ARCHITECTURE.md`. Außerhalb des Stores entsteht Code erst, wenn sein Vertrag hier abgestimmt ist (siehe `CLAUDE.md`).

Diagramme in Mermaid: `classDiagram` für Interfaces und Typen, `sequenceDiagram` für Abläufe, `stateDiagram-v2` für Lebenszyklen.

| Vertrag | Inhalt | Status |
|---|---|---|
| [`sdk.md`](sdk.md) | Treiber-SDK: Registrierung, `Source`, `Reporter`, Scheduler, Ausführung, Zähler-Helfer | abgestimmt 2026-10-09 |
