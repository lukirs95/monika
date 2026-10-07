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
| [`docs/schema/monika.dbml`](docs/schema/monika.dbml) | exakte Definition des Datenbankschemas (dbdiagram.io) |

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
│  │  ├─ state/             Zustand im Speicher, Fakten, Staleness
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
├─ migrations/              SQL-Migrationen (aus docs/schema/monika.dbml)
├─ web/                     Frontend (Technologie offen)
├─ deploy/                  lokale Umgebung und Deployment
└─ docs/
   ├─ ARCHITECTURE.md
   └─ schema/monika.dbml
```

Abhängigkeiten zeigen nur nach innen: `drivers/*` kennt nur `sdk/` (und die jeweilige Gerätelibrary), nie `internal/`. `internal/` implementiert die Seite des Cores.

## Konfiguration

Bootstrap über Umgebungsvariablen bzw. `.env` – siehe [`.env.example`](.env.example) und `docs/ARCHITECTURE.md`, Abschnitt 14.
