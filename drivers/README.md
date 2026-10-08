# drivers

Treiber nach dem Telegraf-Modell: ein Paket pro Treiber, Registrierung in `init()`, Einbindung zur Compile-Zeit über `drivers/all`.

Regeln für alle Treiber:

- nur `sdk/` importieren, nie `internal/`
- dünne Adapter: Gerätelogik gehört in eine eigenständige Library
- stabile `external_id` (eindeutig pro Quelle), keine Bewertung, nur Elemente/Links der eigenen Quelle
- vollständiger Stand beim Verbinden, Lebenszeichen, Raten über den Zähler-Helfer
- Werte für das ganze Gerät an das Zugangs-Element (leere `external_id`)
- nie die eigene Konfiguration ändern; Sende-Ziele nach Abschnitt 11
- keine Aktion, die `feeds`-Links ändert
- kritische Aktionen und Einstellungen kennzeichnen
- Zugangsdaten nur über den Secret-Store

Siehe `docs/ARCHITECTURE.md`, Abschnitt 4.
