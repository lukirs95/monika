# Monika

Universeller, einfacher **Konfigurator und Monitor für Broadcast-Geräte**: zeigt den echten Zustand von Geräten, Streams und I/O unterschiedlicher Hersteller in einem einheitlichen Modell und führt einmalige Aktionen auf ihnen aus.

- **Das Gerät ist immer die Wahrheit.** Kein Soll-Ist-Abgleich der Konfiguration.
- **Keine Kreuzschiene.** Signalbeziehungen werden beobachtet, nie geschaltet.
- **Treiber melden Fakten, der Core bewertet.**

Stack: Go · PostgreSQL ≥ 18 · InfluxDB · Monolith · Treiber zur Compile-Zeit (Telegraf-Modell)

## Dokumentation

| Dokument | Inhalt |
|---|---|
| [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) | alle verbindlichen Architekturentscheidungen |
| [`schema/monika.dbml`](schema/monika.dbml) | exakte Definition des Datenbankschemas (dbdiagram.io); Quelle der Migrationen |
| [`api/openapi.yaml`](api/openapi.yaml) | HTTP-API als OpenAPI 3.0.3; Quelle des erzeugten Server-Codes |
| [`docs/contracts/`](docs/contracts/) | Verträge zwischen den Komponenten: Interfaces, Methoden, Abläufe (Mermaid) |

## Projektstruktur

```
monika/
├─ cmd/monika/              Einstiegspunkt: Bootstrap, Verdrahtung, Start
├─ internal/
│  ├─ config/               Bootstrap aus Umgebung bzw. .env
│  ├─ api/                  HTTP-API und Live-Push an die UI
│  ├─ auth/                 Benutzer, Rollen, Rechte, API-Tokens, später OIDC
│  ├─ secret/               verschlüsselter Secret-Store (AES-256-GCM)
│  ├─ core/
│  │  ├─ inventory/         Discovery-Sync: Elemente, Geräte, Quellen, Links
│  │  ├─ state/             Fakten im Speicher, Lebenszeichen der Quellen
│  │  └─ evaluation/        Regeln, Befunde, Ursache/Symptom, Health, Rollup
│  ├─ action/               Aktions-Executor: FIFO pro Element, Prüfung, Läufe
│  ├─ preset/               Preset-Runner: Stufen, Delays, Rechte-Vorabprüfung
│  ├─ forward/              Weiterleitung: syslog, webhook
│  ├─ store/postgres/       Persistenz in PostgreSQL
│  └─ history/influx/       Messwerte nach InfluxDB
├─ sdk/                     öffentliche Treiber-Schnittstelle: Interfaces, Vokabular, Registry
├─ drivers/
│  ├─ all/                  Sammelpaket: importiert alle Treiber (Telegraf-Modell)
│  ├─ videoxlink/           VideoXLink X4/X8 (Adapter für goxlinkclient)
│  ├─ embrionix/            Embrionix / Riedel emFUSION-2, MuoN
│  ├─ ravio/                DirectOut RAVIO
│  └─ http/                 generische Sende-Aufrufe
├─ api/openapi.yaml         HTTP-API (OpenAPI 3.0.3), Vertrag und Quelle für oapi-codegen
├─ schema/monika.dbml       Datenbankschema (DBML), Quelle für make db-generate
├─ migrations/              SQL-Migrationen (aus schema/monika.dbml)
├─ web/                     Frontend (Technologie offen)
├─ deploy/                  lokale Umgebung und Deployment
└─ docs/
   ├─ ARCHITECTURE.md
   ├─ DRIVERS.md            Leitfaden: einen Treiber schreiben
   ├─ OPERATIONS.md         Betrieb: Auslieferung, Stack, Backup, Updates
   ├─ STATUS.md             Arbeitsstand, nächste Schritte, bekannte Lücken
   └─ contracts/            Verträge (Mermaid) vor dem Code; die API-Spezifikation liegt in api/
```

Abhängigkeiten zeigen nur nach innen: `drivers/*` kennt nur `sdk/` (und die jeweilige Gerätelibrary), nie `internal/`. `internal/` implementiert die Seite des Cores.

## Konfiguration

Bootstrap über Umgebungsvariablen bzw. `.env` – siehe `docs/ARCHITECTURE.md`, Abschnitt 14. Eine `.env.example` mit allen Variablen entsteht mit dem Code (`docs/OPERATIONS.md`).
