# Verträge

Verträge legen fest, *wie* Komponenten zusammenarbeiten: Interfaces, Methoden, Typen und Abläufe. Das *Warum* steht in `docs/ARCHITECTURE.md`. Außerhalb des Stores entsteht Code erst, wenn sein Vertrag hier abgestimmt ist (siehe `CLAUDE.md`).

Diagramme in Mermaid: `classDiagram` für Interfaces und Typen, `sequenceDiagram` für Abläufe, `stateDiagram-v2` für Lebenszyklen.

| Vertrag | Inhalt | Status |
|---|---|---|
| [`vocabulary.md`](vocabulary.md) | Vokabular: Arten, Transporte, Fakten mit Typ und Einheit, Measurements, Aktionen, Namensräume, reservierte Namen | abgestimmt 2026-10-11 |
| [`sdk.md`](sdk.md) | Treiber-SDK: Registrierung, Fakt-Katalog, `Source`, `Reporter`, Scheduler, Ausführung, Zähler-Helfer, Testbaukasten, Logging | abgestimmt 2026-10-09, überarbeitet 2026-10-11 |
| [`core.md`](core.md) | Core-Komponenten: eine Schleife, `View`, Ereignisse, Befehle, Inventar → Fakten → Bewertung, Ursache/Symptom | abgestimmt 2026-10-09, überarbeitet 2026-10-11 |
| [`flows.md`](flows.md) | Abläufe: Start, Herunterfahren, Weiterleitung, Aktion, Preset-Lauf | abgestimmt 2026-10-09, überarbeitet 2026-10-11 |
| [`services.md`](services.md) | Dienste: Supervisor, Executor, Preset-Runner, Weiterleitung, Aufbewahrung; Zusammenspiel mit der API | abgestimmt 2026-10-09, überarbeitet 2026-10-11 |
| [`influx.md`](influx.md) | Influx-Schreiber: Schema, Takt, Puffer | abgestimmt 2026-10-11 |
| [`bootstrap.md`](bootstrap.md) | Bootstrap: Konfiguration, Verdrahtung, Lebenszyklus der Dienste | abgestimmt 2026-10-11 |
| [`auth.md`](auth.md) | Anmeldung (Passwort, TOTP, Sitzungen, Tokens, Kiosk) und Rechte (`Allowed`) | abgestimmt 2026-10-09, überarbeitet 2026-10-10 |
| [`api.md`](api.md), [`api.yaml`](api.yaml) | HTTP-API: Konventionen, Verteiler und Abo-Modell; Endpunkte als OpenAPI 3.0.3 (`make api-check`) | abgestimmt 2026-10-09, überarbeitet 2026-10-11 |
