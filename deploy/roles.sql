-- Rollen für den Betrieb (docs/OPERATIONS.md, Datenbank und Rechte). Einmal als Superuser bzw.
-- Besitzer der Datenbank ausführen, nachdem Monika ihre Migrationen eingespielt hat:
--
--   psql "$DATABASE_URL" -v reporting_password=… -v backup_password=… -f deploy/roles.sql
--
-- Die Migrationen legen keine Rollen an; Rollen und Passwörter gehören dem Betreiber.

-- Liest nur das Schema reporting (Grafana, Reporting, Skripte). Sie sieht alles, ohne Monikas
-- Rechte; Zugang bekommt nur, wer in Monika auch global view hätte.
CREATE ROLE monika_reporting LOGIN PASSWORD :'reporting_password';
GRANT USAGE ON SCHEMA reporting TO monika_reporting;
GRANT SELECT ON ALL TABLES IN SCHEMA reporting TO monika_reporting;
-- Views aus künftigen Migrationen (angelegt von der Rolle monika) ebenso.
ALTER DEFAULT PRIVILEGES FOR ROLE monika IN SCHEMA reporting GRANT SELECT ON TABLES TO monika_reporting;

-- Liest alles für pg_dump und schreibt nichts.
CREATE ROLE monika_backup LOGIN PASSWORD :'backup_password';
GRANT pg_read_all_data TO monika_backup;
