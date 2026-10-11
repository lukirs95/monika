# internal/action

Typen der Läufe (`Run`, `Status`, `Verify`); der Aktions-Executor liegt in `executor/` (`docs/contracts/services.md`).

- Läufe (`action_run`) pro Element nacheinander (FIFO)
- Ausführung immer über die Quelle, die das Element besitzt
- Prüfbedingung gegen Fakten: `accepted` → `confirmed` / `unconfirmed`
- Sammelaktionen auf Gruppen und Geräte verteilen; verschwundene Mitglieder → `failed`; übergeordneter Lauf = schlechtester Status der Kinder
- Aktionen für das ganze Gerät (Zugangs-Elemente) auf Geräte/Gruppen nur mit angegebenem Treiber
- `critical` einfrieren (Aktionsdefinition, `critical_actions`, kritische Schlüssel bei `set`); die Rechte prüft die API

Siehe `docs/ARCHITECTURE.md`, Abschnitt 9.
