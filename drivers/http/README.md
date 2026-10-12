# drivers/http

Treiber für **HTTP-Endpunkte als Sende-Ziel** (Webhooks, Tally, Companion, eigene Skripte). Vertrag dieses Treibers nach `docs/DRIVERS.md`; Regeln für Sende-Ziele: `docs/ARCHITECTURE.md`, Abschnitt 11.

**Status:** Entwurf vom 2026-10-12, zur Abstimmung.

Monika sendet nur. Sie liest keinen Zustand vom Ziel; ihre Fakten sind Beobachtungen über die eigenen Aufrufe.

## Zugang

| | |
|---|---|
| Protokoll | HTTP/1.1 bzw. HTTP/2 über `net/http`, `http` oder `https` |
| Adresse | `SourceConfig.Address` ist die **Basis-URL** des Ziels, z. B. `http://companion.local:8000` oder `https://hooks.example/monika`. Pfade der Aufrufe werden angehängt |
| Abfrage | keine; optional ein Health-Check (siehe unten) |
| `Heartbeat()` | `health.interval_s`, ohne Health-Check `0` (keine Überwachung) |
| Weiterleitungen | werden **nicht** verfolgt; `3xx` gilt als abgelehnt. Das Ziel steht fest in der Konfiguration, und Zugangsdaten in Headern sollen nie woanders landen |
| TLS | Zertifikat wird geprüft; `insecure_skip_verify` schaltet das pro Quelle ab (Geräte mit selbst signierten Zertifikaten) |

**Ohne Health-Check** meldet `Run` sofort `Connected` und den `Snapshot` und wartet dann nur noch; die Quelle ist sofort Live und wird nie wegen Stille unerreichbar.

**Mit Health-Check** ruft `Run` alle `interval_s` Sekunden `health.method` auf `health.path` auf (über den Scheduler). Jede `2xx`-Antwort ist ein Lebenszeichen; die erste nach dem Start bzw. nach einem Ausfall meldet `Connected` und einen vollständigen `Snapshot`. Schlägt er fehl (Fehler oder nicht `2xx`), meldet `Run` einmal `Disconnected(err)` und prüft im selben Takt weiter. Aufrufe (`trigger`) gehen in jedem Fall raus, auch wenn der Health-Check gerade fehlschlägt.

## Elemente

```
Gerät (legt der User an)
└─ http                    access              (keine Fakten)
   ├─ Tally Kamera         trigger · out       call.*, http.status_code · trigger
   └─ Aufnahme starten     trigger · out       call.*, http.status_code · trigger (kritisch)
```

- **Jeder konfigurierte Aufruf ist ein Element** mit `kind = trigger`, `direction = out`, ohne Transport, direkt unter dem Zugang.
- `external_id` = `id` des Aufrufs, vom User vergeben, Muster `^[a-z0-9][a-z0-9._-]{0,63}$`. Sie ist stabil, solange der User sie nicht ändert; eine geänderte `id` ist ein neues Element (Labels, Regeln und Gruppen hängen am alten).
- `name` = `name` des Aufrufs. `attributes` = `{"method": …, "path": …}`, nur zur Anzeige.
- Keine `global_ref`, keine Links, keine Alarme, keine Einstellungen.
- Entfernt der User einen Aufruf aus der Konfiguration, fehlt er im nächsten `Snapshot` und gilt als verschwunden (`present = false`).

## Fakten

Pro Aufruf-Element, gemeldet nach jedem Aufruf (aus `Execute`, über die Goroutine von `Run` an den `Reporter`):

| Fakt | Typ | Bedeutung |
|---|---|---|
| `call.ok` | `bool` | Antwort mit `2xx` |
| `call.called_at` | `string` | Beginn des Aufrufs, RFC 3339 in UTC |
| `call.duration_ms` | `int` | Dauer bis zur Antwort bzw. zum Fehler |
| `http.status_code` | `int`, roh, `status` | HTTP-Status der Antwort; `0`, wenn keine kam (Timeout, keine Verbindung) |

- **Vor dem ersten Aufruf** gibt es keine Fakten; der Treiber erfindet keine. Nach einem Neustart der Quelle fehlen sie wieder, bis zum nächsten Aufruf.
- Grün wird ein Aufruf erst mit einer Regel wie `call.ok eq true`.
- Der Zugang hat keine Fakten; seine Erreichbarkeit zeigt der Health-Check über die Lebenszeichen (`source.unreachable`).

## Aktion `trigger`

Die einzige Aktion jedes Aufruf-Elements; kritisch, wenn der Aufruf `critical: true` hat (`critical_actions`).

