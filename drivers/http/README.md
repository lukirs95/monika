# drivers/http

Generische Sende-Aufrufe. Jeder konfigurierte Aufruf wird ein Element (`kind = trigger`).

- Konfiguration ist das Gerät (`device_source.config`)
- nur Sende-Aufrufe, kein Lesen vom Zielsystem
- `body`/`path` sind Vorlagen; Platzhalter kontextgerecht eingesetzt
- `set` ändert gespeicherte Werte, `trigger {…}` setzt Overrides nur für einen Aufruf
- 2xx → `accepted`, 4xx/5xx → `rejected`, Timeout → `failed`

Siehe `docs/ARCHITECTURE.md`, Abschnitt 11.
