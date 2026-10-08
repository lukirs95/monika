# internal/forward

Weiterleitung von Ereignissen (`finding.raised`, `finding.cleared`, `finding.withdrawn`, `finding.acked`, `action.finished`) an `syslog` und `webhook`.

Filter nach Ereignistyp, Mindest-Schweregrad und Scope. Best effort: wenige Wiederholungen im Speicher, danach `last_error`. Kein Routing, keine Eskalation.

Siehe `docs/ARCHITECTURE.md`, Abschnitt 13.
