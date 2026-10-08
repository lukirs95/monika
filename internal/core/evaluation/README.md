# internal/core/evaluation

Bewertung des Zustands.

- Regeln: Sollzustand `fact op value`, `when`, Haltezeit, Hysterese; Präzedenz nach Geltungsbereich (nähere Gruppe vor fernerer), dann Anzahl der Filter
- Befunde aus Regeln, Gerätealarmen und System; Ende als behoben (`cleared`) oder zurückgezogen (`withdrawn`, mit `cleared_reason`)
- effektives `monitored` (vererbt entlang `parent_id`)
- Ursache vs. Symptom entlang `feeds`, `uses`, `parent`
- Health (ohne Health, `unknown`, schlimmster Befund) und Rollup für Geräte und Gruppen

Siehe `docs/ARCHITECTURE.md`, Abschnitt 8.
