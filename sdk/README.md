# sdk

Öffentliche Schnittstelle zwischen Core und Treibern. Das einzige Paket, das Treiber importieren.

- Treiber-Interfaces (Discovery, Fakten, Alarme, Links, Ausführung)
- Registry für die Registrierung in `init()`
- Vokabular: `kind`, `transport`, Kern- und bekannte Fakt-Schlüssel, Kern-Aktionen
- Typen für Aktionsdefinitionen (Parameter-Schema, Prüfbedingung, `critical`) und `settings`
- JSON-Schema für die Treiberkonfiguration

Die konkreten Interfaces sind noch offen.

Siehe `docs/ARCHITECTURE.md`, Abschnitt 4.
