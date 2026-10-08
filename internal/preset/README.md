# internal/preset

Preset-Runner.

- Stufen nacheinander, Schritte einer Stufe parallel, `delay_ms` pro Schritt
- `continue_on_error`
- `driver` am Schritt bei Aktionen für das ganze Gerät; keine Schritte auf gelöschte Geräte und Gruppen
- Rechte-Vorabprüfung für alle Schritte, bevor irgendetwas läuft
- kein Abgleich nach dem Ausführen (Preset ist kein Sollzustand)

Siehe `docs/ARCHITECTURE.md`, Abschnitt 10.
