# Monika – Build- und Testsystem. `make help` listet alle Targets.

SHELL         := /bin/bash
.SHELLFLAGS   := -eu -o pipefail -c
.DEFAULT_GOAL := help

GO      ?= go
DOCKER  ?= docker
COMPOSE := $(DOCKER) compose -f deploy/compose.yaml

# Go wird lokal vorausgesetzt. Übrige Werkzeuge mit fester Version per go run bzw. als fertiges Docker-Image.
GOOSE          := $(GO) run github.com/pressly/goose/v3/cmd/goose@v3.28.0
DBML2SQL       := $(DOCKER) run --rm -e NPM_CONFIG_UPDATE_NOTIFIER=false \
                  -v $(CURDIR)/docs/schema:/schema:ro node:24-alpine \
                  npx --yes --loglevel=error -p @dbml/cli@10.3.0 dbml2sql --postgres
SQLC           := $(DOCKER) run --rm --user $$(id -u):$$(id -g) -v $(CURDIR):/src -w /src sqlc/sqlc:1.31.1
POSTGRES_IMAGE := postgres:18-alpine
MERMAID_IMAGE  := minlag/mermaid-cli:12.0.0

DATABASE_URL ?= postgres://monika:monika@127.0.0.1:5432/monika?sslmode=disable

.PHONY: help
help: ## Zeigt diese Hilfe
	@awk 'BEGIN {FS = ":.*## "} /^[a-z][a-z-]*:.*## / {printf "  %-14s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

# --- Go ----------------------------------------------------------------------

.PHONY: build
build: ## Baut alle Pakete
	$(GO) build ./...

.PHONY: test
test: ## Unit-Tests (ohne Docker)
	$(GO) test ./...

.PHONY: test-integration
test-integration: ## Integrationstests; startet PostgreSQL per testcontainers in Docker
	$(GO) test -tags integration ./...

.PHONY: vet
vet: ## go vet, inklusive Integrationstests
	$(GO) vet ./...
	$(GO) vet -tags integration ./...

# --- Dokumentation -----------------------------------------------------------

.PHONY: docs-check
docs-check: ## Rendert alle Mermaid-Diagramme in docs/ und bricht bei Syntaxfehlern ab
	@files=$$(grep -rl --include='*.md' '^```mermaid' docs); \
	$(DOCKER) run --rm --user $$(id -u):$$(id -g) -v $(CURDIR)/docs:/data/docs:ro \
	  --entrypoint sh $(MERMAID_IMAGE) -c ' \
	    status=0; \
	    for f in "$$@"; do \
	      if mmdc -p /puppeteer-config.json -q -i "/data/$$f" -o "/tmp/out.md" >/tmp/log 2>&1; then echo "ok    $$f"; \
	      else echo "FEHLER $$f"; grep -m1 -A3 "Error" /tmp/log; status=1; fi; \
	    done; exit $$status' sh $$files

# --- Datenbank ---------------------------------------------------------------

.PHONY: db-generate
db-generate: ## Erzeugt migrations/00001_init.sql aus docs/schema/monika.dbml
	{ echo '-- +goose Up'; \
	  echo '-- Generiert aus docs/schema/monika.dbml mit `make db-generate`. Nicht von Hand ändern.'; \
	  echo '-- +goose StatementBegin'; \
	  $(DBML2SQL) /schema/monika.dbml; \
	  echo '-- +goose StatementEnd'; } > migrations/00001_init.sql.tmp
	mv migrations/00001_init.sql.tmp migrations/00001_init.sql

.PHONY: db-gen-go
db-gen-go: ## Erzeugt Go-Code aus Migrationen und Queries (sqlc)
	rm -rf internal/store/postgres/db # sqlc entfernt keine Dateien entfallener Queries
	$(SQLC) generate

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
