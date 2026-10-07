# internal/config

Liest die Bootstrap-Konfiguration aus Umgebungsvariablen bzw. `.env` (Variablen haben Vorrang).

Enthält ausschließlich Bootstrap-Werte (Master-Key, Datenbank, InfluxDB, initialer Admin). Alle übrigen Geheimnisse liegen im Secret-Store.

Siehe `docs/ARCHITECTURE.md`, Abschnitt 14 (Bootstrap aus der Umgebung).
