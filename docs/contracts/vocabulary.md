# Vertrag: Vokabular

**Status:** abgestimmt am 2026-10-11. **Paket:** `sdk/vocab`. **Hintergrund:** `docs/ARCHITECTURE.md`, Abschnitt 2 (Prinzip 9), 4, 5, 8 und 14; [`sdk.md`](sdk.md), [`influx.md`](influx.md).

Das Vokabular ist die gemeinsame Sprache von Treibern, Core, Regeln, UI und Zeitreihen. Es lebt in Go (`sdk/vocab`); in der Datenbank ist alles `text`, neue Werte brauchen keine Migration, aber eine Änderung am SDK mit Review. Wer ein Gerät anbindet, ordnet alles, was er meldet, einem Eintrag hier zu.

## Grundsätze

- **Ein Vokabular für Fakten und Zeitreihen.** Der Fakt-Schlüssel ist `<measurement>.<feld>`, z. B. `ptp.offset_ns`. Derselbe Name steht in Regeln, in der UI und in InfluxDB (Measurement `ptp`, Feld `offset_ns`). Es gibt keine Abbildung dazwischen.
- **Das Measurement sagt „wovon“, das Feld „was“.** Gleiche Größen heißen überall gleich: `temperature_c` gibt es in `system` und `sfp`, `offset_ns` in `reference`, `ptp` und `st2110`.
- **Drei Stufen:** **Kern** ist das Measurement `state` (einheitliche Bedeutung für generische UI, Sammelaktionen und Gruppenregeln); **bekannt** sind alle anderen Measurements hier; **roh** sind herstellerspezifische Fakten mit dem Treibernamen als Präfix (`videoxlink.p2p`), die nie nach InfluxDB gehen.
- **Ein Messpunkt ist ein Element.** Gibt es eine Größe an einem Ding mehrfach (mehrere Temperaturen, Lüfter, Netzteile, die Pfade eines redundanten Streams), wird jedes Vorkommen ein eigenes Kind-Element. Es gibt keinen Tag für Messpunkte.
- **Bewertung gehört nicht ins Vokabular.** Monika bewertet über Regeln. Ausnahme sind Zustände, die das Gerät selbst meldet (`system.psu_ok`, `st2110.compliant`), wie bei Gerätealarmen.

## Namen

| Regel | Beispiele |
|---|---|
| Measurements und Felder in `snake_case`, Measurements ohne Punkt | `stream`, `st2022_7`, `packets_lost` |
| **Einheit als Endung**, wo eine Einheit existiert | `_ns`, `_ms`, `_s`, `_hz`, `_rpm`, `_c`, `_v`, `_w`, `_ma`, `_mv`, `_m`, `_dbm`, `_mbps`, `_percent`, `_ui` |
| Alles unter einer Millisekunde in `_ns` | Offsets, Laufzeiten, Jitter, Paketzeit |
| **Richtung als Präfix** `rx_` bzw. `tx_`, sobald es die Größe pro Richtung gibt; Bezug ist das Element selbst | `rx_bytes`, `tx_power_dbm` |
| Ereignisse `packets_*` bzw. `frames_*` | `packets_lost`, `frames_dropped` |
| Fehlerzähler enden auf `_errors` | `crc_errors`, `sync_loss_errors` |
| **Booleans: `true` ist der Gutzustand.** Ausnahme sind Erkennungsflags, deren Name den schlechten Zustand nennt | `link_up`, `valid`; Ausnahmen `rx_los`, `tx_fault`, `black`, `freeze`, `silence` |
| Feld nie gleich dem Measurement | `codec.format`, nicht `codec.codec` |

## Typen und Arten

| Typ | Go | InfluxDB | gilt für |
|---|---|---|---|
| `bool` | `bool` | Boolean | Zustände |
| `int` | `int64` | Integer (`i`) | `_ns`, `_ms`, `_s`, `_hz`, `_rpm`, Zähler, Anzahlen (`channels`, `bit_depth`, `height`, `width`) |
| `float` | `float64` | Float | `_mbps`, `_c`, `_v`, `_w`, `_ma`, `_mv`, `_m`, `_dbm`, `_percent`, `_ui`, `frame_rate`, Raten `_per_s` |
| `string` | `string` | String | Zustände und Formate als Text |

