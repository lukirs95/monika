# drivers/videoxlink

Treiber für **VideoXLink X4 und X8**. Adapter für [`github.com/lukirs95/goxlinkclient/v5`](https://github.com/lukirs95/goxlinkclient) (v5.0.0). Vertrag dieses Treibers nach `docs/DRIVERS.md`; Remote-Geräte und Stellvertreter: `docs/ARCHITECTURE.md`, Abschnitt 7; Referenzbeispiel: Abschnitt 16.

**Status:** abgestimmt am 2026-10-13.

Unterstützt ist Firmware **1.8.4**. Firmware 1.7 wird nicht unterstützt (keine Unit-Statistik über `localStats.subscribe`).

## Zugang

| | |
|---|---|
| Protokoll | WebSocket `ws://<adresse>/jsonrpc`, JSON-RPC 2.0, über `goxlinkclient` |
| Adresse | `SourceConfig.Address`, `host` oder `host:port` |
| Datenfluss | Event-Stream: nach dem Verbinden der vollständige `System`-Stand (`UpdateSnapshot`), danach ein `Update` pro geändertem, neuem oder entferntem Element; Statistik etwa alle 2 s. Die Konfiguration jeder Unit (`Config`) lädt die Library per `state.subscribe` nach, auch für die Gegenstellen der lokalen XLink-Units |
| `Heartbeat()` | **5 s**; jede Meldung an den `Reporter` zählt. Nach 15 s Stille gilt die Quelle als nicht erreichbar |
| Verbinden | `Run` ruft `Client.Run` in einer Schleife. Endet die Verbindung (auch bei abgelehnter Anmeldung oder `ErrUpdatesLost`): `Disconnected(err)`, Wartezeit 1 s, verdoppelt bis höchstens 30 s, zurückgesetzt nach einer Verbindung, die Live wurde |
| Erster Stand | nach dem `UpdateSnapshot` `Connected()`; der vollständige `Snapshot` wartet bis zu **5 s** auf die erste Statistik und die Konfiguration aller Units, damit die Fakten von Anfang an vollständig sind. Was dann noch fehlt, kommt mit späteren `Update`s |
| Sicherheit | Das Gerät spricht nur unverschlüsseltes WebSocket; Benutzer und Passwort gehen im Klartext über das Netz. Das ist eine Eigenschaft des Geräts, Monika kann sie nicht ändern |

## Identität

| | |
|---|---|
| `Identity` | Vendor `VideoXLink`, Model `X8` bzw. `X4` (Generation aus der System-ID, `SystemID.HardwareGeneration`), Serial = System-ID (`X8A1111`), Firmware = `SystemInfo.Version` |
| `Snapshot.GlobalRef` | `xlink:<System-ID>`, z. B. `xlink:X8A1111` (Namensraum `xlink` gehört diesem Treiber) |

## Elemente

```
X8A1111                         device                    (legt der User an)
└─ XLink-API                    access                    system.cpu_percent, system.uptime_s, system.licenses_* … · reboot, shutdown
   ├─ CPU                       sensor                    system.temperature_c
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
| Temperaturfühler | `sensor/cpu` | `CPU` | fest |
| SDI-Port | `sdi/<n>` | `SDI <n>` | Anzahl aus der Generation der System-ID: `X8…` 8, `X4…` 4; unbekannte Generation: keine Ports, Warn-Log |
| Schnittstelle | `net/<name>` (`net/eth0`) | `<name>` | `System.Interfaces` |
| PTP | `ptp` | `PTP` | nur, wenn `SystemInfo.ST2110` |
| Unit | Suffix der Unit-ID ohne System-ID: `E1`, `D2`, `srtE1`, `srtD1`, `NdiE1`, `NdiD1` | Name auf dem Gerät, leer: Suffix | `Encoders`, `Decoders`, `SRT…`, `NDI…` |
| 2110-Stream | `<unit>/video2110`, `<unit>/audio2110` | `Video 2110`, `Audio 2110` | nur lokale XLink-Encoder und -Decoder, nur wenn `SystemInfo.ST2110` |
| Trunk | Suffix der Trunk-ID: `L2S1` | Name auf dem Gerät, leer: Suffix | `System.Trunks` |
| Peer | `peer/<System-ID>` | `Peer <Name>` bzw. `Peer <System-ID>` | jedes System in `System.Peers` und jedes System, auf dem eine verknüpfte Gegenstelle liegt |
| Stellvertreter | `peer/<System-ID>/<Suffix>` (`peer/X8A9001/E1`) | Unit-ID | `Decoder.Sender` und `Encoder.Receiver` lokaler XLink-Units, deren Gegenstelle auf einem anderen System liegt |

- **`global_ref`** am eigenen Baum: an jeder lokalen Unit `xlink:<Unit-ID>` (`xlink:X8A1111-E1`), am Zugang die des Systems. Nie an Peers und Stellvertretern (`ARCHITECTURE.md`, Abschnitt 7).
- **Die `external_id` der Units enthält die System-ID nicht.** Wird das Gerät getauscht (neue Seriennummer, gleiche Konfiguration), hängen Labels, Regeln und Gruppen weiter an den Units.
- **Attribute** (nur Anzeige): Units `{"id", "card", "connector"}`; Schnittstellen `{"mac", "ip", "mask", "dhcp", "roles"}` (Default External, Backup, LAN (NDI), WebAdmin); Trunks `{"master", "multicast", "encryption", "mtu"}`; Peers `{"name", "version", "ip", "port"}`. Ändert sich ein Attribut (z. B. DHCP), meldet der Treiber einen neuen `Snapshot`.
- Stellvertreter hängen nur unter dem Peer ihres Systems, wenn ein lokaler Encoder oder Decoder mit ihnen verknüpft ist. Wird die Verknüpfung gelöst, verschwinden sie (`present = false`); der Peer bleibt, solange das System konfiguriert ist.

## Fakten

Werte genau vom Go-Typ des Vokabulars (`int` → `int64`, `float` → `float64`); Zeiten auf ganze ms gerundet. Statistik-Fakten (mit †) kommen aus `Stats`, alle anderen aus dem Stand. Fakten aus der Konfiguration einer Unit (mit ◆) fehlen, solange ihre `Config` nicht geladen ist. Zähler (‡) mit `sdk.Counter`, Messzeitpunkt `Stats.Time`; zu jedem Zähler meldet der Treiber auch `<feld>_per_s`.

**Videoformat** aus dem Signaltext des Geräts (`1080i50`, `1080p50`, `720p59.94` …):

| Text | Fakten |
|---|---|
| `<höhe><p\|i><rate>` | `video.height`; `video.width` aus der Höhe (2160 → 3840, 1080 → 1920, 720 → 1280, 576/486/480 → 720); `video.scan` `p` bzw. `i`; `video.frame_rate` = Rate wie im Text (`1080i50` → 50); `video.frame_rate_num`/`_den` = 50/1, bei 59.94 → 60000/1001, 29.97 → 30000/1001, 23.98 → 24000/1001 |
| `No Signal`, leer | keine `video.*`-Fakten aus dem Text |
| unbekanntes Format | keine `video.*`-Fakten aus dem Text, Warn-Log einmal pro Text |

Den Text selbst meldet der Treiber immer als `videoxlink.video_in` bzw. `videoxlink.video_out`. **Dynamikumfang** (`HDR`): `video.transfer` = `SDR`, `PQ` bzw. `HLG`; meldet das Gerät nur „HDR“ ohne Kurve, fehlt `video.transfer`.

**Zugang, Fühler, PTP**

| Element | Fakt | Quelle |
|---|---|---|
| Zugang | `system.cpu_percent` † | `Health.CPU` |
| | `system.uptime_s` † | `Health.Uptime` |
| | `system.licenses_used`, `system.licenses_total` † | `Health.LicensesUsed`, `Health.Licenses` (Video-Lizenzen) |
| | `videoxlink.decoder_licenses_used`, `videoxlink.decoder_licenses_total` † | `Health.DecoderLicensesUsed`, `Health.DecoderLicenses` |
| | `videoxlink.needs_reboot` | `SystemInfo.NeedsReboot` |
| `sensor/cpu` | `system.temperature_c` † | `Health.CPUTemp` |
| `ptp` | `videoxlink.ptp_enabled`, `videoxlink.ptp_sync` † | `Health.PTP`, `Health.PTPSync` |
| `sdi/<n>` | – | das Gerät meldet nichts pro Port; Signale stehen an den Units |

**Schnittstellen**

| Fakt | Quelle |
|---|---|
| `ethernet.link_up` | `Interface.LinkUp` |
| `ethernet.speed_mbps` | `Interface.Speed` (`1Gbps` → 1000, `25Gbps` → 25000, `100Mbps` → 100; `?` → 0) |
| `ethernet.rx_mbps`, `ethernet.tx_mbps` † | `InterfaceStats.RX`, `.TX`; das Gerät meldet nur aktive Schnittstellen, alle anderen 0 |
| `videoxlink.internet`, `videoxlink.gateway_ping` | `Interface.Internet`, `.GatewayPing` |

**XLink-Encoder** (`E<n>` und Stellvertreter `peer/<S>/E<n>`)

| Fakt | lokal | Stellvertreter (`Decoder.Sender`) |
|---|---|---|
| `state.running`, `state.connected` | `Running`, `XLink` (Tunnel steht) | `Running`, `Connected` |
| `state.signal_present`, `video.*`, `videoxlink.video_in`, `videoxlink.audio_in` | Eingang `VideoIn`, `AudioIn`, `HDR` | ebenso |
| `codec.format` | `Codec`: `H264`, `HEVC`, `VC5` | ebenso |
| `codec.bitrate_mbps` | `Bitrate` (Ziel; mit CRF die höchste) | ebenso |
| `videoxlink.p2p` | `P2P` | – |
| ◆ Kodierung | `videoxlink.fec_level` (`FECLevel`), `videoxlink.bit_depth` (`BitDepth`), `videoxlink.crf_enabled`, `videoxlink.crf` (`CRFOn`, `CRF`), `videoxlink.gop_enabled`, `videoxlink.gop_size` (`GOPOn`, `GOP`) | aus `Sender.Config` |
| ◆ ARQ | `videoxlink.ack` (`PacketAck`), `videoxlink.resend` (`Resend`), `videoxlink.max_resends` (`MaxResends`), `videoxlink.resend_delay_ms` (`ResendDelay`), `videoxlink.max_rtt_ms` (`MaxRTT`) | aus `Sender.Config` |
| ◆ Audio | `audio.channels` (`AudioChannels`), `videoxlink.audio_mode` (`uncompressed`, `compressed`), `videoxlink.audio_bitrate_kbps` (`AudioBitrate`, pro Kanal) | aus `Sender.Config` |
| † Statistik | `stream.rtt_ms` (`XLink.RTT`), `stream.packets_retransmitted` ‡ (`XLink.Resent`), `videoxlink.input_fps` (`InputFPS`) | aus `DecoderStats.Sender` |

**XLink-Decoder** (`D<n>` und Stellvertreter `peer/<S>/D<n>`)

| Fakt | lokal | Stellvertreter (`Encoder.Receiver`) |
|---|---|---|
| `state.running`, `state.connected` | `Running`, `XLink` | `Running`, `Connected` |
| `state.signal_present` | empfangenes Signal `VideoIn` | ebenso |
| `video.*` | Ausgang `VideoOut`, `HDR` (was auf SDI bzw. 2110 rausgeht) | ebenso |
| `videoxlink.video_in`, `videoxlink.video_out`, `videoxlink.audio_in`, `videoxlink.audio_out` | Signaltexte | ebenso |
| `reference.locked` | `RefLocked` | ebenso |
| `videoxlink.av_sync`, `videoxlink.av_delay_ms` | `AVSync`, `AVDelay` | ebenso |
| `videoxlink.p2p` | `P2P` | – |
| ◆ `videoxlink.packet_buffer_ms` | `PacketBuffer` | aus `Receiver.Config` |
| † `stream.bitrate_mbps` | `RX` | aus `EncoderStats.Receiver` |
| † `stream.rtt_ms` | `Sender.XLink.RTT` (die Strecke zur Gegenstelle) | – (steht am lokalen Encoder) |
| † `stream.latency_ms` | `Buffer.Delay` (Playout-Verzögerung) | ebenso |
| † `stream.packets_lost` ‡, `stream.packets_dropped` ‡ | `Packets.Missing`, `Packets.LateDropped` | ebenso |
| † `stream.packets_retransmitted` ‡ | `XLink.Resent` | ebenso |
| † `codec.rx_frames` ‡, `codec.frames_dropped` ‡ | `Video.Total`, `Video.Dropped` | ebenso |
| † roh, Zähler ‡ | `videoxlink.packets_late` (`Packets.Late`), `videoxlink.fec_corrected` (`Video.FECCorrected`), `videoxlink.fec_failed` (`Video.FECFailed`), `videoxlink.frames_missing` (`Video.Missing`), `videoxlink.audio_frames_dropped`, `videoxlink.audio_frames_missing`, `videoxlink.audio_unsynced` (`Audio.*`), `videoxlink.slow_decode` (`Packets.SlowDecode`) | ebenso |
| † roh | `videoxlink.buffer_headroom_min_ms` (`Buffer.HeadMin`), `videoxlink.buffer_min_ms` (`Buffer.Min`), `videoxlink.buffer_warning` (`Buffer.Warning`), `videoxlink.decode_fps`, `videoxlink.output_fps` | ebenso |

Nicht abgebildet: `XLink.Drop` und `XLink.ResentDrop` der Decoder sowie die ACK-Zähler der Encoder (Bedeutung unklar), die Werte „im letzten Intervall“ (`…Last`; die Raten bildet `sdk.Counter`) und die Audiopegel pro Kanal (kein Feld im Vokabular).

**2110-Streams** (`<unit>/video2110`, `<unit>/audio2110`)

| Fakt | Quelle |
|---|---|
| `videoxlink.interface` | `Stream.Interface`, ohne Schnittstelle `none` |
| `videoxlink.stream_enabled` | `Stream.Enabled` |
| `stream.dest_address`, `stream.dest_port` | `Stream.Address`, `Stream.Port` (Multicast-Gruppe, beim Encoder die empfangene) |

**SRT** (`srtE<n>`, `srtD<n>`)

| Fakt | Quelle |
|---|---|
| `state.running` | `Running` |
| `state.signal_present`, `video.*` | Encoder: `VideoIn`; Decoder: `state.signal_present` aus `VideoIn`, `video.*` aus `VideoOut` |
| `videoxlink.video_in`, `videoxlink.video_out` | Signaltexte (Encoder nur `video_in`) |
| `codec.bitrate_mbps` | nur Encoder, `Bitrate` |
| `videoxlink.srt_mode`, `videoxlink.srt_address`, `videoxlink.srt_port`, `videoxlink.srt_local_port`, `videoxlink.srt_encryption` | `SRT.Mode` (`listener`, `caller`), `.Address`, `.Port`, `.LocalPort`, `.Encryption`. Roh, weil Adresse und Port je nach Modus und Richtung Gegenstelle oder eigener Port sind |
| † `videoxlink.srt_clients` | `Clients` (verbundene Caller im Listener-Modus; im Caller-Modus 0) |
| † Verbindung | solange die Unit läuft und **genau eine** Verbindung besteht (immer im Caller-Modus, im Listener-Modus mit einem Client): `stream.bitrate_mbps`, `stream.rtt_ms`, `stream.latency_ms`, `stream.packets_lost` ‡, beim Encoder `stream.tx_packets` ‡ und `stream.packets_retransmitted` ‡, beim Decoder `stream.rx_packets` ‡ und `stream.packets_dropped` ‡ |

Die SRT-Passphrase aus der `Config` wird nie Fakt, Attribut, Einstellung oder Log-Eintrag.

**NDI** (`NdiE<n>`, `NdiD<n>`)

| Unit | Fakten |
|---|---|
| `NdiE<n>` | `state.running`, `state.signal_present` und `video.*` aus `VideoIn`, `videoxlink.video_in`, `videoxlink.video_out`; † `videoxlink.ndi_clients` (`Clients`) |
| `NdiD<n>` | `state.running`, `state.connected` (`SourceConnected`), `state.signal_present` aus `VideoIn`, `video.*` aus `VideoOut`, `videoxlink.video_in`, `videoxlink.video_out`, `videoxlink.ndi_source` (`SourceName`), `reference.locked`, `videoxlink.av_sync`, `videoxlink.av_delay_ms`; † `videoxlink.input_fps`, `videoxlink.output_fps` |

**Trunks und Peers**

| Element | Fakten |
|---|---|
| Trunk | `state.running` |
| Peer | `state.connected` (`Connected`), `videoxlink.p2p`, `videoxlink.on_local_net`; † `stream.rtt_ms` (`PeerStats.RTT`) nur, solange das Gerät eine Statistik für das System liefert |

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
| `reboot` | Zugang | keine (`accepted`; die Quelle verbindet danach neu) | **ja** | `Reboot` |
| `shutdown` | Zugang | keine (`accepted`; die Quelle wird unerreichbar) | **ja**: einschalten geht nur vor Ort | `Shutdown` |
| `set` | Elemente mit Einstellungen | aus den Werten (Core) | nein | `Configure…` |

- **Angenommen ≠ bestätigt:** Ein `start` vor dem Verbindungsaufbau einer frisch verknüpften Unit oder ein Decoder-Start ohne freie Decoder-Lizenz nimmt das Gerät an, es wirkt aber nicht. Der Lauf endet dann als `unconfirmed`.
- **Ergebnis:** Antwort des Geräts `accepted`. Ein `*DeviceError` ist `rejected` mit `Detail` (z. B. `video not running`) und `RejectedFields` (Gerätefelder auf Einstellungs-Schlüssel übersetzt, z. B. `vTBR` → `codec.bitrate_mbps`), Antwort `{"code", "message", "detail", "fields"}`. `ErrNotConnected` und Transportfehler sind ein `error` (`failed`).
- `Execute` ist parallel zu `Run` und für verschiedene Elemente gleichzeitig sicher (der `Client` ist es).

## Einstellungen

Video und Audio der Encoder und Decoder, **lokal und an Stellvertretern** (die Library konfiguriert Remote-Units über das lokale System; geprüft wird das Recht am Stellvertreter, `ARCHITECTURE.md` Abschnitt 7). Keine Einstellung ist kritisch.

| Element | Schlüssel | Typ | Library |
|---|---|---|---|
| XLink-Encoder | `codec.format` | `enum` `H264`, `HEVC`, `VC5` | `EncoderCodec` |
| | `codec.bitrate_mbps` | `float`, `min` 1, nur ganze Zahlen; die Obergrenze prüft das Gerät (HEVC höchstens 10) | `EncoderBitrate` |
| | `videoxlink.fec_level` | `int`, 0–3 | `EncoderFEC` |
| | `videoxlink.bit_depth` | `int`, nur 8 oder 10 | `EncoderBitDepth` |
| | `videoxlink.crf_enabled`, `videoxlink.crf` | `bool`, `int` (`min` 0) | `EncoderCRF` |
| | `videoxlink.gop_enabled`, `videoxlink.gop_size` | `bool`, `int` (`min` 1) | `EncoderGOP` |
| | `videoxlink.ack`, `videoxlink.resend` | `bool` | `EncoderPacketAck`, `EncoderResend` |
| | `videoxlink.max_resends` | `int`, `min` 0 | `EncoderMaxResends` |
| | `videoxlink.resend_delay_ms`, `videoxlink.max_rtt_ms` | `int`, `min` 0 | `EncoderResendDelay`, `EncoderMaxRTT` |
| | `audio.channels` | `int`, nur 2, 8 oder 16 | `EncoderAudioChannels` |
| | `videoxlink.audio_mode` | `enum` `uncompressed`, `compressed` | `EncoderAudioMode` |
| | `videoxlink.audio_bitrate_kbps` | `int`, `min` 1 (pro Kanal) | `EncoderAudioBitrate` |
| XLink-Decoder | `videoxlink.packet_buffer_ms` | `int`, `min` 0 | `DecoderPacketBuffer` |
| SRT-Encoder | `codec.bitrate_mbps` | wie oben | `SRTEncoderBitrate` |
| 2110-Stream (nur lokal) | `videoxlink.interface` | `enum`: die 2110-fähigen Schnittstellen der Generation (X8 `eth6`, `eth7`; X4 `eth2`, `eth3`) und `none` | `EncoderVideo2110` usw. |
| | `stream.dest_address` | `ipv4` | |
| | `stream.dest_port` | `int`, 1–65535 | |

- **Ein `set`, ein Request:** Alle Schlüssel eines `set` gehen in einem `Configure…`-Aufruf ans Gerät. Werte, die das Schema nicht ausdrücken kann (nur 8/10, nur 2/8/16, ganze Zahlen), prüft der Treiber und lehnt sie mit `RejectedFields` ab, ohne etwas zu senden.
- **Paare:** CRF und GOP setzt die Library nur mit Schalter und Wert zusammen; ein 2110-Stream geht immer vollständig. Der Treiber ergänzt nicht gesetzte Teile aus der zuletzt gemeldeten `Config` bzw. dem Stand. Ist die `Config` noch nicht geladen, lehnt er solche Einstellungen ab (`Detail`: Konfiguration noch nicht geladen). Eine Schnittstelle schaltet einen 2110-Stream ein, `none` aus (`videoxlink.stream_enabled`).
- **Nebenwirkungen des Geräts** (z. B. HEVC begrenzt die Bitrate, der Empfangspuffer ändert `maxRtt`) zeigen die Fakten; ein dadurch abweichender Wert endet als `unconfirmed`.
- **Nie angeboten:** Wahl des Ein- bzw. Ausgangs (`Card`; Routing, die `feeds`-Links bleiben unveränderbar), `DecoderSender` (ändert `feeds`), SRT-Passphrase (geheim), Netzwerk- und IP-Einstellungen, PTP, Systemname und -ports, Zugangsdaten.

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

- Aus dem `UpdateSnapshot` baut der Treiber Elemente, Links, Fakten und Alarme und meldet sie als `Snapshot`.
- Jedes weitere `Update` der Library betrifft ein Element. Ändert es die Struktur (Element neu oder entfernt, Attribute, Links, Aktionen, Einstellungen), meldet der Treiber einen vollständigen `Snapshot`; sonst ein `Update` mit den geänderten Fakten und Alarmen der betroffenen Elemente (eine Unit samt ihren 2110-Streams und ihrem Stellvertreter).
- Jede Statistik ergibt ein `Update` mit den geänderten Statistik-Fakten.
- Library und Treiber loggen nur über `SourceConfig.Logger`.

## Besonderheiten

- **Wiederverwendete IDs:** Das Gerät vergibt die IDs gelöschter Units neu (`E6` gelöscht, neue Unit wird wieder `E6`). Für Monika ist die neue Unit dasselbe Element wie die alte; Labels und Regeln hängen weiter daran (`ARCHITECTURE.md`, Abschnitt 5). Der Typ steckt in der ID, ein Encoder wird also nie zum Decoder.
- **Verknüpfen nur am Decoder:** Das Gerät verknüpft Units über `sender` am Decoder. Monika ändert Verknüpfungen nicht (`DecoderSender` ist ausgeschlossen).
- **Verlorene Nachrichten:** Erkennt die Library eine Lücke, endet `Run` mit `ErrUpdatesLost`; der Treiber verbindet neu und meldet einen frischen `Snapshot`.
- **`unconfirmed`** ist bei `start` zu erwarten, wenn die XLink-Verbindung nach dem Verknüpfen noch nicht steht oder keine Decoder-Lizenz frei ist.

## Katalog

Rohe Fakten (`Driver.Facts`; nie in InfluxDB):

| Fakt | Typ | Art |
|---|---|---|
| `videoxlink.needs_reboot`, `videoxlink.ptp_enabled`, `videoxlink.ptp_sync`, `videoxlink.internet`, `videoxlink.gateway_ping`, `videoxlink.p2p`, `videoxlink.on_local_net`, `videoxlink.stream_enabled`, `videoxlink.srt_encryption`, `videoxlink.av_sync`, `videoxlink.crf_enabled`, `videoxlink.gop_enabled`, `videoxlink.ack`, `videoxlink.resend` | bool | status |
| `videoxlink.buffer_warning` | bool | status (Erkennungsflag) |
| `videoxlink.video_in`, `videoxlink.video_out`, `videoxlink.audio_in`, `videoxlink.audio_out`, `videoxlink.interface`, `videoxlink.srt_mode`, `videoxlink.srt_address`, `videoxlink.ndi_source`, `videoxlink.audio_mode` | string | status |
| `videoxlink.fec_level`, `videoxlink.bit_depth`, `videoxlink.crf`, `videoxlink.gop_size`, `videoxlink.max_resends`, `videoxlink.resend_delay_ms`, `videoxlink.max_rtt_ms`, `videoxlink.packet_buffer_ms`, `videoxlink.audio_bitrate_kbps`, `videoxlink.srt_port`, `videoxlink.srt_local_port` | int | status |
| `videoxlink.decoder_licenses_used`, `videoxlink.decoder_licenses_total`, `videoxlink.av_delay_ms`, `videoxlink.buffer_headroom_min_ms`, `videoxlink.buffer_min_ms`, `videoxlink.srt_clients`, `videoxlink.ndi_clients` | int | gauge |
| `videoxlink.input_fps`, `videoxlink.decode_fps`, `videoxlink.output_fps` | float | gauge |
| `videoxlink.packets_late`, `videoxlink.fec_corrected`, `videoxlink.fec_failed`, `videoxlink.frames_missing`, `videoxlink.audio_frames_dropped`, `videoxlink.audio_frames_missing`, `videoxlink.audio_unsynced`, `videoxlink.slow_decode` | int | counter |

Aktionen (`Driver.Actions`): `start`, `stop`, `set`, `reset_counters`, `reboot`, `shutdown`, `videoxlink.reset_video_buffer`, `videoxlink.flush_audio`.

## Tests

- **Attrappe des Geräts** im Testpaket: ein WebSocket-Server mit JSON-RPC (Anmeldung, `systems.full`, `systems.update`, `systems.stats`, `systems.localStats`, `state.subscribe`/`state.update`, `config`, `start`, `stop`, `reboot` …), gefüttert mit den Mitschnitten der Library (Firmware 1.8) unter `testdata/`. Der Test schaltet Zustände um (Signal, Verknüpfung, Unit neu und gelöscht, Konfiguration geändert, Verbindung bricht ab, Anmeldung abgelehnt).
- **`sdktest.Conformance`** gegen die Attrappe.
- Table-driven: Zerlegung der Signaltexte, Zuordnung der Fakten samt Einheiten, Elementbaum und Links (X8 und X4, mit und ohne ST 2110, Schleife, Stellvertreter), `set` mit Ergänzung der Paare und Übersetzung abgelehnter Felder, Secret und Passphrase nie im Log.
