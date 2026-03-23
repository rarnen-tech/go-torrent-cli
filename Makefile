APP_NAME := go-torrent-cli
COMPOSE := docker compose -f docker-compose.yml

ifeq ($(OS),Windows_NT)
EXE := .exe
SHELL := cmd.exe
.SHELLFLAGS := /C
MKDIR_BIN := if not exist bin mkdir bin
RM_BIN := if exist bin rmdir /s /q bin
RM_STATE := if exist .torrent-state rmdir /s /q .torrent-state
RM_DOWNLOADS := if exist downloads rmdir /s /q downloads
RM_DOWNLOADS_JSON := if exist downloads.json del /f /q downloads.json
RM_CONFIG := if exist config.json del /f /q config.json
else
EXE :=
MKDIR_BIN := mkdir -p bin
RM_BIN := rm -rf bin
RM_STATE := rm -rf .torrent-state
RM_DOWNLOADS := rm -rf downloads
RM_DOWNLOADS_JSON := rm -f downloads.json
RM_CONFIG := rm -f config.json
endif

.PHONY: up down restart logs ps build run test bench clean

up:
	$(COMPOSE) up -d

down:
	$(COMPOSE) down

restart:
	$(COMPOSE) down
	$(COMPOSE) up -d

logs:
	$(COMPOSE) logs -f

ps:
	$(COMPOSE) ps

build:
	$(MKDIR_BIN)
	go build -o bin/$(APP_NAME)$(EXE) ./cmd/app

run:
	go run ./cmd/app

test:
	go test ./...

bench:
	go test -run ^$$ -bench Benchmark -benchmem ./internal/torrent

clean:
	$(RM_BIN)
	$(RM_STATE)
	$(RM_DOWNLOADS)
	$(RM_DOWNLOADS_JSON)
	$(RM_CONFIG)
