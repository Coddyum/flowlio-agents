# Makefile — flowlio-ia
# Cibles dev (déléguées à Claude) vs cibles prod (humain exclusivement).

MIGRATE       ?= migrate
SQLC          ?= sqlc
MIGRATIONS    ?= sql/migrations
SCHEMA_DUMP   ?= sql/schema/schema.sql
PG_CONTAINER  ?= flowlio-postgres

# Charge .env s'il existe : DATABASE_URL est alors disponible pour les cibles ci-dessous.
ifneq (,$(wildcard .env))
include .env
export
endif

DB_URL_DEV    ?= $(DATABASE_URL)
DB_URL_PROD   ?= $(DATABASE_URL_PROD)

.PHONY: help dev-up dev-down run build check lint test test-integration vet sqlc schema sommaire up-dev down-dev up-prod new-migration

help: ## Liste les cibles
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

## --- Dev ---

dev-up: ## Démarre Postgres 17 (docker compose)
	docker compose up -d

dev-down: ## Arrête Postgres 17
	docker compose down

run: ## Lance l'API
	go run ./cmd/api

build: ## Compile
	go build ./...

## --- Qualité (à passer avant de déclarer une tâche terminée) ---

check: vet test ## vet + tests

vet: ## go vet
	go vet ./...

test: ## Tests unitaires (aucune infrastructure requise)
	go test ./...

test-integration: ## Tests d'intégration sur la base de dev
	FLOWLIO_TEST_DATABASE_URL="$(DB_URL_DEV)" go test ./... -count=1

lint: ## golangci-lint + garde-fous structurels
	golangci-lint run ./...
	./scripts/check-cross-feature-imports.sh
	./scripts/check-file-size.sh
	./scripts/check-sommaire.sh

sommaire: ## Resynchronise les numéros de ligne des blocs // SOMMAIRE
	./scripts/sync-sommaire-lines.sh

## --- Base de données ---
## sqlc / up-dev / down-dev / new-migration : délégués à Claude.
## up-prod : HUMAIN EXCLUSIVEMENT.

sqlc: ## Génère internal/database/ depuis sql/migrations + sql/queries
	$(SQLC) generate

schema: ## Regénère l'instantané lisible du schéma depuis la base de dev
	docker exec $(PG_CONTAINER) pg_dump -U flowlio -d flowlio --schema-only --no-owner --no-privileges > $(SCHEMA_DUMP)

up-dev: ## Applique les migrations en dev
	$(MIGRATE) -path $(MIGRATIONS) -database "$(DB_URL_DEV)" up

down-dev: ## Rollback 1 migration en dev
	$(MIGRATE) -path $(MIGRATIONS) -database "$(DB_URL_DEV)" down 1

new-migration: ## make new-migration name=create_users
	$(MIGRATE) create -ext sql -dir $(MIGRATIONS) -seq $(name)

up-prod: ## Applique les migrations en prod — HUMAIN EXCLUSIVEMENT
	$(MIGRATE) -path $(MIGRATIONS) -database "$(DB_URL_PROD)" up
