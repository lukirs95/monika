# Treiber schreiben

Leitfaden für alle, die ein Gerät an Monika anbinden. Er setzt kein Wissen über bestimmte Hersteller voraus und führt an einem **erfundenen Gerät** durch alle Schritte. Verbindlich sind die Verträge: [`contracts/sdk.md`](contracts/sdk.md) (Schnittstelle) und [`contracts/vocabulary.md`](contracts/vocabulary.md) (Namen, Typen, Einheiten). Das *Warum* steht in [`ARCHITECTURE.md`](ARCHITECTURE.md), Abschnitt 4 und 5.

## Was ein Treiber tut – und was nicht

Ein Treiber ist ein **dünner Übersetzer** zwischen der Gerätelibrary und Monika. Er meldet, was das Gerät sieht, und führt aus, was Monika verlangt.

| Der Treiber … | Der Treiber nie … |
|---|---|
| meldet Elemente, Fakten, Alarme und Links | bewertet („Fehler“, „ok“); er meldet `state.signal_present: false` |
| meldet nach jedem Verbinden den **vollständigen** Stand | bildet Differenzen; das macht der Core |
| gibt Lebenszeichen und nennt ihren Abstand | entscheidet, ob die Quelle „erreichbar“ ist |
| führt Befehle aus und liefert die Antwort des Geräts | prüft, ob ein Befehl gewirkt hat; das macht der Core über die Fakten |
| verbindet bei Abbruch selbst neu | ändert seine eigene Konfiguration |
| | bietet Management-Adresse oder Zugangsdaten als Einstellung an |
| | spricht Elemente oder Links anderer Quellen an |
| | importiert etwas aus `internal/` |

## Das Beispielgerät: Acme GW-4

Ein erfundenes SDI-zu-ST-2110-Gateway, so wie es viele gibt:

- 4 SDI-Eingänge; jeder speist einen **ST-2110-20-Sender**, redundant nach **ST 2022-7** über zwei Media-Schnittstellen (A und B).
- 2 Media-Schnittstellen mit steckbaren **SFPs**, dazu eine Management-Schnittstelle.
- **PTP** als Takt.
- 2 Lüfter, 2 Netzteile, Temperaturfühler auf Board und FPGA.
- **REST-API** zum Abfragen und Steuern, ein **WebSocket** für Änderungen und Alarme, Anmeldung mit Benutzer und Passwort.
- Die Seriennummer ist eindeutig; eine global eindeutige ID für einzelne Sender gibt es nicht.

Der Treiber heißt `acme` (Paket `drivers/acme`).

## Schritt 1: Paket und Registrierung

- Paket `drivers/acme`, Treibername = Paketname, nur `[a-z0-9]`. Der Name darf kein Measurement des Vokabulars sein (also nicht `ptp`, `sdi` …).
- Registrierung in `init()` mit `sdk.Register(sdk.Driver{…})`: Name, Konfigurationsschema, Aktionskatalog, Fakt-Katalog und die Fabrik `New`.
- Eine Zeile Import in `drivers/all`.
- Die Gerätelibrary (z. B. `acmeclient`) ist ein eigenes Repository ohne Abhängigkeit zu Monika und nimmt einen `*slog.Logger` als Option an.

## Schritt 2: Das Gerät in Elemente zerlegen

**Ein Element ist alles, was einen eigenen Zustand, eigene Aktionen oder eigene Regeln haben kann.** Was nur beschreibt, ist ein Attribut.

```
Acme GW-4 (Gerät, legt der User an)
└─ ACME-API                 access              system.* für das ganze Gerät (Uptime, CPU) · reboot
   ├─ SDI 1 … 4             port · sdi · in     state.signal_present, sdi.*, video.*, timecode.*
   ├─ Sender 1 … 4          sender · st2110-20  state.running, stream.*, st2110.* · start, stop, set
   │  ├─ Pfad A             path · st2110-20    state.connected, stream.packets_lost
   │  └─ Pfad B             path · st2110-20    state.connected, stream.packets_lost
   ├─ Media A, Media B      network_interface · ethernet   ethernet.*
   │  └─ SFP                module              sfp.*
   ├─ Management            network_interface · ethernet   ethernet.*
   ├─ PTP                   clock · ptp          ptp.*
   ├─ Lüfter 1, 2           module              system.fan_speed_rpm
   ├─ Netzteil 1, 2         module              system.psu_ok, system.voltage_v, system.power_w
   ├─ Board, FPGA           sensor              system.temperature_c
```

