# Команды разработки backend по образцу AvitoLab.
GO ?= go
ARGS ?=
BINARY ?= bin/backend
MIGRATIONS_DIR ?= migrations
GOLANGCI_LINT ?= golangci-lint
OAPI_CODEGEN ?= oapi-codegen
GOOSE_BIN ?= goose

# Настройки локального backend и миграций загружаются из .env.
-include .env
export

GOOSE = $(GOOSE_BIN) -dir $(MIGRATIONS_DIR)

.DEFAULT_GOAL := help
.PHONY: help install build run format lint tidy-check test migrate migrate-down migrate-status migrate-create generate

help: ## Показать список команд
	@grep -hE '^[a-zA-Z_-]+:.*## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*## "}; {printf "  %-16s %s\n", $$1, $$2}'

install: ## Скачать зависимости приложения
	$(GO) mod download

build: ## Собрать исполняемый файл
	$(GO) build -buildvcs=false -trimpath -o $(BINARY) ./cmd/backend

run: ## Запустить backend с настройками из .env и параметрами ARGS
	$(GO) run ./cmd/backend $(ARGS)

format: ## Применить gofumpt и goimports
	$(GOLANGCI_LINT) fmt --config .golangci.yml

lint: ## Проверить модули, форматирование и код
	$(GO) mod verify
	$(GOLANGCI_LINT) config verify --config .golangci.yml
	$(GOLANGCI_LINT) fmt --config .golangci.yml --diff
	$(GOLANGCI_LINT) run --config .golangci.yml

tidy-check: ## Проверить go.mod и go.sum (нужен доступ к зависимостям)
	$(GO) mod tidy -diff

test: ## Запустить тесты с race detector и создать отчёты покрытия
	$(GO) test -race -coverprofile=coverage.out ./...
	$(GO) tool cover -func=coverage.out
	$(GO) tool cover -html=coverage.out -o coverage.html

migrate: ## Применить миграции PostgreSQL к DATABASE_URL
	$(GOOSE) postgres "$(DATABASE_URL)" up

migrate-down: ## Откатить последнюю миграцию
	$(GOOSE) postgres "$(DATABASE_URL)" down

migrate-status: ## Показать состояние миграций
	$(GOOSE) postgres "$(DATABASE_URL)" status

migrate-create: ## Создать SQL-миграцию: make migrate-create NAME=create_tasks
	@test -n "$(NAME)" || { echo "Укажите имя: make migrate-create NAME=create_tasks"; exit 1; }
	$(GOOSE) create $(NAME) sql

generate: ## Сгенерировать Go-типы и маршруты из OpenAPI
	$(OAPI_CODEGEN) \
		-generate types,chi-server,skip-prune \
		-package api \
		-o internal/generated/api.gen.go \
		contracts/openapi/backend.openapi.yaml
