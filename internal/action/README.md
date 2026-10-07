# internal/action

Aktions-Executor.

- Läufe (`action_run`) pro Element nacheinander (FIFO)
- Ausführung immer über die Quelle, die das Element besitzt
- Prüfbedingung gegen Fakten: `accepted` → `confirmed` / `unconfirmed`
- Sammelaktionen auf Gruppen und Geräte verteilen
- Rechteprüfung inkl. `critical`

Siehe `docs/ARCHITECTURE.md`, Abschnitt 9.