| Art | Bedeutung |
|---|---|
| `status` | Zustand oder Format |
| `gauge` | Messwert |
| `counter` | Zähler (`int`, nur steigend bis zum Reset). Zu jedem Zähler gibt es den Fakt `<feld>_per_s` (`float`, pro Sekunde). Ein Treiber meldet mindestens eines von beiden; meldet er den Zähler, meldet er auch die Rate (`sdk.Counter`). Ob ein Wert ein Zähler ist, sagt die Art, nicht der Name |

- **Historisiert** (nach InfluxDB) wird jedes Feld dieses Vokabulars, außer es ist unten ausdrücklich ausgenommen. Raten `_per_s` werden nur historisiert, wenn das Element den zugehörigen Zähler nicht meldet; sonst bildet das Werkzeug die Rate aus dem Zähler.
- Regeln auf Zähler: „gehalten“ mit `<zähler> eq 0` (bis jemand zurücksetzt), „aktuell“ mit `<zähler>_per_s eq 0` und `hold_seconds` (`docs/ARCHITECTURE.md` Abschnitt 8).

## Element-Arten (`kind`)

`kind` beschreibt die **Rolle** eines Elements, nicht seine Technik; die Technik steht in `transport`.

| `kind` | Bedeutung | Abgrenzung | typische Measurements |
|---|---|---|---|
| `device` | Gerät | **reserviert**, nur der Core | – (nur Health und Rollup) |
| `group` | Gruppe | **reserviert**, nur der Core | – (nur Health und Rollup) |
| `access` | Zugang einer Quelle | **reserviert**, nur der Core; Fakten für das ganze Gerät meldet der Treiber mit leerer `external_id` | `system`, `ptp` |
| `module` | austauschbare oder steckbare Hardware | trägt selbst kein Signal; Lüfter, Netzteile und SFPs sind `module` | `state`, `system`, `sfp` |
| `port` | physischer Anschluss für Signale | ein Stream ist kein Port | `state`, `sdi`, `video`, `audio`, `aes3`, `reference`, `timecode`, `gpio`, `tally` |
| `network_interface` | Netzwerk-Schnittstelle | trägt Streams über `uses` | `ethernet` |
| `clock` | Taktquelle oder -empfänger | | `state`, `ptp`, `reference` |
| `sender` | erzeugt einen Stream oder ein Signal | „sendet weg“ | `state`, `stream`, `st2110`, `codec`, `video`, `audio` |
| `receiver` | nimmt einen Stream entgegen | „empfängt her“ | `state`, `stream`, `st2022_7`, `tr101290`, `codec`, `video`, `audio` |
| `peer` | Sicht des Geräts auf eine Gegenstelle | keine Health-Daten der Gegenstelle, nur die Verbindung | `state`, `stream` |
| `path` | ein Pfad eines redundanten Streams (ST 2022-7) | Kind des Senders bzw. Empfängers, `uses` auf seine Schnittstelle | `state`, `stream` |
| `tunnel` | transparente Verbindung ohne eigenes Signalformat | | `state`, `stream` |
| `processing` | verändert ein Signal im Gerät | weder Ein- noch Ausgang | `state`, `video`, `audio` |
| `sensor` | Messpunkt ohne eigene Funktion | ein Lüfter ist `module`, ein Temperaturfühler `sensor` | `system` |
| `trigger` | konfigurierter Aufruf eines Sende-Ziels | nur Aktion `trigger` | `call` |

## Transporte (`transport`)

`transport` beschreibt den **Weg** eines Signals: Schnittstelle bzw. Protokoll. Proprietäre Protokolle sind Transporte wie offene. Codec, Level und Kanalzahl sind Fakten, kein eigener Transport.

