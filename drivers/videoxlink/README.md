# drivers/videoxlink

Treiber für VideoXLink X4/X8. Adapter für `github.com/lukirs95/goxlinkclient` (Firmware 1.8.4).

- Units (Encoder/Decoder, SRT, NDI) als `sender`/`receiver` direkt unter dem Gerät, `uses` auf SDI-Port bzw. Interface
- Peers als Elemente mit Stellvertretern der verbundenen Remote-Units (aus der lokalen Link-Konfiguration)
- `feeds`-Links meldet die Decoder-Seite
- `DecoderSender` wird **nicht** angeboten
- `unconfirmed` ist zu erwarten bei Start vor Verbindungsaufbau oder ohne Decoder-Lizenz

Siehe `docs/ARCHITECTURE.md`, Abschnitt 7 und 16.
