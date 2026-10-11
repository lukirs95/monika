# internal/core/inventory

Übernimmt den Elementbaum aus der Discovery eines Treibers.

- nur Elemente und Links der jeweiligen Quelle anfassen
- Abgleich über `external_id` (eindeutig pro Quelle); verschwundene Elemente → `present = false`, nie löschen
- Zugangs-Element (`kind = access`) beim Anlegen einer Quelle erzeugen; es verschwindet nie und ist die Wurzel der Elemente seiner Quelle; es trägt die `global_ref` des Geräts
- reservierte `kind`-Werte (`device`, `group`, `access`) aus der Discovery ablehnen
- Links und Wechsel von `present` (`appear`, `disappear`) mit `actor_id = NULL` ins Audit
- Geräte, Quellen und Gruppen hart löschen: vorher Befunde zurückziehen und Mitgliedschaften, Regeln, Rechte, Weiterleitungen mit Audit entfernen; abgelehnt bei Preset-Schritten
- User-Spalten (`label`, `notes`, `monitored`) nie überschreiben
- unaufgelöste Links (`from_ref`/`to_ref`) auflösen, sobald das Ziel bekannt ist
- `member`-Links auf Zyklen prüfen (unter Advisory Lock)

Siehe `docs/ARCHITECTURE.md`, Abschnitt 5, 6 und 7.
