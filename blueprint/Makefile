# Makefile — <PROJECT_NAME>
# Cibles humaines (migrations, sqlc) vs cibles CI (check, lint).
# Adapter DATABASE_URL / chemins migrations au projet.

MIGRATE       ?= migrate
SQLC          ?= sqlc
MIGRATIONS    ?= sql/migrations
DB_URL_DEV    ?= $(DATABASE_URL)
DB_URL_PROD   ?= $(DATABASE_URL_PROD)

.PHONY: help run build check lint test vet sqlc up-dev up-prod down-dev new-migration

help: ## Liste les cibles
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

## --- Dev ---

run: ## Lance l'API
	go run ./cmd/api

build: ## Compile
	go build ./...

## --- Qualité (à passer avant de déclarer une tâche terminée) ---

check: vet test ## vet + tests

vet: ## go vet
	go vet ./...

test: ## Tests
	go test ./...

lint: ## golangci-lint + garde-fous structurels
	golangci-lint run ./...
	./scripts/check-cross-feature-imports.sh
	./scripts/check-file-size.sh
	./scripts/check-sommaire.sh

## --- Base de données (HUMAIN uniquement) ---

sqlc: ## Génère internal/database/ depuis sql/queries/ — HUMAIN
	$(SQLC) generate

up-dev: ## Applique les migrations en dev — HUMAIN
	$(MIGRATE) -path $(MIGRATIONS) -database "$(DB_URL_DEV)" up

up-prod: ## Applique les migrations en prod — HUMAIN
	$(MIGRATE) -path $(MIGRATIONS) -database "$(DB_URL_PROD)" up

down-dev: ## Rollback 1 migration en dev — HUMAIN
	$(MIGRATE) -path $(MIGRATIONS) -database "$(DB_URL_DEV)" down 1

new-migration: ## make new-migration name=create_users — HUMAIN
	$(MIGRATE) create -ext sql -dir $(MIGRATIONS) -seq $(name)
