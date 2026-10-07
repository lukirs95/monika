# internal/auth

Identität und Berechtigung, getrennt:

- Anmeldung lokal (argon2id), später OIDC (`user_identity`)
- Rollen, Rechte mit Scope (Element, Gerät, Gruppe; rekursiv)
- Zusatzrecht `critical`
- Dienstkonten und API-Tokens (SHA-256-Hash)
- Synchronisation von Rollen aus dem Groups-Claim des IdP

Siehe `docs/ARCHITECTURE.md`, Abschnitt 12.
