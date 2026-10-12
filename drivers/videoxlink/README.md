# drivers/videoxlink

Treiber für **VideoXLink X4 und X8**. Adapter für [`github.com/lukirs95/goxlinkclient/v4`](https://github.com/lukirs95/goxlinkclient) (v4.0.1). Vertrag dieses Treibers nach `docs/DRIVERS.md`; Remote-Geräte und Stellvertreter: `docs/ARCHITECTURE.md`, Abschnitt 7; Referenzbeispiel: Abschnitt 16.

**Status:** abgestimmt am 2026-10-12.

Unterstützt ist Firmware **1.8.4**. Firmware 1.7 liefert keine Unit-Statistik (`localStats.subscribe` schlägt fehl); der Treiber läuft dann ohne die Statistik-Fakten (Warn-Log der Library).

## Zugang

| | |
|---|---|
| Protokoll | WebSocket `ws://<adresse>/jsonrpc`, JSON-RPC 2.0, über `goxlinkclient` |
| Adresse | `SourceConfig.Address`, `host` oder `host:port` |
| Datenfluss | Event-Stream: ein vollständiger `System`-Stand nach jeder Änderung (`systems.update` kommt etwa alle 5 s, auch ohne echte Änderung), Statistik etwa alle 2 s |
| `Heartbeat()` | **5 s**; jede Meldung an den `Reporter` zählt. Nach 15 s Stille gilt die Quelle als nicht erreichbar |
| Verbinden | `Run` ruft `Client.Run` in einer Schleife. Endet die Verbindung (auch bei abgelehnter Anmeldung): `Disconnected(err)`, Wartezeit 1 s, verdoppelt bis höchstens 30 s, zurückgesetzt nach einer Verbindung, die Live wurde |
| Erster Stand | nach dem ersten `System`-Stand `Connected()`; der vollständige `Snapshot` wartet bis zu **5 s** auf die erste Statistik, damit Health-Fakten (CPU, Temperatur, Lizenzen, RTT) von Anfang an dabei sind. Kommt keine, geht der `Snapshot` ohne sie raus |
| Sicherheit | Das Gerät spricht nur unverschlüsseltes WebSocket; Benutzer und Passwort gehen im Klartext über das Netz. Das ist eine Eigenschaft des Geräts, Monika kann sie nicht ändern |

## Identität

| | |
|---|---|
| `Identity` | Vendor `VideoXLink`, Model `X8` bzw. `X4` (Generation aus der System-ID, `SystemID.HardwareGeneration`), Serial = System-ID (`X8A1111`), Firmware = `System.Version` |
| `Snapshot.GlobalRef` | `xlink:<System-ID>`, z. B. `xlink:X8A1111` (Namensraum `xlink` gehört diesem Treiber) |

## Elemente

```
X8A1111                         device                    (legt der User an)
└─ XLink-API                    access                    system.cpu_percent, system.uptime_s, system.licenses_*, videoxlink.needs_reboot …
   ├─ CPU, System               sensor                    system.temperature_c
   ├─ SDI 1 … SDI 8             port · sdi · bidir        (X4: SDI 1 … 4)
   ├─ eth0 … eth7               network_interface · ethernet
   ├─ PTP                       clock · ptp               (nur mit ST 2110)
   ├─ E1                        sender · xlink · out      XLink-Encoder
   │  ├─ Video 2110             receiver · st2110-20 · in (nur mit ST 2110)
   │  └─ Audio 2110             receiver · st2110-30 · in
   ├─ D2                        receiver · xlink · in     XLink-Decoder
   │  ├─ Video 2110             sender · st2110-20 · out  (nur mit ST 2110)
   │  └─ Audio 2110             sender · st2110-30 · out
   ├─ srtE1 / srtD1             sender · srt · out / receiver · srt · in
   ├─ NdiE1 / NdiD1             sender · ndi · out / receiver · ndi · in
   ├─ L2S1                      tunnel · xlink            Layer-2-Trunk
   └─ Peer X8A9001              peer · xlink              Sicht auf ein Remote-System
      ├─ X8A9001-E1             sender · xlink            Stellvertreter (Gegenstelle eines lokalen Decoders)
      └─ X8A9001-D3             receiver · xlink          Stellvertreter (Gegenstelle eines lokalen Encoders)
```

| Element | `external_id` | `name` | Herkunft |
|---|---|---|---|
| Zugang | `""` | – | System |
| Temperaturfühler | `sensor/cpu`, `sensor/system` | `CPU`, `System` | fest |
| SDI-Port | `sdi/<n>` | `SDI <n>` | Anzahl aus der Generation der System-ID: `X8…` 8, `X4…` 4; unbekannte Generation: keine Ports, Warn-Log |
| Schnittstelle | `net/<name>` (`net/eth0`) | `<name>` | `System.Interfaces` |
| PTP | `ptp` | `PTP` | nur, wenn `System.ST2110` |
| Unit | Suffix der Unit-ID ohne System-ID: `E1`, `D2`, `srtE1`, `srtD1`, `NdiE1`, `NdiD1` | Name auf dem Gerät, leer: Suffix | `Encoders`, `Decoders`, `SRT…`, `NDI…` |
| 2110-Stream | `<unit>/video2110`, `<unit>/audio2110` | `Video 2110`, `Audio 2110` | nur XLink-Encoder und -Decoder, nur wenn `System.ST2110` |
| Trunk | Suffix der Trunk-ID: `L2S1` | Name auf dem Gerät, leer: Suffix | `System.Trunks` |
| Peer | `peer/<System-ID>` | `Peer <Name>` bzw. `Peer <System-ID>` | jedes System in `System.Peers` und jedes System, auf dem eine verknüpfte Gegenstelle liegt |
| Stellvertreter | `peer/<System-ID>/<Suffix>` (`peer/X8A9001/E1`) | Unit-ID | `Decoder.Sender` und `Encoder.Receiver` lokaler XLink-Units, deren Gegenstelle auf einem anderen System liegt |

- **`global_ref`** am eigenen Baum: an jeder lokalen Unit `xlink:<Unit-ID>` (`xlink:X8A1111-E1`), am Zugang die des Systems. Nie an Peers und Stellvertretern (`ARCHITECTURE.md`, Abschnitt 7).
- **Die `external_id` der Units enthält die System-ID nicht.** Wird das Gerät getauscht (neue Seriennummer, gleiche Konfiguration), hängen Labels, Regeln und Gruppen weiter an den Units.
- **Attribute** (nur Anzeige): Units `{"id", "card"}`; Schnittstellen `{"mac", "ip", "mask", "dhcp", "roles"}` (Default External, Backup, LAN (NDI), WebAdmin); Trunks `{"master", "multicast", "encryption", "mtu"}`; Peers `{"name", "version", "ip", "port"}`. Ändert sich ein Attribut (z. B. DHCP), meldet der Treiber einen neuen `Snapshot`.
- Stellvertreter hängen nur unter dem Peer ihres Systems, wenn ein lokaler Encoder oder Decoder mit ihnen verknüpft ist. Wird die Verknüpfung gelöst, verschwinden sie (`present = false`); der Peer bleibt, solange das System konfiguriert ist.

## Fakten

Werte genau vom Go-Typ des Vokabulars (`int` → `int64`, `float` → `float64`). Statistik-Fakten (mit †) kommen aus `Stats`, alle anderen aus dem `System`-Stand. Zähler (‡) mit `sdk.Counter`, Messzeitpunkt `Stats.Time`; zu jedem Zähler meldet der Treiber auch `<feld>_per_s`.

**Videoformat** aus dem Signaltext des Geräts (`1080i50`, `1080p50`, `720p59.94` …):

| Text | Fakten |
|---|---|
| `<höhe><p\|i><rate>` | `video.height`; `video.width` aus der Höhe (2160 → 3840, 1080 → 1920, 720 → 1280, 576/486/480 → 720); `video.scan` `p` bzw. `i`; `video.frame_rate` = Rate wie im Text (`1080i50` → 50); `video.frame_rate_num`/`_den` = 50/1, bei 59.94 → 60000/1001, 29.97 → 30000/1001, 23.98 → 24000/1001 |
| `No Signal`, leer | keine `video.*`-Fakten |
| unbekanntes Format | keine `video.*`-Fakten, Warn-Log einmal pro Text |

Den Text selbst meldet der Treiber immer als `videoxlink.video_in` bzw. `videoxlink.video_out`.

**Zugang und Fühler**

| Element | Fakt | Quelle |
|---|---|---|
| Zugang | `system.cpu_percent` † | `Health.CPU` |
| | `system.uptime_s` † | `Health.Uptime` |
| | `system.licenses_used`, `system.licenses_total` † | `Health.LicensesUsed`, `Health.Licenses` (Video-Lizenzen) |
| | `videoxlink.decoder_licenses_used`, `videoxlink.decoder_licenses_total` † | `Health.DecoderLicensesUsed`, `Health.DecoderLicenses` |
| | `videoxlink.needs_reboot` | `System.NeedsReboot` |
| `sensor/cpu` | `system.temperature_c` † | `Health.CPUTemp` |
| `sensor/system` | `system.temperature_c` † | `Health.SystemTemp` |
| `ptp` | `videoxlink.ptp_enabled`, `videoxlink.ptp_sync`, `videoxlink.ptp_sync_local` † | `Health.PTP`, `.PTPSync`, `.PTPSyncLocal` |
| `sdi/<n>` | – | das Gerät meldet nichts pro Port; Signale stehen an den Units |

**Schnittstellen**

| Fakt | Quelle |
|---|---|
| `ethernet.link_up` | `Interface.LinkUp` |
| `ethernet.speed_mbps` | `Interface.Speed` (`1Gbps` → 1000, `25Gbps` → 25000, `100Mbps` → 100; `?` → 0) |
| `ethernet.rx_mbps`, `ethernet.tx_mbps` † | `InterfaceStats.RX`, `.TX`; das Gerät meldet nur aktive Schnittstellen, alle anderen 0 |
| `videoxlink.internet`, `videoxlink.gateway_ping` | `Interface.Internet`, `.GatewayPing` |

**XLink-Encoder** (`E<n>`)

| Fakt | Quelle |
|---|---|
| `state.running` | `Encoder.Running` |
| `state.connected` | `Encoder.XLink` (Tunnel zum Empfänger steht) |
| `state.signal_present` | Eingang `VideoIn` liegt an |
| `video.*`, `videoxlink.video_in`, `videoxlink.audio_in` | `VideoIn`, `AudioIn` |
| `codec.format` | `Codec`: `H264`, `HEVC`, `VC5` |
| `codec.bitrate_mbps` | `Bitrate` (Ziel) |
| `videoxlink.fec_level` | `FECLevel` |
| `videoxlink.p2p` | `P2P` |
| `stream.rtt_ms` † | `XLink.RTT`, auf ganze ms gerundet (im LAN oft 0) |
| `stream.packets_retransmitted` †‡ | `XLink.Resent` |
| `videoxlink.input_fps` † | `InputFPS` |

**XLink-Decoder** (`D<n>`)

| Fakt | Quelle |
|---|---|
| `state.running` | `Decoder.Running` |
| `state.connected` | `Decoder.XLink` |
| `state.signal_present` | empfangenes Signal `VideoIn` liegt an |
| `video.*` | Ausgang `VideoOut` (was auf SDI bzw. 2110 rausgeht) |
| `videoxlink.video_in`, `videoxlink.video_out`, `videoxlink.audio_in`, `videoxlink.audio_out` | Signaltexte |
| `audio.channels` | `AudioChannels` |
| `videoxlink.p2p` | `P2P` |
| `stream.bitrate_mbps` † | `RX` |
| `stream.rtt_ms` † | `XLink.RTT`, gerundet |
| `stream.rx_packets` †‡ | `Receive.Received` |
| `stream.packets_lost` †‡ | `Receive.Lost` |
| `stream.packets_dropped` †‡ | `Receive.Late` |
| `stream.packets_retransmitted` †‡ | `XLink.Resent` |
| `stream.jitter_ns` † | `Receive.JitterAvg` |
| `stream.latency_ms` † | `Receive.Delay`, gerundet |
| `codec.frames_dropped` †‡ | `Video.Dropped` |
| `videoxlink.fec_failed` †‡, `videoxlink.frames_missing` †‡, `videoxlink.audio_frames_dropped` †‡ | `Video.FECFailed`, `Video.Missing`, `Audio.Dropped` |
| `videoxlink.buffer_ms` †, `videoxlink.buffer_min_ms` †, `videoxlink.buffer_warning` † | `Buffer.Size`, `.Min`, `.Warning` |
| `videoxlink.receive_health_percent` † | `Receive.Health` |
| `videoxlink.output_fps` † | `OutputFPS` |

**2110-Streams** (`<unit>/video2110`, `<unit>/audio2110`)

| Fakt | Quelle |
|---|---|
| `videoxlink.interface` | `Stream.Interface`, ohne Schnittstelle `none` |
| `videoxlink.stream_enabled` | `Stream.Enabled` |
| `stream.dest_address`, `stream.dest_port` | `Stream.Address`, `Stream.Port` (Multicast-Gruppe, beim Encoder die empfangene) |

**SRT und NDI**

| Unit | Fakten |
|---|---|
| `srtE<n>` | `state.running`, `state.signal_present` und `video.*` aus `VideoIn`, `videoxlink.video_in`, `codec.bitrate_mbps`, SRT-Verbindung (unten) |
| `srtD<n>` | `state.running`, `state.signal_present` aus `VideoIn`, `video.*` aus `VideoOut`, `videoxlink.video_in`, `videoxlink.video_out`, SRT-Verbindung |
| SRT-Verbindung | `videoxlink.srt_mode` (`listener`, `caller`), `videoxlink.srt_address`, `videoxlink.srt_port`, `videoxlink.srt_local_port`, `videoxlink.srt_encryption`. Roh, weil Adresse und Port je nach Modus und Richtung Gegenstelle oder eigener Port sind |
| `NdiE<n>` | `state.running`, `state.signal_present` und `video.*` aus `VideoIn`, `videoxlink.video_in`, `videoxlink.video_out` |
| `NdiD<n>` | `state.running`, `state.connected` (`SourceConnected`), `state.signal_present` aus `VideoIn`, `video.*` aus `VideoOut`, `videoxlink.video_in`, `videoxlink.video_out`, `videoxlink.ndi_source` (`SourceName`) |

Für SRT- und NDI-Units liefert das Gerät keine Statistik.

**Trunks, Peers, Stellvertreter**

| Element | Fakten |
|---|---|
| Trunk | `state.running` |
| Peer | `state.connected` (`Peer.Connected`), `videoxlink.p2p`, `videoxlink.on_local_net`; `stream.rtt_ms` † (`PeerStats.RTT`, gerundet) nur, solange das Gerät eine Statistik für das System liefert |
| Stellvertreter eines Encoders | aus `Decoder.Sender`: `state.running`, `state.connected`, `state.signal_present`, `video.*` und `videoxlink.video_in` aus `Video`; aus `DecoderStats.Sender` †: `stream.rtt_ms`, `stream.packets_retransmitted` ‡, `videoxlink.input_fps` |
| Stellvertreter eines Decoders | aus `Encoder.Receiver`: `state.running`, `state.connected`, `state.signal_present`, `video.*` und `videoxlink.video_out` aus `Video`; aus `EncoderStats.Receiver` †: dieselben Statistik-Fakten wie ein lokaler Decoder |

Nicht abgebildet, weil die Bedeutung unklar ist: `XLink.Drop` und `XLink.ResentDrop`.

## Alarme

| Element | Alarm | Severity |
|---|---|---|
| `srtE<n>`, `srtD<n>` | `srt_error`, Meldung = `Error` des Geräts, solange es einen meldet | `error` |

Weitere Alarme meldet das Gerät nicht. Bewertungen (PTP-Sync, Temperatur, Lizenzen, `videoxlink.needs_reboot`, `videoxlink.buffer_warning`) laufen über Regeln.

## Aktionen

| Aktion | an | Prüfbedingung | kritisch | Library |
|---|---|---|---|---|
| `start` | alle lokalen Units, Stellvertreter | `state.running eq true`, 15 s | nein | `Start` |
| `stop` | alle lokalen Units, Stellvertreter | `state.running eq false`, 15 s | nein | `Stop` |
| `start`, `stop` | Trunks | wie oben | **ja**: ein Trunk trägt oft Steuer- und Management-Verkehr der Remote-Seite | `StartTrunk`, `StopTrunk` |
| `reset_counters` | lokale XLink-Encoder und -Decoder | keine | nein | `ResetStats` |
| `videoxlink.reset_video_buffer` | lokale XLink-Encoder | keine | nein | `ResetVideoBuffer`; gestoppt lehnt das Gerät ab |
| `videoxlink.flush_audio` | lokale XLink-Decoder | keine | nein | `FlushAudio`; gestoppt lehnt das Gerät ab |
| `set` | Elemente mit Einstellungen | aus den Werten (Core) | je Einstellung | `Configure…` |

- **Angenommen ≠ bestätigt:** Ein `start` vor dem Verbindungsaufbau einer frisch verknüpften Unit oder ein Decoder-Start ohne freie Decoder-Lizenz nimmt das Gerät an, es wirkt aber nicht. Der Lauf endet dann als `unconfirmed`.
- **`reboot` und `shutdown`** folgen, sobald `goxlinkclient` sie kann (dann am Zugang, immer kritisch).
- **Ergebnis:** Antwort des Geräts `accepted`. Ein `*DeviceError` ist `rejected` mit `Detail` (z. B. `video not running`) und `RejectedFields` (Gerätefelder auf Einstellungs-Schlüssel übersetzt, z. B. `vTBR` → `codec.bitrate_mbps`), Antwort `{"code", "message", "detail", "fields"}`. `ErrNotConnected` und Transportfehler sind ein `error` (`failed`).
- `Execute` ist parallel zu `Run` und für verschiedene Elemente gleichzeitig sicher (der `Client` ist es).

## Einstellungen

Am Anfang nur das Nötigste für Video und Audio der Encoder und Decoder:

| Element | Schlüssel | Typ | Library |
|---|---|---|---|
| XLink-Encoder | `codec.format` | `enum` `H264`, `HEVC`, `VC5` | `EncoderCodec` |
| | `codec.bitrate_mbps` | `float`, `min` 1, nur ganze Zahlen (sonst `rejected`); die Obergrenze prüft das Gerät (HEVC höchstens 10) | `EncoderBitrate` |
| SRT-Encoder | `codec.bitrate_mbps` | wie oben | `SRTEncoderBitrate` |
| 2110-Stream (Encoder und Decoder, Video und Audio) | `videoxlink.interface` | `enum`: die 2110-fähigen Schnittstellen der Generation (X8 `eth6`, `eth7`; X4 `eth2`, `eth3`) und `none` | `EncoderVideo2110` usw. |
| | `stream.dest_address` | `ipv4` | |
| | `stream.dest_port` | `int`, 1–65535 | |

- Keine Einstellung ist kritisch.
- **2110:** Die Library setzt einen Stream immer vollständig. Der Treiber ergänzt die nicht gesetzten Felder aus dem letzten Stand; eine Schnittstelle schaltet den Stream ein, `none` aus (`videoxlink.stream_enabled`).
- **Nie angeboten:** Wahl des SDI-Ein- bzw. -Ausgangs (Routing; die `feeds`-Links bleiben unveränderbar), `DecoderSender` (ändert `feeds`), SRT-Passphrase (geheim), Netzwerk- und IP-Einstellungen, PTP, Systemname und -ports, Zugangsdaten.
- Audio-Einstellungen der XLink-Encoder (Modus, Kanäle, Bitrate) stehen nicht im `System`-Stand; ohne lesbaren Wert gibt es keine Prüfung. Sie kommen, sobald die Library sie liefert.

## Links

| Link | von | nach | Herkunft |
|---|---|---|---|
| `uses` | Encoder-Unit (XLink, SRT, NDI) | `sdi/<n>` | Eingang (`Card` 1–8); vom Gerät wählbar |
| `feeds` | Decoder-Unit (XLink, SRT, NDI) | `sdi/<n>` | Ausgang (`Card` 1–8); Monika bietet die Wahl nicht an |
| `uses` | 2110-Stream | `net/<eth>` | `Stream.Interface`, nur wenn gesetzt |
| `uses` | Trunk | `net/<eth>` | `Trunk.Interface` |
| `uses` | XLink-Unit | `peer/<System-ID>` | Gegenstelle auf einem anderen System |
| `feeds` | Stellvertreter `peer/<S>/E<n>` | lokaler Decoder | `Decoder.Sender` |
| `feeds` | lokaler Encoder | Stellvertreter `peer/<S>/D<n>` | `Encoder.Receiver` |
| `feeds` | lokaler Encoder | lokaler Decoder | beide auf diesem System (Schleife); nur einmal gemeldet, aus `Decoder.Sender` |
| `represents` | Peer | `xlink:<System-ID>` | immer per `GlobalRef` |
| `represents` | Stellvertreter | `xlink:<Unit-ID>` | immer per `GlobalRef` |

Jede Quelle verlinkt nur in ihren eigenen Baum; den Bezug zum Original stellt `represents` her. Sind beide Geräte in Monika, gibt es so keine doppelten `feeds` (`ARCHITECTURE.md`, Abschnitt 7).

## Konfiguration und Secret

- **`config`:** leeres Objekt, `{"type": "object", "additionalProperties": false}`. Die Adresse steht in der Quelle.
- **Secret** (Pflicht): `{"username": "…", "password": "…"}`. Fehlt es oder ist es kein gültiges JSON mit beiden Feldern, lehnt `New` die Quelle ab (ohne den Inhalt zu nennen). Der Inhalt erscheint nie in Logs, Fehlern oder Fakten.

## Meldungen

- Der Treiber baut aus jedem `System`-Stand Elemente, Links, Fakten und Alarme. Unterscheiden sich Elemente (samt Attributen, Aktionen, Einstellungen) oder Links vom zuletzt gemeldeten Stand, meldet er einen vollständigen `Snapshot`, sonst ein `Update` mit den geänderten Fakten und Alarmen.
- Jede Statistik ergibt ein `Update` mit den geänderten Statistik-Fakten.
- Library und Treiber loggen nur über `SourceConfig.Logger`.

## Besonderheiten

- **Wiederverwendete IDs:** Das Gerät vergibt die IDs gelöschter Units neu (`E6` gelöscht, neue Unit wird wieder `E6`). Für Monika ist die neue Unit dasselbe Element wie die alte; Labels und Regeln hängen weiter daran (`ARCHITECTURE.md`, Abschnitt 5). Der Typ steckt in der ID, ein Encoder wird also nie zum Decoder.
- **Verknüpfen nur am Decoder:** Das Gerät verknüpft Units über `sender` am Decoder. Monika ändert Verknüpfungen nicht (`DecoderSender` ist ausgeschlossen).
- **Lücken in `systems.update`:** Verliert die Library Deltas, kann der Stand bis zum nächsten Verbinden unvollständig sein (Warn-Log der Library).
- **`unconfirmed`** ist bei `start` zu erwarten, wenn die XLink-Verbindung nach dem Verknüpfen noch nicht steht oder keine Decoder-Lizenz frei ist.

## Katalog

Rohe Fakten (`Driver.Facts`; nie in InfluxDB):

| Fakt | Typ | Art |
|---|---|---|
| `videoxlink.needs_reboot`, `videoxlink.ptp_enabled`, `videoxlink.ptp_sync`, `videoxlink.ptp_sync_local`, `videoxlink.internet`, `videoxlink.gateway_ping`, `videoxlink.p2p`, `videoxlink.on_local_net`, `videoxlink.stream_enabled`, `videoxlink.srt_encryption` | bool | status |
| `videoxlink.buffer_warning` | bool | status (Erkennungsflag) |
| `videoxlink.video_in`, `videoxlink.video_out`, `videoxlink.audio_in`, `videoxlink.audio_out`, `videoxlink.interface`, `videoxlink.srt_mode`, `videoxlink.srt_address`, `videoxlink.ndi_source` | string | status |
| `videoxlink.fec_level`, `videoxlink.srt_port`, `videoxlink.srt_local_port` | int | status |
| `videoxlink.decoder_licenses_used`, `videoxlink.decoder_licenses_total`, `videoxlink.input_fps`, `videoxlink.output_fps`, `videoxlink.buffer_ms`, `videoxlink.buffer_min_ms` | int | gauge |
| `videoxlink.receive_health_percent` | float | gauge |
| `videoxlink.fec_failed`, `videoxlink.frames_missing`, `videoxlink.audio_frames_dropped` | int | counter |

Aktionen (`Driver.Actions`): `start`, `stop`, `set`, `reset_counters`, `videoxlink.reset_video_buffer`, `videoxlink.flush_audio`.

## Tests

- **Attrappe des Geräts** im Testpaket: ein WebSocket-Server mit JSON-RPC (Anmeldung, `systems.full`, `systems.update`, `systems.stats`, `systems.localStats`, `config`, `start`, `stop` …), gefüttert mit den Mitschnitten der Library (Firmware 1.8) unter `testdata/`. Der Test schaltet Zustände um (Signal, Verknüpfung, Unit neu und gelöscht, Verbindung bricht ab, Anmeldung abgelehnt).
- **`sdktest.Conformance`** gegen die Attrappe.
- Table-driven: Zerlegung der Signaltexte, Zuordnung der Fakten samt Einheiten, Elementbaum und Links (X8 und X4, mit und ohne ST 2110, Schleife, Stellvertreter), `set` mit Übersetzung abgelehnter Felder, Secret nie im Log.
