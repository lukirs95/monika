# Betrieb

Wie Monika ausgeliefert, betrieben, gesichert und aktualisiert wird. Konfiguration und Variablen: [`ARCHITECTURE.md`](ARCHITECTURE.md), Abschnitt 14, und [`contracts/bootstrap.md`](contracts/bootstrap.md). Die Dateien in `deploy/` entstehen mit dem Code nach dieser Beschreibung.

## Auslieferung

- **Ein statisches Binary** (`CGO_ENABLED=0`) für `linux/amd64` und `linux/arm64`.
- **Container-Image** auf Basis von `distroless/static` (ohne Shell und Paketmanager), Benutzer ohne Root-Rechte, nur lesbares Dateisystem.
- `make image` baut das Binary **lokal** und kopiert es ins Image. Go wird auch für Releases nie in Docker gebaut (`CLAUDE.md`).
- **Healthcheck:** `monika -healthcheck` fragt `GET /healthz` auf der eigenen Adresse ab und endet mit `0` (gesund) oder `1`. Das Image hat kein `curl`; Docker ruft dafür das Binary selbst auf. Neben `-version` ist das das einzige Flag.

## Stack für den Produktivbetrieb

`deploy/compose.prod.yaml`:

| Dienst | Image | Aufgabe |
|---|---|---|
| `monika` | Monika | Monitor und Konfigurator; Healthcheck `monika -healthcheck` |
| `postgres` | `postgres:18-alpine` | Hauptdatenbank, Volume für die Daten |
| `influxdb` | `influxdb:2` | Zeitreihen; Bucket und Token beim ersten Start per `DOCKER_INFLUXDB_INIT_*`, Aufbewahrung des Buckets z. B. 30 Tage |
| `caddy` | `caddy:2` | Reverse-Proxy mit automatischem TLS, leitet an `monika:8080` weiter, setzt `X-Forwarded-For` |

- InfluxDB ist fester Teil der Architektur und deshalb im Stack. Wer schon eine InfluxDB betreibt, lässt den Dienst weg und setzt `MONIKA_INFLUX_*` auf sie; Monika spricht nur Line Protocol und ist versionsunabhängig.
- **Grafana** ist nicht im Stack: Es ist ein Werkzeug, das Monika nutzt, kein Teil davon. Es liest InfluxDB und das Schema `reporting` in PostgreSQL (Rolle `monika_reporting`).
- `MONIKA_TRUSTED_PROXIES` enthält das Netz des Stacks, damit Monika die echte IP des Clients sieht.
- **`.env.example`** im Projektstamm listet alle Variablen aus `ARCHITECTURE.md` Abschnitt 14 mit Standardwerten und Kommentaren; die echte `.env` gehört nicht ins Git und hat Rechte `600`.

## Netzwerk

- Monika braucht TCP bzw. HTTP zu den **Management-Netzen** der Geräte und sendet Syslog und Webhooks. Medienströme empfängt sie nie.
- Ein normales Docker-Bridge-Netz reicht. Ein Host-Netz wäre erst für Multicast-Discovery (z. B. mDNS bei NMOS) nötig, die Version 1 nicht hat.
- Liegen Geräte in mehreren VLANs, muss der Host sie routen; Monika selbst kennt keine Netzwerkkonfiguration.

## Uhrzeit

Der Host muss per **NTP synchron** laufen. TOTP verträgt nur ±30 s Abweichung, und Zeitstempel von Befunden, Läufen, Audit und InfluxDB hängen an der Uhr des Hosts. Das ist eine Voraussetzung, keine Funktion von Monika.

## Datenbank und Rechte

| Rolle | Rechte |
|---|---|
| `monika` | Besitzer des Schemas; führt beim Start die Migrationen aus |
| `monika_reporting` | liest nur das Schema `reporting` (Grafana, Reporting, Skripte) |
| `monika_backup` | liest alles für `pg_dump`, schreibt nichts |

Die Migrationen legen das Schema `reporting` und seine Views an, nicht die Rollen; Rollen und Passwörter richtet der Betreiber einmal ein (Beispiel-Skript in `deploy/`).

## Updates