| | |
|---|---|
| Parameter | `trigger {"camera": 2, "state": "program"}`: nur für diesen Aufruf; geprüft gegen das Schema in `element.params.trigger`; fehlende Werte aus `default` |
| Prüfung | unbekannter Parameter, falscher Typ, außerhalb von `min`/`max`, nicht in `values`, fehlend ohne `default` → `rejected` mit den Feldern in `RejectedFields`; es geht dann nichts raus |
| Anfrage | `method` an Basis-URL + `path` mit den Parametern aus `query`; Body aus `body`, sonst ohne Body; Header aus `headers` des Aufrufs, dann aus dem Secret, dann die Anmeldung (siehe unten) |
| Ergebnis | `2xx` → `accepted`, Antwort `{"status": 204}` · `3xx`, `4xx`, `5xx` → `rejected`, Antwort `{"status": 503, "body": "…"}` (Body höchstens 4 KiB, als Text) · Timeout, keine Verbindung, TLS-Fehler → Fehler (`failed`) |
| Prüfbedingung | keine; `accepted` ist der Endzustand (Sende-Ziel) |
| Timeout | `timeout_s` der Quelle (Standard 10 s, höchstens 30 s); der Katalog nennt 30 s für `trigger` |

**Platzhalter** `{{name}}` in `path`, `query` und `body`, ohne Anführungszeichen:

| Stelle | eingesetzt als |
|---|---|
| `body` | JSON-Wert (`"program"`, `2`, `true`) |
| `path` | URL-kodiert für einen Pfadabschnitt; `path` enthält kein `?` |
| Werte in `query` | URL-kodiert als Query-Parameter, z. B. `"query": {"state": "{{state}}", "source": "monika"}` |

Ein Platzhalter, den `params` nicht kennt, ist ein Fehler der Konfiguration: `New` lehnt sie ab, die Quelle bleibt im Backoff mit dieser Meldung. Ebenso doppelte `id`s, ein `?` in `path` und Vorlagen, die nach dem Einsetzen der Standardwerte kein gültiges JSON (Body) bzw. keine gültige URL ergeben.

**Parameter-Typen** wie bei Einstellungen (`sdk.Setting`): `int`, `float`, `bool`, `string`, `enum` (mit `values`); `min`/`max` für Zahlen; `default` optional.

## Konfiguration

`device_source.config`, geprüft gegen dieses Schema (`ConfigSchema`):

```json
{
  "timeout_s": 10,
  "insecure_skip_verify": false,
  "health": { "method": "GET", "path": "/health", "interval_s": 10 },
  "calls": [
    {
      "id": "tally",
      "name": "Tally Kamera",
      "method": "POST",
      "path": "/api/tally/{{camera}}",
      "query": { "source": "monika" },
      "headers": { "Content-Type": "application/json" },
      "body": "{\"camera\": {{camera}}, \"state\": {{state}}}",
      "critical": false,
      "params": {
        "camera": { "type": "int", "min": 1, "max": 16, "default": 3 },
        "state":  { "type": "enum", "values": ["off", "preview", "program"], "default": "off" }
      }
    }
  ]
}
```

| Feld | Pflicht | Standard | |
|---|---|---|---|
| `calls` | ja | | mindestens einer |
| `calls[].id`, `name`, `method`, `path` | ja | | `method`: `GET`, `POST`, `PUT`, `PATCH`, `DELETE`; `path` beginnt mit `/`, ohne `?` |
| `calls[].query` | | | Query-Parameter, Name → Vorlage |
| `calls[].headers` | | | feste Header; ohne `Content-Type` und mit Body gilt `application/json` |
| `calls[].body`, `params` | | | |
| `calls[].critical` | | `false` | |
| `timeout_s` | | `10` | 1 bis 30 |
| `insecure_skip_verify` | | `false` | |
| `health` | | | ohne: keine Überwachung; `method` Standard `GET`, `interval_s` 1 bis 3600 |
| `auth` | | | Anmeldung vor dem Aufruf (siehe unten) |

Keine Zugangsdaten in der Konfiguration; sie stehen im Secret und erscheinen in Vorlagen nur als `{{secret.<feld>}}`.

## Anmeldung (`auth`)

Für Ziele, die weder festen Bearer noch Basic Auth können, sondern eine Anmeldung verlangen, deren Ergebnis (Cookie, Token) danach mitgeschickt wird:

```json
"auth": {
  "login": "on_demand",
  "method": "POST",
  "path": "/api/login",
  "headers": { "Content-Type": "application/json" },
  "body": "{\"user\": {{secret.username}}, \"password\": {{secret.password}}}",
  "token": { "from": "json", "pointer": "/data/token" },
  "apply": { "as": "header", "name": "Authorization", "format": "Bearer {{token}}" },
  "retry_on": [401]
}
```

| Feld | | |
|---|---|---|
| `login` | `on_demand` (Standard) | der erste Aufruf geht ohne Token; erst eine Antwort aus `retry_on` löst die Anmeldung aus |
| | `on_start` | zusätzlich einmal beim Start von `Run`, damit schon der erste Aufruf einen Token trägt (für Ziele, die einen Aufruf ohne Anmeldung trotzdem ausführen könnten) |
| `method`, `path`, `headers`, `body` | | die Anfrage der Anmeldung; `method` Standard `POST`. Platzhalter `{{secret.<feld>}}` aus dem JSON des Secrets: im Body als JSON-Wert, in Headern als Text. Andere Platzhalter gibt es hier nicht |
| `token.from` | `cookie` (`name`) | Wert des Cookies aus `Set-Cookie` |
| | `header` (`name`) | Wert eines Antwort-Headers |
| | `json` (`pointer`) | Wert im JSON der Antwort, als JSON Pointer (RFC 6901); muss ein String sein |
| `apply.as` | `cookie` (`name`) | als Cookie bei jeder Anfrage |
| | `header` (`name`, `format`) | als Header; `format` mit `{{token}}`, Standard `{{token}}` |
| `retry_on` | Standard `[401]` | Status, die eine neue Anmeldung auslösen, z. B. `[401, 403]` |

