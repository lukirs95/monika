# internal/forward

Weiterleitung von Ereignissen (`finding.*`, `action.finished`) an `syslog` und `webhook`.

Filter nach Ereignistyp, Mindest-Schweregrad und Scope. Best effort: wenige Wiederholungen im Speicher, danach `last_error`. Kein Routing, keine Eskalation.

Siehe `docs/ARCHITECTURE.md`, Abschnitt 13.
