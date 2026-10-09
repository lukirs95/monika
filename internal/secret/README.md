# internal/secret

Verschlüsselter Secret-Store in PostgreSQL.

- AES-256-GCM (Go-Standardbibliothek), Nonce pro Verschlüsselung, Associated Data = Secret-ID
- Master-Key aus `MONIKA_MASTER_KEY` (`secret.ParseKey`), Rotation über `key_id` (Fingerabdruck des Schlüssels) und `MONIKA_MASTER_KEY_PREVIOUS`
- `secret.Service`: anlegen, ersetzen, Klartext für Treiber, löschen; `VerifyAndRotate` beim Start
- weitere verschlüsselte Bestände (TOTP-Geheimnisse) als `secret.Vault`: `Seal`/`Open` verschlüsseln sie, `VerifyAndRotate` prüft und rotiert sie mit
- Startprüfung: ein nicht lesbares Secret → kein Start, nichts wird rotiert
- `secret.Key` gibt beim Formatieren und Loggen nie den Schlüssel aus
- Werte werden nie angezeigt, geloggt oder ins Audit geschrieben

Siehe `docs/ARCHITECTURE.md`, Abschnitt 14 (Secret-Store).