| Wert | Bereich | Beschreibung | Norm / Herkunft |
|---|---|---|---|
| `sdi` | Video, Basisband | Serial Digital Interface, Level als Fakt `sdi.level` | SMPTE ST 259 / 292 / 424 / 2081 / 2082 / 2083 |
| `hdmi` | Video, Basisband | inkl. Embedded Audio, ARC/eARC | HDMI Forum / HDMI LA |
| `dvi` | Video, Basisband | digitales Monitorsignal | DDWG |
| `displayport` | Video, Basisband | auch USB-C Alt Mode | VESA |
| `hdbaset` | Video, Basisband | Punkt-zu-Punkt über Cat-Kabel | HDBaseT Alliance, IEEE 1911 |
| `composite` | Video, Basisband | CVBS, analog | SMPTE 170M, ITU-R BT.1700 |
| `svideo` | Video, Basisband | Y/C, analog | – |
| `component` | Video, Basisband | YPbPr / RGBHV, analog | CEA-770, SMPTE 274M |
| `analog` | Audio, Basisband | Line/Mic, symmetrisch oder unsymmetrisch | IEC 60268 |
| `aes3` | Audio, Basisband | 110 Ω, XLR, 2 Kanäle | AES3, IEC 60958-4 |
| `aes3id` | Audio, Basisband | 75 Ω, BNC, 2 Kanäle | AES-3id, SMPTE ST 276 |
| `spdif` | Audio, Basisband | Consumer, Koax oder TOSLINK | IEC 60958-3 |
| `aes42` | Audio, Basisband | digitale Mikrofone mit Fernsteuerung | AES42 |
| `madi` | Audio, Basisband | 56/64 Kanäle, Koax oder Glasfaser | AES10 |
| `adat` | Audio, Basisband | 8 Kanäle @ 48 kHz, S/MUX für höhere Raten | Alesis, de facto |
| `aes50` | Audio, Basisband | 48 Kanäle @ 48 kHz über Cat-Kabel | AES50 |
| `tdif` | Audio, Basisband | 8 Kanäle | Tascam, proprietär |
| `ultranet` | Audio, Layer 2 | 16 Kanäle, Monitoring | Behringer, proprietär |
| `optocore` | Audio, Layer 2 | Glasfaser-Ring | Optocore, proprietär |
| `rocknet` | Audio, Layer 2 | Ring über Cat-Kabel | Riedel, proprietär |
| `ethersound` | Audio, Layer 2 | Ethernet-basiert | Digigram, proprietär |
| `cobranet` | Audio, Layer 2 | Ethernet-basiert | Cirrus Logic, proprietär |
| `anet` | Audio, Layer 2 | Personal Monitoring | Aviom, proprietär |
| `aes67` | Audio über IP | Interop-Standard, RTP/PTP | AES67 |
| `dante` | Audio über IP | AES67-Modus optional | Audinate, proprietär |
| `ravenna` | Audio über IP | AES67-kompatibel | ALC NetworX / Lawo |
| `milan` | Audio über IP | AVB-Profil, Layer 2 | Avnu Alliance, IEEE 802.1 |
| `livewire` | Audio über IP | Radio-Umfeld | Telos/Axia |
| `st2022-6` | Video über IP | unkomprimiertes SDI gekapselt | SMPTE ST 2022-6 |
| `ndi` | Video über IP | LAN, komprimiert, inkl. NDI HX | Vizrt/NDI |
| `st2110-20` | ST 2110 | unkomprimiertes Video | SMPTE ST 2110-20 |
| `st2110-22` | ST 2110 | komprimiertes Video (z. B. JPEG XS) | SMPTE ST 2110-22 |
| `st2110-30` | ST 2110 | Audio PCM | SMPTE ST 2110-30 |
| `st2110-31` | ST 2110 | AES3 transparent | SMPTE ST 2110-31 |
| `st2110-40` | ST 2110 | Ancillary Data | SMPTE ST 2110-40 |
| `st2110-41` | ST 2110 | Fast Metadata | SMPTE ST 2110-41 |
| `asi` | Transport Stream | DVB-ASI, MPEG-TS auf 75 Ω | EN 50083-9 |
| `st2022-2` | Transport Stream | MPEG-TS über IP | SMPTE ST 2022-2 |
| `xlink` | Contribution, WAN | Übertragungsprotokoll der VideoXLink-Geräte | VideoXLink, proprietär |
| `srt` | Contribution, WAN | Secure Reliable Transport | SRT Alliance (Haivision) |
| `rist` | Contribution, WAN | Reliable Internet Stream Transport | VSF TR-06 |
| `rtmp` | Contribution, WAN | Streaming-Ingest | Adobe, de facto |
| `zixi` | Contribution, WAN | proprietär | Zixi |
| `whip` | Contribution, WAN | WebRTC-Ingest | IETF RFC 9725 |
| `ethernet` | Netzwerk | generischer Netzwerkport | IEEE 802.3 |
| `ptp` | Takt | Precision Time Protocol | IEEE 1588, SMPTE ST 2059 |
| `blackburst` | Takt | analoger SD-Referenztakt | SMPTE ST 318 |
| `trilevel` | Takt | HD-Referenztakt | SMPTE ST 274 / 296 |
| `wordclock` | Takt | Audio-Sampletakt | de facto |
| `dars` | Takt | Digital Audio Reference Signal | AES11 |
| `ltc` | Timecode | Longitudinal Timecode | SMPTE ST 12-1 |
| `4wire` | Intercom | getrennte Sprech- und Hörleitung | – |
| `2wire` | Intercom | Partyline (Clear-Com, RTS) | herstellerspezifisch |
| `rs422` | Steuerung | Sony 9-Pin, VTR/Server-Steuerung | EIA-422 |
| `rs232` | Steuerung | serielle Steuerung | EIA-232 |
| `gpio` | Steuerung | GPI/GPO, Tally, Kontakte | – |

