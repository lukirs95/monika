# Monika – Build- und Testsystem. `make help` listet alle Targets.

SHELL         := /bin/bash
.SHELLFLAGS   := -eu -o pipefail -c
.DEFAULT_GOAL := help

GO      ?= go
DOCKER  ?= docker
COMPOSE := $(DOCKER) compose -f deploy/compose.yaml

# Go wird lokal vorausgesetzt. Übrige Werkzeuge mit fester Version per go run bzw. als fertiges Docker-Image.
GOOSE          := $(GO) run github.com/pressly/goose/v3/cmd/goose@v3.28.0
GOLANGCI_LINT  := $(GO) run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0
DBML2SQL       := $(DOCKER) run --rm -e NPM_CONFIG_UPDATE_NOTIFIER=false \
                  -v $(CURDIR)/schema:/schema:ro node:24-alpine \
                  npx --yes --loglevel=error -p @dbml/cli@10.3.0 dbml2sql --postgres
SQLC           := $(DOCKER) run --rm --user $$(id -u):$$(id -g) -v $(CURDIR):/src -w /src sqlc/sqlc:1.31.1
POSTGRES_IMAGE := postgres:18-alpine
MERMAID_IMAGE  := minlag/mermaid-cli:12.0.0
REDOCLY_IMAGE  := redocly/cli:2.62.0

DATABASE_URL ?= postgres://monika:monika@127.0.0.1:5432/monika?sslmode=disable

.PHONY: help
help: ## Zeigt diese Hilfe
	@awk 'BEGIN {FS = ":.*## "} /^[a-z][a-z-]*:.*## / {printf "  %-14s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

# --- Go ----------------------------------------------------------------------

.PHONY: build
build: ## Baut alle Pakete und das Binary bin/monika für dieses System
	$(GO) build ./...
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o bin/monika ./cmd/monika

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
ARCH    ?= $(shell $(GO) env GOARCH)
IMAGE   ?= monika:$(VERSION)

.PHONY: image
image: ## Baut das Binary für linux/ARCH lokal und das Image IMAGE daraus (deploy/Dockerfile)
	CGO_ENABLED=0 GOOS=linux GOARCH=$(ARCH) $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o bin/linux-$(ARCH)/monika ./cmd/monika
	$(DOCKER) build --platform linux/$(ARCH) --build-arg BINARY=bin/linux-$(ARCH)/monika -f deploy/Dockerfile -t $(IMAGE) .

.PHONY: test
test: ## Unit-Tests (ohne Docker)
	$(GO) test ./...

.PHONY: test-integration
test-integration: ## Integrationstests; startet PostgreSQL per testcontainers in Docker
	$(GO) test -tags integration ./...

BENCH     ?= .
BENCHTIME ?= 1s

.PHONY: bench
bench: ## Benchmarks in Stufen bis 1000 Geräte; Auswahl mit BENCH=Muster, Dauer mit BENCHTIME
	$(GO) test -run '^$$' -bench '$(BENCH)' -benchtime $(BENCHTIME) -benchmem -timeout 60m ./internal/...

.PHONY: vet
vet: ## go vet, inklusive Integrationstests
	$(GO) vet ./...
	$(GO) vet -tags integration ./...

.PHONY: lint
lint: ## golangci-lint (Konfiguration in .golangci.yml), inklusive Integrationstests
	$(GOLANGCI_LINT) run --build-tags integration ./...

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

.PHONY: api-gen
api-gen: ## Erzeugt Typen, Router und Server-Interface aus api/openapi.yaml (oapi-codegen)
	$(GO) tool oapi-codegen -config api/oapi-codegen.yaml api/openapi.yaml

.PHONY: api-check
api-check: ## Prüft die OpenAPI-Spezifikation api/openapi.yaml
	$(DOCKER) run --rm -v $(CURDIR):/spec:ro -w /spec $(REDOCLY_IMAGE) lint --format=stylish api/openapi.yaml

# --- Datenbank ---------------------------------------------------------------

.PHONY: db-generate
db-generate: ## Erzeugt migrations/00001_init.sql aus schema/monika.dbml
	{ echo '-- +goose Up'; \
	  echo '-- Generiert aus schema/monika.dbml mit `make db-generate`. Nicht von Hand ändern.'; \
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
