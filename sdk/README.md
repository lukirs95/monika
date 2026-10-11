# sdk

Öffentliche Schnittstelle zwischen Core und Treibern. Das einzige Paket, das Treiber importieren.

- `sdk`: `Driver`, `Source`, `Reporter`, `Scheduler`, Meldungen (`Snapshot`, `Update`, `Element`, `Link`, `Alarm`), Ausführung (`Command`, `Result`, `ActionDef`, `Setting`, `Param`), Registry (`Register`, `Lookup`, `Drivers`) mit Prüfung des Katalogs, `Driver.CheckElement`, Zähler-Helfer `Counter`
- `sdk/vocab`: Vokabular mit Arten, Transporten, Fakten (`<measurement>.<feld>` mit Typ und Art), Aktionen, Namensräumen und reservierten Namen
- `sdk/sdktest`: Testbaukasten für Treiber und Core (aufzeichnender `Reporter`, `Conformance`, programmierbarer Fake-Treiber)

Verträge: `docs/contracts/sdk.md`, `docs/contracts/vocabulary.md`; Leitfaden: `docs/DRIVERS.md`.
