# Monika – Build- und Testsystem. `make help` listet alle Targets.

SHELL         := /bin/bash
.SHELLFLAGS   := -eu -o pipefail -c
.DEFAULT_GOAL := help

GO      ?= go
DOCKER  ?= docker
COMPOSE := $(DOCKER) compose -f deploy/compose.yaml

# Werkzeuge mit fester Version; laufen per go run bzw. in Docker, nichts muss lokal installiert sein.
GOOSE          := $(GO) run github.com/pressly/goose/v3/cmd/goose@v3.28.0
DBML2SQL       := $(DOCKER) run --rm -e NPM_CONFIG_UPDATE_NOTIFIER=false \
                  -v $(CURDIR)/docs/schema:/schema:ro node:24-alpine \
                  npx --yes --loglevel=error -p @dbml/cli@10.3.0 dbml2sql --postgres
POSTGRES_IMAGE := postgres:18-alpine

DATABASE_URL ?= postgres://monika:monika@127.0.0.1:5432/monika?sslmode=disable

.PHONY: help
help: ## Zeigt diese Hilfe
	@awk 'BEGIN {FS = ":.*## "} /^[a-z][a-z-]*:.*## / {printf "  %-14s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

# --- Datenbank ---------------------------------------------------------------

.PHONY: db-generate
db-generate: ## Erzeugt migrations/00001_init.sql aus docs/schema/monika.dbml
	{ echo '-- +goose Up'; \
	  echo '-- Generiert aus docs/schema/monika.dbml mit `make db-generate`. Nicht von Hand ändern.'; \
	  echo '-- +goose StatementBegin'; \
	  $(DBML2SQL) /schema/monika.dbml; \
	  echo '-- +goose StatementEnd'; } > migrations/00001_init.sql.tmp
	mv migrations/00001_init.sql.tmp migrations/00001_init.sql

.PHONY: db-validate
db-validate: ## Spielt alle Migrationen in eine frische, temporäre PostgreSQL ein
	cid=$$($(DOCKER) run -d --rm -e POSTGRES_USER=monika -e POSTGRES_PASSWORD=monika \
	  -p 127.0.0.1::5432 --tmpfs /var/lib/postgresql $(POSTGRES_IMAGE)); \
	trap '$(DOCKER) rm -f '$$cid' >/dev/null' EXIT; \
	until $(DOCKER) exec $$cid pg_isready -q -h 127.0.0.1 -U monika; do sleep 0.5; done; \
	port=$$($(DOCKER) port $$cid 5432/tcp | head -n1 | cut -d: -f2); \
	$(GOOSE) -dir migrations postgres "postgres://monika:monika@127.0.0.1:$$port/monika?sslmode=disable" up

.PHONY: db-up
db-up: ## Startet die lokale PostgreSQL (deploy/compose.yaml)
	$(COMPOSE) up -d --wait postgres

.PHONY: db-down
db-down: ## Stoppt die lokale PostgreSQL (Daten bleiben im Volume)
	$(COMPOSE) down

.PHONY: db-migrate
db-migrate: ## Spielt alle Migrationen in die lokale PostgreSQL ein
	$(GOOSE) -dir migrations postgres "$(DATABASE_URL)" up
