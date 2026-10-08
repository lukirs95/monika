# sdk

Öffentliche Schnittstelle zwischen Core und Treibern. Das einzige Paket, das Treiber importieren.

- Treiber-Interfaces (Discovery, Fakten, Alarme, Links, Ausführung)
- Registry für die Registrierung in `init()`
- Vokabular: `kind`, `transport`, Kern- und bekannte Fakt-Schlüssel, Kern-Aktionen; reservierte Werte (`kind` `device`, `group`, `access`; Fakt `present`)
- Typen für Aktionsdefinitionen (Parameter-Schema, Prüfbedingung, `critical`), `settings` und `critical_actions`
- Zähler-Helfer: Rohwerte → `<fakt>.rate` pro Sekunde, mit Erkennung von Reset und Überlauf
- Vertrag: vollständiger Stand beim (Wieder-)Verbinden, Lebenszeichen mit erwartetem Abstand, Zugangs-Element mit leerer `external_id`
- JSON-Schema für die Treiberkonfiguration

Die konkreten Interfaces sind noch offen.

Siehe `docs/ARCHITECTURE.md`, Abschnitt 4.
