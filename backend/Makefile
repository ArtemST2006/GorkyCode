# Команды разработки backend по образцу AvitoLab.
GO ?= go
ARGS ?=
BINARY ?= bin/backend
MIGRATIONS_DIR ?= migrations
GOLANGCI_LINT ?= golangci-lint
OAPI_CODEGEN ?= oapi-codegen
GOOSE_BIN ?= goose

# Настройки для модели
MODEL_URL ?= https://github.com/k2-fsa/sherpa-onnx/releases/download/asr-models/sherpa-onnx-zipformer-ru-2024-09-18.tar.bz2
MODEL_DIR ?= model/asr-model
MODEL_ARCHIVE ?= model/sherpa-onnx-zipformer-ru-2024-09-18.tar.bz2

# Настройки локального backend и миграций загружаются из .env.
-include .env
export

GOOSE = $(GOOSE_BIN) -dir $(MIGRATIONS_DIR)

.DEFAULT_GOAL := help
.PHONY: help install build run format lint tidy-check test test-asr migrate migrate-down migrate-status migrate-create generate download-model clean-model

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

test-asr: ## Протестировать распознавание речи на тестовых аудио (test_wavs)
	$(GO) test -v -run TestTranscribeTestWavs ./internal/voice

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
	$(OAPI_CODEGEN) -config oapi-codegen.yaml ../contracts/backend.openapi.yaml

download-model: ## Скачать и распаковать модель распознавания речи
	@echo "Скачивание модели в $(MODEL_DIR)..."
	@mkdir -p model
	@if [ ! -f "$(MODEL_ARCHIVE)" ]; then \
		curl -L -o "$(MODEL_ARCHIVE)" "$(MODEL_URL)"; \
		echo "Модель скачана: $(MODEL_ARCHIVE)"; \
	else \
		echo "Архив уже существует: $(MODEL_ARCHIVE)"; \
	fi
	@if [ ! -f "$(MODEL_DIR)/tokens.txt" ]; then \
		echo "Распаковка модели..."; \
		mkdir -p "$(MODEL_DIR)"; \
		tar -xjf "$(MODEL_ARCHIVE)" -C "$(MODEL_DIR)" --strip-components=1; \
		echo "Модель распакована в $(MODEL_DIR)"; \
	else \
		echo "Модель уже распакована в $(MODEL_DIR)"; \
	fi

clean-model: ## Удалить скачанную модель
	@echo "Удаление модели..."
	@rm -rf "$(MODEL_ARCHIVE)" "$(MODEL_DIR)"
	@echo "Модель удалена"