**Ablauf eines Aufrufs mit `auth`:**

1. Liegt ein Token im Speicher, geht er mit.
2. Antwortet das Ziel mit einem Status aus `retry_on`, meldet sich der Treiber an, legt den neuen Token im Speicher ab und schickt dieselbe Anfrage **genau einmal** erneut.
3. Der Token bleibt, bis wieder ein Status aus `retry_on` kommt. Er liegt nur im Speicher; nach einem Neustart der Quelle holt die erste Anfrage (bzw. `on_start`) einen neuen.

**Leitplanken:**

- Höchstens eine Wiederholung pro Aufruf; antwortet auch sie mit einem Status aus `retry_on`, ist der Lauf `rejected`. Es gibt keine Schleife.
- Nur eine Anmeldung zur selben Zeit: Laufen parallele Aufrufe in einen Status aus `retry_on`, meldet sich nur einer an, die anderen warten und nehmen den neuen Token. Hat ein anderer Aufruf inzwischen schon einen neueren Token geholt als den, mit dem die Anfrage scheiterte, wird ohne neue Anmeldung damit wiederholt.
- Scheitert die Anmeldung (Status nicht `2xx`, Token nicht in der Antwort), ist der Lauf `rejected` mit `{"status": …, "auth": true}`, **ohne** Body der Anmeldung. Ohne Verbindung: `failed`.
- Der Health-Check läuft durch denselben Ablauf und kann den Token ebenso auffrischen. Mit `on_start` meldet ein Fehlschlag der ersten Anmeldung `Disconnected`; `Run` versucht es im Takt des Health-Checks (ohne Health-Check alle 10 s) erneut.
- `call.*` beschreibt nur den eigentlichen Aufruf: `call.duration_ms` misst ihn samt Anmeldung und Wiederholung, `http.status_code` ist der letzte Status.
- `auth` schließt Basic Auth aus (`username`/`password` im Secret sind dann nur noch für Platzhalter da); feste `headers` aus dem Secret gelten weiter.
- Token und Antworten der Anmeldung erscheinen nie im Log, in Fehlern, Fakten oder Antworten eines Laufs.

## Secret

Optional (`secret_id` der Quelle), JSON:

```json
{ "username": "monika", "password": "…", "headers": { "Authorization": "Bearer …" } }
```

- `username` und `password` → HTTP Basic Auth bei jeder Anfrage, auch beim Health-Check; mit `auth` stattdessen nur für Platzhalter.
- `headers` → zusätzliche Header bei jeder Anfrage; sie überschreiben gleichnamige aus der Konfiguration.
- Weitere Felder dürfen vorkommen; `auth` setzt sie als `{{secret.<feld>}}` ein (Werte: String, Zahl, Bool).
- Alles darf fehlen. Werte des Secrets erscheinen nie im Log, in Fehlern, Fakten oder Antworten.

## Katalog

| | |
|---|---|
| Treibername | `http` |
| Aktionen | `trigger` (Timeout 30 s, ohne Prüfbedingung) |
| rohe Fakten | `http.status_code` (`int`, `status`) |

## Tests

- Attrappe des Ziels: `httptest.Server` mit umschaltbaren Antworten (2xx, 4xx, 5xx, Verzögerung, Abbruch) und Aufzeichnung der Anfragen.
- `sdktest.Conformance` mit und ohne Health-Check.
- Eigene Tests: Platzhalter und Kodierung in Pfad, Query und Body, Prüfung der Parameter, Ergebnisse und Fakten, Secret als Basic Auth und Header, keine Weiterleitung, Health-Check fällt aus und kommt wieder, ungültige Konfigurationen in `New`.
- Anmeldung: Token aus Cookie, Header und JSON; Anwenden als Cookie und Header; erneute Anmeldung nach `retry_on`; genau eine Wiederholung; parallele Aufrufe melden sich nur einmal an; gescheiterte Anmeldung ohne Body in der Antwort; `on_start`; kein Token und kein Secret im Log.
- **Ende zu Ende** (Integrationstest in `cmd/monika`): Monika startet mit dem Treiber, über die API werden Gerät, Secret und Quelle (mit `auth`) angelegt; die Quelle wird Live, ein `trigger` meldet sich an der Attrappe an und erreicht sie mit eingesetzten Parametern, der Lauf endet `accepted`, `call.ok` steht in den Fakten; eine Regel `call.ok eq true` wird bei einer `503` verletzt und mit dem nächsten erfolgreichen Aufruf wieder erfüllt; läuft der Token in der Attrappe ab, meldet sich der Treiber beim nächsten Aufruf selbst neu an.
