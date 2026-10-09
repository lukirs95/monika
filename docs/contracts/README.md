# Verträge

Verträge legen fest, *wie* Komponenten zusammenarbeiten: Interfaces, Methoden, Typen und Abläufe. Das *Warum* steht in `docs/ARCHITECTURE.md`. Außerhalb des Stores entsteht Code erst, wenn sein Vertrag hier abgestimmt ist (siehe `CLAUDE.md`).

Diagramme in Mermaid: `classDiagram` für Interfaces und Typen, `sequenceDiagram` für Abläufe, `stateDiagram-v2` für Lebenszyklen.

| Vertrag | Inhalt | Status |
|---|---|---|
| [`sdk.md`](sdk.md) | Treiber-SDK: Registrierung, `Source`, `Reporter`, Scheduler, Ausführung, Zähler-Helfer, Logging | abgestimmt 2026-10-09 |
| [`core.md`](core.md) | Core-Komponenten: eine Schleife, `View`, Ereignisse, Befehle, Inventar → Fakten → Bewertung | abgestimmt 2026-10-09 |
| [`flows.md`](flows.md) | Abläufe: Start, Herunterfahren, Weiterleitung, Aktion, Preset-Lauf; Supervisor | abgestimmt 2026-10-09 |
| [`services.md`](services.md) | Dienste: Supervisor, Executor, Preset-Runner, Weiterleitung; Zusammenspiel mit der API | abgestimmt 2026-10-09 |
| [`auth.md`](auth.md) | Anmeldung (Passwort, TOTP, Sitzungen, Tokens, Kiosk) und Rechte (`Allowed`) | abgestimmt 2026-10-09 |
