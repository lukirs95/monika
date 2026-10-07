# internal/core/inventory

Übernimmt den Elementbaum aus der Discovery eines Treibers.

- nur Elemente und Links der jeweiligen Quelle anfassen
- Abgleich über `external_id`; verschwundene Elemente → `present = false`, nie löschen
- vorab angelegte Elemente (`first_seen_at = NULL`) übernehmen
- User-Spalten (`label`, `notes`, `monitored`) nie überschreiben
- unaufgelöste Links (`from_ref`/`to_ref`) auflösen, sobald das Ziel bekannt ist
- `member`-Links auf Zyklen prüfen

Siehe `docs/ARCHITECTURE.md`, Abschnitt 5, 6 und 7.
