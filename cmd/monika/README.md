# cmd/monika

Einstiegspunkt des Monolithen.

- Bootstrap laden (`internal/config`)
- Master-Key prüfen (Startprüfung des Secret-Stores)
- Datenbank-Migrationen ausführen
- initialen Admin anlegen, falls noch kein Benutzer existiert
- Treiber über `drivers/all` einbinden
- Komponenten verdrahten und starten

Siehe `docs/ARCHITECTURE.md`, Abschnitt 3 und 14.