- **Migrationen laufen beim Start, nur vorwärts.** Es gibt keine Down-Migrationen im Betrieb; zurück heißt: Backup einspielen und die alte Version starten.
- Mit dem ersten produktiven Einsatz wird `00001_init.sql` **eingefroren**; danach ändert sich das Schema nur noch über neue Migrationen.
- **Vor jedem Update ein Backup.** Ablauf: Backup → neues Image → Neustart → Log prüfen (Migrationen, `VerifyAndRotate`, Start der Quellen).
- Laufende Läufe enden beim Stopp nach F8 (`contracts/flows.md`); nichts wird nach dem Neustart erneut gesendet.

## Backup und Wiederherstellung

- Monika macht selbst keine Backups. Empfohlen: `pg_dump` täglich (z. B. ein Cron-Job oder ein Dienst im Stack mit `monika_backup`), mehrere Tage aufbewahrt, außerhalb des Hosts gespeichert.
- **Der Master-Key wird getrennt gesichert** (Passwort-Manager, Vault). Ohne ihn sind Secrets und TOTP-Geheimnisse im Backup nicht lesbar; ein Backup allein ist für Angreifer wertlos (`ARCHITECTURE.md` Abschnitt 14).
- **Wiederherstellung regelmäßig proben:** Dump in eine leere Datenbank, Monika mit demselben Master-Key starten; `VerifyAndRotate` bestätigt, dass alle Secrets lesbar sind.
- InfluxDB ist Historie zum Auswerten; ihr Backup ist optional und Sache der InfluxDB-Werkzeuge.
- **Schlüssel verloren:** Monika startet nicht. Secrets und TOTP müssen dann neu eingerichtet werden (Secrets neu anlegen, TOTP für alle Benutzer zurücksetzen).

## Rotation des Master-Keys

1. Neuen Schlüssel erzeugen (`openssl rand -base64 32`).
2. `MONIKA_MASTER_KEY` = neu, `MONIKA_MASTER_KEY_PREVIOUS` = alt, Neustart. Monika verschlüsselt beim Start alle Secrets und TOTP-Geheimnisse neu.
3. Nach dem Hinweis im Log „`MONIKA_MASTER_KEY_PREVIOUS` can be removed“ den alten Schlüssel entfernen und neu starten.
4. Den neuen Schlüssel getrennt sichern, den alten erst nach dem nächsten Backup verwerfen.

## Überwachung von Monika selbst

- **Logs** als JSON auf `stdout` (`MONIKA_LOG_FORMAT=json`), vom Container-Runtime z. B. nach Loki.
- **Kennzahlen** schreibt Monika im Takt des Influx-Schreibers in das reservierte Measurement `monika` (ein Punkt pro Takt, ohne Tags). Die Werte der Schleife kommen aus `View.Stats` (`contracts/core.md`), die des Schreibers von ihm selbst, `api_streams` von der API:

| Feld | Typ | Art | Bedeutung |
|---|---|---|---|
| `loop_lag_ms` | int | gauge | Wartezeit der ältesten Nachricht vor der Schleife |
| `loop_queue` | int | gauge | Nachrichten in der Warteschlange der Schleife |
| `sources_live` | int | gauge | Quellen im Zustand `live` |
| `sources_not_live` | int | gauge | aktivierte Quellen, die nicht `live` sind |
| `subscribers` | int | gauge | Abonnenten des Core |
| `subscribers_dropped` | int | counter | abgehängte Abonnenten |
| `events` | int | counter | verteilte Ereignisse |
| `db_ok` | bool | status | letzte Schreibvorgänge in PostgreSQL gelungen |
| `influx_buffered` | int | gauge | gepufferte Takte |
| `influx_dropped` | int | counter | verworfene Takte |
| `api_streams` | int | gauge | offene SSE-Ströme |

- **Erreichbarkeit** von außen über `GET /healthz` (`200` bzw. `503`, wenn die Datenbank nicht erreichbar ist).

## Sizing

Grobe Richtwerte für einige tausend Elemente bei 1 s Takt: 1–2 CPU-Kerne und 1 GB RAM für Monika; der Zustand liegt im Speicher und wächst mit der Zahl der Elemente und Fakten. InfluxDB bekommt den größeren Teil (etwa 3 000 bis 30 000 Werte pro Sekunde, siehe `contracts/influx.md`); der Speicherbedarf hängt vor allem davon ab, wie oft sich Werte ändern.