## Richtung (`direction`)

`in`, `out`, `bidir`: die **Fähigkeit** eines Anschlusses. Die aktuelle Richtung eines `bidir`-Ports ist der Fakt `state.current_direction`.

## Kern: `state`

Generische UI, Sammelaktionen und Gruppenregeln über Hersteller und Transporte hinweg stützen sich nur hierauf.

| Feld | Typ | Art | Bedeutung |
|---|---|---|---|
| `running` | bool | status | Unit bzw. Stream ist gestartet (Prüfbedingung von `start`/`stop`) |
| `signal_present` | bool | status | das **eigene** Signal des Elements liegt an (SDI am Port, Referenz am Referenzeingang, Stream am Empfänger). Was im Signal steckt, steht in der Domäne (`timecode.present`, `video.black`) |
| `connected` | bool | status | Verbindung zur Gegenstelle steht (Peer, SRT, RIST, NDI, Pfad) |
| `reachable` | bool | status | ein Teil hinter der Quelle antwortet (Karte im Rahmen); eine gezogene Karte ist dagegen verschwunden (`present = false`) |
| `current_direction` | string | status | `in` oder `out` bei einem `bidir`-Port |

## Bekannt: Domänen

| Measurement | Feld | Typ | Art | Bedeutung | Norm / Herkunft |
|---|---|---|---|---|---|
| `system` | `temperature_c` | float | gauge | Temperatur des Geräts, Moduls oder Fühlers | |
| | `fan_speed_rpm` | int | gauge | Lüfterdrehzahl | |
| | `power_w` | float | gauge | Leistungsaufnahme | |
| | `voltage_v` | float | gauge | Spannung, z. B. eines Netzteils | |
| | `cpu_percent` | float | gauge | CPU-Auslastung | |
| | `memory_percent` | float | gauge | Speicherauslastung | |
| | `disk_percent` | float | gauge | Belegung des Datenträgers | |
| | `uptime_s` | int | gauge | Laufzeit seit dem letzten Start | |
| | `psu_ok` | bool | status | Netzteil in Ordnung, wie das Gerät es meldet | |
| | `licenses_used` | int | gauge | belegte Lizenzen | |
| | `licenses_total` | int | gauge | vorhandene Lizenzen | |
| `reference` | `locked` | bool | status | auf die Referenz eingerastet | |
| | `height` | int | status | Zeilenzahl des Referenzformats | |
| | `frame_rate` | float | status | Bildrate für Abfragen | |
| | `frame_rate_num` | int | status | Bildrate exakt, Zähler | |
| | `frame_rate_den` | int | status | Bildrate exakt, Nenner | |
| | `scan` | string | status | `p`, `i` oder `psf` | |
| | `offset_ns` | int | gauge | Timing-Versatz zur Referenz | |
| `timecode` | `present` | bool | status | Timecode erkannt (LTC, VITC, ATC) | SMPTE ST 12-1 |
| | `drop_frame` | bool | status | Drop-Frame-Zählung | |
| `sdi` | `level` | string | status | `SD`, `HD`, `3G-A`, `3G-B`, `6G`, `12G`, `24G` | |
| | `payload_id` | string | status | Payload Identifier (hex) | SMPTE ST 352 |
| | `amplitude_mv` | float | gauge | Signalamplitude, nominal 800 mV | |
| | `cable_length_m` | float | gauge | geschätzte Kabellänge | |
| | `timing_jitter_ui` | float | gauge | Timing-Jitter | SMPTE RP 184 |
| | `alignment_jitter_ui` | float | gauge | Alignment-Jitter | SMPTE RP 184 |
| | `crc_errors` | int | counter | CRC-Fehler, bei SD EDH | SMPTE ST 292-1, RP 165 |
| | `anc_checksum_errors` | int | counter | ANC-Prüfsummenfehler | SMPTE ST 291-1 |
| `video` | `width` | int | status | aktive Pixel pro Zeile | |
| | `height` | int | status | aktive Zeilen | |
| | `frame_rate` | float | status | Bildrate für Abfragen | |
| | `frame_rate_num` | int | status | Bildrate exakt, Zähler | |
| | `frame_rate_den` | int | status | Bildrate exakt, Nenner | |
| | `scan` | string | status | `p`, `i` oder `psf` | |
| | `sampling` | string | status | z. B. `YCbCr-4:2:2`, `RGB` | SMPTE ST 2110-20 |
| | `bit_depth` | int | status | Bit pro Komponente | |
| | `colorimetry` | string | status | `BT601`, `BT709`, `BT2020`, `BT2100` | |
| | `transfer` | string | status | `SDR`, `HLG`, `PQ` | SMPTE ST 2110-20 (`TCS`) |
| | `range` | string | status | `NARROW`, `FULL`, `FULLPROTECT` | |
| | `afd` | int | status | Active Format Description | SMPTE ST 2016-1 |
| | `captions_present` | bool | status | Untertitel CEA-608/708 bzw. OP-47 im ANC | |
| | `black` | bool | status | Schwarzbild erkannt (Erkennungsflag) | |
| | `freeze` | bool | status | Standbild erkannt (Erkennungsflag) | |
| | `gamut_errors` | int | counter | Gamut-Verletzungen | EBU R 103 |
| `audio` | `sample_rate_hz` | int | status | Abtastrate | |
| | `bit_depth` | int | status | Bit pro Sample | |
| | `channels` | int | status | Anzahl Kanäle | |
| | `channel_order` | string | status | z. B. `SMPTE2110.(ST,51)` | SMPTE ST 2110-30 |
| | `encoding` | string | status | `PCM`, `DOLBY_E`, `AC3`, `EAC3` | SMPTE ST 338 |
| | `packet_time_ns` | int | status | Paketzeit bei IP-Audio, z. B. `1000000` | |
| | `silence` | bool | status | Stille unterhalb der Schwelle erkannt (Erkennungsflag) | |
| `aes3` | `valid` | bool | status | Daten gültig, Validity-Bit nicht gesetzt | AES3 |
| | `crc_errors` | int | counter | CRC-Fehler im Channel-Status-Block | AES3 |
| `ethernet` | `link_up` | bool | status | Link aktiv | |
| | `speed_mbps` | float | gauge | ausgehandelte Geschwindigkeit | |
| | `rx_bytes`, `tx_bytes` | int | counter | empfangene bzw. gesendete Bytes | |
| | `rx_packets`, `tx_packets` | int | counter | empfangene bzw. gesendete Pakete | |
| | `rx_errors`, `tx_errors` | int | counter | Empfangsfehler inkl. FCS bzw. Sendefehler | |
| | `rx_discards`, `tx_discards` | int | counter | verworfene Pakete | |
| `sfp` | `rx_los` | bool | status | Loss of Signal (Erkennungsflag) | SFF-8472 |
| | `tx_fault` | bool | status | Laser-Fehler (Erkennungsflag) | SFF-8472 |
| | `temperature_c` | float | gauge | Modultemperatur | SFF-8472 |
| | `voltage_v` | float | gauge | Versorgungsspannung | SFF-8472 |
| | `tx_bias_ma` | float | gauge | Laser-Bias-Strom | SFF-8472 |
| | `tx_power_dbm` | float | gauge | optische Sendeleistung | SFF-8472 |
| | `rx_power_dbm` | float | gauge | optische Empfangsleistung | SFF-8472 |
| `ptp` | `state` | string | status | Port-Zustand: `initializing`, `faulty`, `disabled`, `listening`, `pre_master`, `master`, `passive`, `uncalibrated`, `slave` | IEEE 1588 |
| | `offset_ns` | int | gauge | Abweichung zur Master-Uhr | IEEE 1588 |
| | `path_delay_ns` | int | gauge | mittlere Pfadlaufzeit | IEEE 1588 |
| | `grandmaster_id` | string | status | Clock Identity des Grandmasters | IEEE 1588 |
| | `clock_class` | int | status | Clock Class des Grandmasters | IEEE 1588 |
| | `domain` | int | status | PTP-Domäne | IEEE 1588 |
| | `steps_removed` | int | gauge | Hops bis zum Grandmaster | IEEE 1588 |
| `stream` | `bitrate_mbps` | float | gauge | gemessene Datenrate | |
| | `rx_packets`, `tx_packets` | int | counter | empfangene bzw. gesendete Pakete | RFC 3550 |
| | `packets_lost` | int | counter | verlorene Pakete | RFC 3550 |
| | `packets_out_of_order` | int | counter | Pakete außer Reihenfolge | |
| | `packets_retransmitted` | int | counter | Neuübertragungen (SRT, RIST, XLink) | |
| | `packets_recovered` | int | counter | per ARQ bzw. FEC wiederhergestellt | |
| | `packets_dropped` | int | counter | zu spät eingetroffen, verworfen | |
| | `jitter_ns` | int | gauge | Interarrival-Jitter | RFC 3550 |
| | `rtt_ms` | int | gauge | Round Trip Time | |
| | `latency_ms` | int | gauge | ausgehandelte bzw. gemessene Latenz | |
| | `bandwidth_mbps` | float | gauge | geschätzte verfügbare Bandbreite | |
| | `dest_address` | string | status | Ziel- bzw. Multicast-Adresse | |
| | `dest_port` | int | status | Zielport | |
| `st2110` | `offset_ns` | int | gauge | Versatz RTP-Zeitstempel zu PTP | SMPTE ST 2110-10 |
| | `sender_type` | string | status | `2110TPN`, `2110TPNL` oder `2110TPW` | SMPTE ST 2110-21 |
| | `cinst_max` | int | gauge | Maximum C_INST | SMPTE ST 2110-21 |
| | `vrx_max` | int | gauge | Maximum VRX | SMPTE ST 2110-21 |
| | `compliant` | bool | status | Timing-Modell eingehalten, wie das Gerät es meldet | SMPTE ST 2110-21 |
| `st2022_7` | `packets_unrecovered` | int | counter | auf beiden Pfaden verloren | SMPTE ST 2022-7 |
| | `path_skew_ms` | int | gauge | Laufzeitdifferenz der Pfade | SMPTE ST 2022-7 |
| `tr101290` | `sync_loss_errors` | int | counter | TS-Sync-Verlust (P1) | ETSI TR 101 290 |
| | `cc_errors` | int | counter | Continuity-Count-Fehler (P1) | ETSI TR 101 290 |
| | `pat_errors` | int | counter | PAT-Fehler (P1) | ETSI TR 101 290 |
| | `pmt_errors` | int | counter | PMT-Fehler (P1) | ETSI TR 101 290 |
| | `pcr_accuracy_errors` | int | counter | PCR-Genauigkeit außerhalb ±500 ns (P2) | ETSI TR 101 290 |
| `codec` | `format` | string | status | z. B. `H264`, `HEVC`, `JPEGXS` | |
| | `bitrate_mbps` | float | gauge | Zielbitrate des Codecs | |
| | `latency_ms` | int | gauge | Codec-Latenz | |
| | `rx_frames` | int | counter | empfangene Frames | |
| | `frames_dropped` | int | counter | verworfene Frames | |
| `tally` | `program` | bool | status | rotes Tally | |
| | `preview` | bool | status | grünes Tally | |
| `gpio` | `state` | bool | status | Zustand des Kontakts | |
| `call` | `ok` | bool | status | letzter Aufruf eines Sende-Ziels erfolgreich | |
| | `duration_ms` | int | gauge | Dauer des letzten Aufrufs | |
| | `called_at` | string | status | Zeitpunkt des letzten Aufrufs (RFC 3339); **nicht historisiert** | |

