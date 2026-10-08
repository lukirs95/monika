# internal/action

Aktions-Executor.

- Läufe (`action_run`) pro Element nacheinander (FIFO)
- Ausführung immer über die Quelle, die das Element besitzt
- Prüfbedingung gegen Fakten: `accepted` → `confirmed` / `unconfirmed`
- Sammelaktionen auf Gruppen und Geräte verteilen; verschwundene Mitglieder → `failed`; übergeordneter Lauf = schlechtester Status der Kinder
- Aktionen für das ganze Gerät (Zugangs-Elemente) auf Geräte/Gruppen nur mit angegebenem Treiber
- Rechteprüfung inkl. `critical` (Aktionsdefinition oder `critical_actions`)

Siehe `docs/ARCHITECTURE.md`, Abschnitt 9.
