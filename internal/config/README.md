# internal/config

Liest die Bootstrap-Konfiguration aus Umgebungsvariablen bzw. `.env` (Variablen haben Vorrang).

Enthält ausschließlich Bootstrap-Werte (Master-Key, Datenbank, öffentliche URL, Sitzungen, Logging, InfluxDB, Aufbewahrung, initialer Admin). Alle übrigen Geheimnisse liegen im Secret-Store.

- strikte `.env`-Teilmenge (`KEY=VALUE`, Kommentarzeilen, optionale Anführungszeichen, keine Ersetzung)
- unbekannte `MONIKA_*`-Variablen und ungültige Werte brechen ab, alle Fehler auf einmal
- geheime Werte als `config.Secret`, beim Ausgeben und Loggen `[redacted]`

Vertrag: `docs/contracts/bootstrap.md`; Variablen: `docs/ARCHITECTURE.md`, Abschnitt 14.