**ST 2022-7:** Jeder Pfad ist ein Kind-Element (`kind = path`) mit `state.connected`, `stream.packets_lost` und `uses` auf seine Schnittstelle; am Sender bzw. Empfänger stehen `st2022_7.packets_unrecovered` und `st2022_7.path_skew_ms`. Der Ausfall eines Pfads ist ein eigener Befund, auch wenn das Signal weiterläuft (keine Redundanzlogik, `docs/ARCHITECTURE.md` Abschnitt 5).

## Roh

- Schlüssel `<treiber>.<name>` mit dem Treibernamen als Präfix, z. B. `videoxlink.p2p`, `http.status_code`.
- Jeder rohe Fakt steht im Fakt-Katalog des Treibers (`Driver.Facts`) mit Typ, Art und Beschreibung.
- **Rohe Fakten werden nie historisiert.** Was in eine Zeitreihe soll, kommt ins Vokabular. Ein Rohwert wird bekannt, sobald ein zweiter Treiber denselben Wert liefert; dann bekommt er hier einen Platz in einem Measurement.

## Aktionen

| Aktion | Stufe | Parameter | Prüfbedingung (Standard) | an |
|---|---|---|---|---|
| `start` | Kern | – | `state.running eq true`, 10 s | `sender`, `receiver`, `processing` |
| `stop` | Kern | – | `state.running eq false`, 10 s | wie `start` |
| `reboot` | Kern | – | keine (`accepted` ist Endzustand; die Quelle verbindet danach neu) | `access`, `module`; immer `critical` |
| `set` | Kern | `{"schlüssel": wert}` | erzeugt der Core aus den Werten (außer `write_only`) | alles mit `settings` |
| `trigger` | Kern | laut `element.params` | keine | `trigger` |
| `reset_counters` | bekannt | – | keine | Elemente mit Zählern |