- **Wurzel ist der Zugang.** Elemente ohne Eltern meldet der Treiber mit `ParentID ""`; sie hängen am Zugangs-Element seiner Quelle. Das Gerät selbst meldet er nie.
- **`kind` ist die Rolle, `transport` der Weg** (`vocabulary.md`). Ein Sender, der SDI in ST 2110 wandelt, ist `sender` mit `transport = st2110-20`, kein „Gateway“.
- **Ein Messpunkt ist ein Element.** Zwei Temperaturen sind zwei `sensor`, zwei Lüfter zwei `module`. Nie `temperature_board_c` und `temperature_fpga_c` am selben Element.
- **Redundante Pfade** (ST 2022-7) sind Kinder des Senders (`kind = path`).
- **Steckbares** (SFP) ist ein eigenes `module` unter der Schnittstelle, damit Ziehen und Stecken als Verschwinden und Wiederkehren sichtbar wird.
- Gibt es mehrere Wege zum selben Gerät (z. B. später IPMI), ist das eine zweite Quelle mit eigenem Treiber und eigenem Teilbaum, nicht ein Teil dieses Treibers.

## Schritt 3: Stabile `external_id`s

Monika erkennt ein Element nach einem Neustart oder Wiederverbinden nur an seiner `external_id` wieder. Labels, Regeln, Gruppen und Presets hängen daran.

| Element | `external_id` | warum |
|---|---|---|
| SDI 2 | `in/2` | Nummer auf dem Gehäuse |
| Sender 3 | `sender/3` | fester Index des Geräts |
| Pfad B von Sender 3 | `sender/3/path/b` | Pfad + Eltern |
| Media A | `nic/media-a` | Name der Schnittstelle |
| SFP in Media A | `nic/media-a/sfp` | Steckplatz, nicht Seriennummer des SFP |
| Lüfter 2 | `fan/2` | Steckplatz |
| FPGA-Fühler | `sensor/fpga` | Bezeichnung des Geräts |

- **Nie** eine laufende Nummer aus der Reihenfolge der Discovery, nie eine ID, die das Gerät bei jedem Start neu vergibt.
- Der **Steckplatz**, nicht das gesteckte Teil: Ein getauschtes SFP ist dasselbe Element mit neuen Attributen (Hersteller, Seriennummer).
- Vergibt ein Gerät gelöschte IDs neu, ist das so; dokumentiere es im README des Treibers (`ARCHITECTURE.md`, Abschnitt 5, wiederverwendete IDs).

**`global_ref`** nur, wenn das Gerät eine wirklich global eindeutige ID hat, und nur in einem Namensraum, der dem Treiber gehört (`vocabulary.md`). Der GW-4 hat keine; seine Seriennummer steht in der Identität der Quelle (`Snapshot.Identity`).

## Schritt 4: Fakten

Jeder Wert aus der API wird einem Fakt des Vokabulars zugeordnet: `<measurement>.<feld>`.

| API des GW-4 | Element | Fakt | Typ |
|---|---|---|---|
| `inputs[2].locked` | `in/2` | `state.signal_present` | `bool` |
| `inputs[2].standard = "1080i50"` | `in/2` | `video.width` = 1920, `video.height` = 1080, `video.frame_rate` = 50, `video.frame_rate_num` = 50, `video.frame_rate_den` = 1, `video.scan` = `i` | `int64`, `float64`, `string` |
| `inputs[2].crcCount` | `in/2` | `sdi.crc_errors` und `sdi.crc_errors_per_s` | `int64`, `float64` |
| `senders[3].enabled` | `sender/3` | `state.running` | `bool` |
| `senders[3].mcast` | `sender/3` | `stream.dest_address` | `string` |
| `senders[3].legs.b.lost` | `sender/3/path/b` | `stream.packets_lost` und `_per_s` | `int64`, `float64` |
| `ptp.offset` (in µs!) | `ptp` | `ptp.offset_ns` = Wert × 1000 | `int64` |
| `sfp.rxPower` (in mW!) | `nic/media-a/sfp` | `sfp.rx_power_dbm` = 10·log₁₀(mW) | `float64` |
| `fpgaBuild` | Zugang | `acme.fpga_build` (roh) | `string` |

