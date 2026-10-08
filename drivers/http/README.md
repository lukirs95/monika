# drivers/http

Treiber für HTTP als Sende-Ziel. Jeder konfigurierte Aufruf wird ein Element (`kind = trigger`, Aktion `trigger`).

- nur Sende-Aufrufe, kein Lesen vom Zielsystem; die Konfiguration ändert nur der User
- `body`/`path` sind Vorlagen; Platzhalter kontextgerecht eingesetzt
- Parameter pro Aufruf (`trigger {…}`), Schema mit `default` in `attributes`; kein `set`
- Fakten: `last_ok`, `last_called_at`, `last_duration_ms`, `http.status`
- 2xx → `accepted`, 4xx/5xx → `rejected`, Timeout → `failed`
- optionaler Health-Check als Lebenszeichen; kritische Aufrufe → `critical_actions`

Siehe `docs/ARCHITECTURE.md`, Abschnitt 11.
