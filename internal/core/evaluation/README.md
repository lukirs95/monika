# internal/core/evaluation

Bewertung des Zustands.

- Regeln: Sollzustand `fact op value`, `when` (unerfüllt = erfüllt), Haltezeiten beim Entstehen und Enden, Hysterese; Präzedenz pro Element, Fakt und Richtung nach Geltungsbereich, dann Anzahl der Filter
- Befunde aus Regeln, Gerätealarmen und System; Ende als behoben (`cleared`) oder zurückgezogen (`withdrawn`, mit `cleared_reason`)
- effektives `monitored` (vererbt entlang `parent_id`, deaktivierte Quelle = nicht überwacht)
- Ursache vs. Symptom entlang `feeds`, `uses`, `parent`, bei jeder Änderung neu abgeleitet
- Health (keine Health, `unknown` bei nicht Live, schlimmster Befund, `ok` nur wenn geprüft) und Rollup über die verschiedenen Elemente im Geltungsbereich von Geräten und Gruppen
- `evaluation.Store`: Regeln (Löschen/Deaktivieren zieht Befunde zurück), Befunde (auslösen, beenden mit Grund, Ursache setzen, quittieren), Health und Rollup (nur bei Änderung)

Siehe `docs/ARCHITECTURE.md`, Abschnitt 8.