- **Einheit umrechnen**, nie die des Geräts übernehmen. Die Einheit steht im Feldnamen.
- **Genau der Go-Typ des Vokabulars:** `int`-Felder als `int64`, `float`-Felder als `float64`. Ein `float64` in einem `int`-Feld verwirft der Core.
- **Zähler** mit `sdk.Counter` (ein `Counter` pro Zähler und Element; Messzeitpunkt `at` vom Gerät, sonst `time.Now()`): Rohwert und Rate melden. Liefert das Gerät nur eine Rate, nur diese.
- **Nichts weglassen.** Ein Fakt, den das Element grundsätzlich hat, wird immer gemeldet, auch als `false` oder `0`. Ein fehlender Fakt beendet Befunde (`fact_missing`).
- **Rohe Fakten** nur für wirklich Herstellerspezifisches, mit Präfix `acme.` und im Fakt-Katalog (`Driver.Facts`). Sie gehen nie nach InfluxDB. Gibt es einen passenden Begriff im Vokabular, ist er Pflicht; fehlt einer, der für Broadcast allgemein sinnvoll ist, wird das Vokabular erweitert (Änderung am SDK mit Review).
- **Fakten für das ganze Gerät** (Uptime, CPU, Lizenzen) gehören an den Zugang (`external_id ""`).

**`Snapshot` und `Update`:** Nach jedem `Connected()` ein vollständiger `Snapshot` (alle Elemente, Links, Fakten, Alarme). Danach nur `Update`s mit geänderten Fakten. Ändert sich die Struktur (SFP gezogen, Sender hinzugefügt), wieder ein vollständiger `Snapshot`.

## Schritt 5: Lebenszeichen

- Der GW-4 wird alle 2 s abgefragt (`SourceConfig.Scheduler.Every`) und schickt Änderungen über den WebSocket. **`Source.Heartbeat()` = 2 s**: Jede erfolgreiche Abfrage und jede Nachricht zählt als Lebenszeichen. Bleiben sie dreimal so lange aus, gilt die Quelle als nicht erreichbar.
- Wer nur einen Event-Stream hat, ruft `Reporter.Heartbeat()`, wenn längere Zeit nichts zu melden ist (Keepalive).
- `Heartbeat() = 0` nur für Sende-Ziele ohne Health-Check.
- Bricht die Verbindung ab: `Disconnected(err)`, selbst neu verbinden (mit eigener Wartezeit), dann `Connected()` und wieder ein vollständiger `Snapshot`.
- `Run` kehrt zügig zurück, wenn `ctx` abgebrochen wird, und schließt alle Verbindungen.

## Schritt 6: Alarme

Meldet das Gerät eigene Alarme („PSU 2 failure“), gibt der Treiber sie pro Element als **vollständige Liste** weiter (`Snapshot.Alarms`, `Update.Alarms`); eine neue Liste ersetzt die alte. Monika macht daraus Befunde und beendet sie, wenn der Alarm fehlt. Der Treiber erfindet keine Alarme aus Fakten; das ist Sache von Regeln.

## Schritt 7: Aktionen und Einstellungen

- **Katalog** (`Driver.Actions`): jede Aktion einmal, mit Parametern, Prüfbedingung, Timeout und `Critical`. Kern-Aktionen behalten ihre Bedeutung (`start` → `state.running eq true`).
- **Pro Element** (`Element.Actions`): welche Aktionen es hat. Der GW-4: `start`, `stop`, `set` an den Sendern; `reboot` am Zugang (immer kritisch); roh `acme.reset_ptp` am PTP-Element.
- **Einstellungen** (`Element.Settings`): Schlüssel = Fakt-Schlüssel, mit Typ, Grenzen, `Critical`, ggf. `WriteOnly`. Der GW-4: `stream.dest_address` und `stream.dest_port` an den Sendern, `ptp.domain` am PTP-Element (kritisch).
- **Nie** als Einstellung: Management-Adresse, Passwörter, Schlüssel.
- **`Execute`:** Parameter prüfen, dann ans Gerät. Antwort `Accepted`, `Rejected` (mit abgelehnten Feldern) oder ein `error`, wenn das Gerät nicht erreichbar war. Ob es gewirkt hat, zeigt der nächste `Update`, nicht die Antwort.
- `Execute` muss parallel zu `Run` und für verschiedene Elemente gleichzeitig funktionieren.
- **Keine Aktion, die `feeds`-Links ändert** (Kreuzschienen-Grenze). `uses` dürfen sich ändern.

## Schritt 8: Links

| Link | GW-4 |
|---|---|
| `feeds` | `in/2` → `sender/2`: beim GW-4 fest verdrahtet, jeder Eingang speist genau seinen Sender |
| `uses` | `sender/2/path/a` → `nic/media-a`, `sender/2/path/b` → `nic/media-b` |
| `represents` | – (nur, wenn der Treiber Stellvertreter fremder Elemente zeigt; Ziel immer per `global_ref`) |