Rohe Aktionen tragen den Treibernamen als Präfix (`videoxlink.reset_video_buffer`). Treiber dürfen Timeout und Prüfbedingung der Kern-Aktionen in ihrer `ActionDef` anpassen, aber nicht ihre Bedeutung.

## Tags in InfluxDB

| Tag | Inhalt |
|---|---|
| `element_id` | wovon der Wert ist |
| `device_id` | „alles von diesem Gerät“ ohne Join |
| `source_id` | Unterscheidung, wenn zwei Quellen dasselbe Gerät sehen |
| `driver` | herstellerspezifische Auswertung |
| `kind` | z. B. alle `receiver` |
| `transport` | z. B. `srt` innerhalb von `stream`; leer wird weggelassen |

Bewusst **keine** Tags: Label, Gruppe und Name (ändern sich und würden Zeitreihen zerreißen, aufgelöst über das Schema `reporting`); Hersteller, Modell und Firmware (stehen in der Identität der Quelle und ändern sich mit Updates); Richtung (steckt im Feldnamen); Messpunkt und Pfad (sind Elemente).

## Namensräume von `global_ref`

Format `<namensraum>:<id>`. Jeder Namensraum gehört genau einem Treiber, der ihn als eigene Identität setzt; andere verwenden ihn nur als Ziel von Links.

| Namensraum | gehört | Beispiele |
|---|---|---|
| `xlink` | `videoxlink` | `xlink:X8A9001` (Gerät, am Zugang), `xlink:X8A9001-E1` (Unit) |
| `nmos` | späterer NMOS-Treiber | `nmos:<uuid>` |

## Reservierte Namen

| Art | Namen |
|---|---|
| `kind` | `device`, `group`, `access` |
| Fakt | `present` (blendet der Core aus `element.present` ein; nicht historisiert) |
| Measurement | `health`, `rollup` (schreibt Monika selbst) |
| Treibername | kein Measurement-Name dieses Vokabulars (sonst kollidieren rohe Schlüssel `<treiber>.<name>` mit `<measurement>.<feld>`) |
