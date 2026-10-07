# internal/secret

Verschlüsselter Secret-Store in PostgreSQL.

- AES-256-GCM (Go-Standardbibliothek), Nonce pro Verschlüsselung, Associated Data = Secret-ID
- Master-Key aus `MONIKA_MASTER_KEY`, Rotation über `key_id` und `MONIKA_MASTER_KEY_PREVIOUS`
- Startprüfung: falscher Schlüssel → kein Start
- Werte werden nie angezeigt, geloggt oder ins Audit geschrieben

Siehe `docs/ARCHITECTURE.md`, Abschnitt 14 (Secret-Store).