- **`feeds` oder `uses`?** Fester Signalfluss ist `feeds`. Ließe der GW-4 per Einstellung wählen, welchen Eingang ein Sender nimmt, wäre es `uses` von `sender/2` auf den gewählten Eingang; `feeds` darf keine Aktion ändern (Kreuzschienen-Grenze).
- Links über Gerätegrenzen meldet die Seite, die sie konfiguriert, mit `Endpoint.GlobalRef` auf das fremde Element.

## Schritt 9: Konfiguration und Secret

- **`ConfigSchema`** (JSON-Schema) für `device_source.config`, z. B. `{"port": 443, "tls": true}`. Die UI baut daraus das Formular. Keine Zugangsdaten in `config`.
- **Secret** als JSON in `SourceConfig.Secret`, Format legt der Treiber fest und dokumentiert es: `{"username": "…", "password": "…"}`. Nie loggen.
- `SourceConfig.Address` ist die Management-Adresse; sie gehört dem User.

## Schritt 10: Logging

Nur über `SourceConfig.Logger`. `Debug` für Protokolldetails, `Info` für Verbinden und Trennen, `Warn` für Fehler, die der Treiber selbst behebt, `Error` für alles andere. Keine Geheimnisse, auch nicht in Fehlermeldungen der Library.

## Schritt 11: Testen

- **Attrappe des Geräts** im Testpaket: ein `httptest.Server`, der die REST-API und den WebSocket nachbildet, mit Zuständen, die der Test umschaltet (Signal weg, SFP gezogen, Verbindung bricht ab).
- **`sdktest.Conformance(t, driver, cfg)`** gegen die Attrappe: prüft die Regeln des SDK-Vertrags automatisch.
- **`sdktest.Reporter`** für eigene Tests: Zuordnung der Fakten (table-driven, z. B. µs → ns, mW → dBm), Struktur nach dem Ziehen eines SFP, Alarme, `Execute` mit `Accepted` und `Rejected`.
- Nie echte Hardware in `make test`.

## Typische Fehler

| Fehler | Folge | richtig |
|---|---|---|
| `external_id` aus der Reihenfolge der API | nach einem Neustart hängen Labels und Regeln am falschen Element | feste Nummern, Namen oder Steckplätze |
| Fakt weglassen, wenn kein Signal da ist | Befund wird zurückgezogen statt gemeldet | `state.signal_present: false` |
| Einheit des Geräts übernehmen | `ptp.offset_ns` ist 1000-fach falsch | umrechnen in die Einheit des Feldnamens |
| `float64` für ein `int`-Feld | Wert wird verworfen | Go-Typ aus dem Vokabular |
| zwei Temperaturen an einem Element | zweiter Wert überschreibt den ersten | je Messpunkt ein Element |
| eigenes Feld statt Vokabular (`acme.rx_power`) | keine Zeitreihe, keine herstellerübergreifende Regel | `sfp.rx_power_dbm` |
| `Update` vor dem ersten `Snapshot` | wird verworfen | erst `Connected`, dann vollständiger `Snapshot` |
| bewerten im Treiber (`acme.ok`) | doppelte, nicht konfigurierbare Logik | Fakten melden, Regeln bewerten |
| Management-IP als Einstellung | Quelle nach dem Setzen unerreichbar | nie anbieten |

## Checkliste vor dem Merge

- [ ] Name `[a-z0-9]+`, kein Measurement-Name; Paket unter `drivers/`, Import in `drivers/all`; nichts aus `internal/`.
- [ ] Elementbaum: Rollen nach `vocabulary.md`, Messpunkte und Pfade als Elemente, Steckbares als `module`.
- [ ] `external_id`s stabil über Neustarts; `global_ref` nur mit eigenem Namensraum.
- [ ] Alle Werte auf das Vokabular abgebildet, Einheiten umgerechnet, Go-Typen exakt; Zähler mit Rate; rohe Fakten mit Präfix im Katalog.
- [ ] `Heartbeat()` passt zum Abfragetakt bzw. Keepalive.
- [ ] Nach jedem `Connected` ein vollständiger `Snapshot`; Strukturänderungen als `Snapshot`.
- [ ] Aktionen im Katalog mit Prüfbedingung und `Critical`; keine Management-Adresse, keine Geheimnisse als Einstellung; keine Aktion ändert `feeds`.
- [ ] `ConfigSchema` vorhanden, Format des Secrets dokumentiert, nichts Geheimes im Log.
- [ ] Tests gegen eine Attrappe, `sdktest.Conformance` grün.
- [ ] `drivers/<name>/README.md`: Gerät, unterstützte Firmware, Elementbaum, `external_id`-Schema, rohe Fakten, Aktionen, Format von Konfiguration und Secret, Besonderheiten (z. B. wiederverwendete IDs).
