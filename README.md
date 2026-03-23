[Русский README](./README.ru.md)
# go-torrent-cli

[![CI](https://github.com/rarnen-tech/go-torrent-cli/actions/workflows/ci.yml/badge.svg)](https://github.com/rarnen-tech/go-torrent-cli/actions/workflows/ci.yml)
[![Release](https://github.com/rarnen-tech/go-torrent-cli/actions/workflows/release.yml/badge.svg)](https://github.com/rarnen-tech/go-torrent-cli/actions/workflows/release.yml)
[![Go 1.25+](https://img.shields.io/badge/Go-1.25%2B-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![Redis](https://img.shields.io/badge/State-Redis-DC382D?logo=redis&logoColor=white)](https://redis.io/)
[![TUI](https://img.shields.io/badge/UI-Bubble%20Tea-7AD67A)](https://github.com/charmbracelet/bubbletea)

Простой торрент-клиент на Go с зелёным TUI-интерфейсом, поддержкой `.torrent` и `magnet`, локальным control API и хранением состояния в Redis.

![Интерфейс](preview.png)

## Обзор

`go-torrent-cli` — это pet-проект консольного торрент-клиента. Его цель — дать простой и понятный инструмент для загрузки торрентов прямо из терминала без тяжёлого GUI, но при этом сохранить полезные вещи: список загрузок, ETA, скорость, возобновление задач, один живой процесс клиента и сохранение состояния между запусками.

Проект ориентирован на практическое использование и одновременно может служить примером небольшого системного приложения на Go: тут есть сетевой движок, TUI, кэш состояния, Docker Compose и GitHub Actions.

## Содержание

1. [Структура репозитория](#структура-репозитория)
2. [Возможности](#возможности)
3. [Требования](#требования)
4. [Быстрый старт](#быстрый-старт)
5. [Подробный запуск](#подробный-запуск)
6. [Команды и управление](#команды-и-управление)
7. [Docker Compose и Redis](#docker-compose-и-redis)
8. [Конфигурация](#конфигурация)
9. [Сборка](#сборка)
10. [Тесты](#тесты)
11. [Бенчмарки](#бенчмарки)
12. [CI/CD](#cicd)
13. [Ограничения и замечания](#ограничения-и-замечания)
14. [Topics для GitHub](#topics-для-github)

## Структура репозитория

```text
go-torrent-cli/
│
├── README.md                     # Основное описание проекта
├── .gitignore                    # Игнорируемые runtime и build-файлы
├── Makefile                      # Короткие команды для запуска, тестов и сборки
├── docker-compose.yml            # Redis и PostgreSQL для локальной инфраструктуры
├── go.mod                        # Go-модуль и прямые зависимости
├── go.sum                        # Зафиксированные версии зависимостей
├── preview.png                   # Скриншот интерфейса для README
│
├── .github/
│   └── workflows/
│       ├── ci.yml                # Тесты, benchmark-тесты и сборка в GitHub Actions
│       └── release.yml           # Сборка релизных бинарников по git-тегам
│
├── cmd/
│   └── app/
│       └── main.go               # Точка входа приложения
│
└── internal/
    ├── control/
    │   ├── control.go            # Локальный control API и CLI-команды
    │   └── control_test.go       # Тесты control-слоя
    │
    ├── torrent/
    │   ├── client.go             # Основная логика торрент-клиента
    │   ├── network.go            # Работа с трекерами, peer-логикой и helper-функциями
    │   ├── paths.go              # Пути, storage и перенос локальных данных
    │   ├── store.go              # Redis store и memory fallback
    │   └── torrent_test.go       # Тесты и benchmark-тесты движка
    │
    └── ui/
        ├── tui.go                # Terminal UI на Bubble Tea / Lip Gloss
        └── tui_test.go           # Тесты UI helper-функций
```

## Возможности

- добавление `.torrent` файлов;
- добавление `magnet` ссылок;
- список загрузок в терминале;
- показ статуса, скорости, ETA и прогресса;
- остановка, возобновление и удаление задач;
- один живой процесс клиента для одного workspace;
- локальный control API для повторных CLI-команд;
- хранение state в Redis;
- memory fallback, если Redis временно недоступен;
- сборка и тесты через GitHub Actions.

## Требования

Для работы проекта нужны:

- [Go 1.25+](https://go.dev/doc/install)
- [Docker Desktop](https://www.docker.com/products/docker-desktop/) — если хочешь поднимать Redis и PostgreSQL через `docker compose`
- `make` — по желанию, для коротких команд

Если `make` нет, проект всё равно можно запускать вручную обычными `go` и `docker compose` командами.

## Быстрый старт

1. Клонируй репозиторий:

```bash
git clone https://github.com/rarnen-tech/go-torrent-cli.git
cd go-torrent-cli
```

2. Подними сервисы:

```bash
make up
```

3. Запусти клиент:

```bash
make run
```

Если `make` не установлен:

```bash
docker compose -f docker-compose.yml up -d
go run ./cmd/app
```

## Подробный запуск

### Запуск TUI

```powershell
go run .\cmd\app\main.go
```

### Добавить `.torrent`

```powershell
go run .\cmd\app\main.go C:\path\movie.torrent
```

### Добавить `magnet`

```powershell
go run .\cmd\app\main.go "magnet:?xt=..."
```

### Запустить уже собранный бинарник

```powershell
.\bin\go-torrent-cli.exe
```

## Команды и управление

### Make-команды

```bash
make up
make down
make restart
make logs
make ps
make run
make build
make test
make bench
make clean
```

### CLI-команды

Получить список задач:

```powershell
go run .\cmd\app\main.go list
```

Посмотреть статус задачи:

```powershell
go run .\cmd\app\main.go status 1
```

Остановить задачу:

```powershell
go run .\cmd\app\main.go stop 1
```

Возобновить задачу:

```powershell
go run .\cmd\app\main.go resume 1
```

Удалить задачу:

```powershell
go run .\cmd\app\main.go delete 1
```

Сменить папку загрузки:

```powershell
go run .\cmd\app\main.go path C:\Downloads
```

### Управление в TUI

- `I` — добавить торрент или magnet
- `P` — поменять папку загрузки
- `Up / Down` — выбрать задачу
- `S` — остановить задачу
- `R` — возобновить задачу
- `D` — удалить задачу
- `Esc` — выйти

## Docker Compose и Redis

В `docker-compose.yml` сейчас подняты два сервиса:

- `Redis` — используется приложением для хранения текущего состояния загрузок;
- `PostgreSQL` — пока не участвует в основной логике, но оставлен как задел под историю, статистику и метаданные.

Поднять сервисы:

```bash
make up
```

Остановить сервисы:

```bash
make down
```

Посмотреть логи:

```bash
make logs
```

Если Redis недоступен, клиент не падает и продолжает работу в memory fallback-режиме. Это удобно для локальной отладки, но в таком режиме состояние не переживёт перезапуск приложения.

## Конфигурация

По умолчанию приложение ищет Redis на `127.0.0.1:6379`.

Если нужно переопределить настройки Redis, используй переменные окружения:

```powershell
$env:TORRENT_REDIS_ADDR="127.0.0.1:6379"
$env:TORRENT_REDIS_PASSWORD=""
$env:TORRENT_REDIS_DB="0"
$env:TORRENT_REDIS_PREFIX="go-torrent-cli"
```

Что они значат:

- `TORRENT_REDIS_ADDR` — адрес Redis;
- `TORRENT_REDIS_PASSWORD` — пароль Redis;
- `TORRENT_REDIS_DB` — номер базы;
- `TORRENT_REDIS_PREFIX` — префикс ключей.

## Сборка

### Windows

```powershell
go build -o .\bin\go-torrent-cli.exe .\cmd\app
```

### Linux / macOS

```bash
go build -o ./bin/go-torrent-cli ./cmd/app
```

### Через make

```bash
make build
```

## Тесты

В проекте уже есть unit- и интеграционные тесты для основных частей:

- `internal/torrent` — store, path helper-функции, tracker helper-функции, сортировка и snapshot-логика;
- `internal/control` — разбор аргументов, адресация control API, формат CLI-ответов;
- `internal/ui` — очистка текста, progress bar и отображение статусов.

Запуск всех тестов:

```bash
make test
```

или

```bash
go test ./...
```

## Бенчмарки

В проект добавлены встроенные Go benchmark-тесты. Они меряют внутренние быстрые операции клиента и подходят для локальной проверки или CI.

Запуск:

```bash
make bench
```

или

```bash
go test -run ^$ -bench Benchmark -benchmem ./internal/torrent
```

Локальный прогон от `2026-03-23` на `Windows / AMD Ryzen 5 5500U / Go 1.25`:

| Benchmark | Result | Memory | Allocs |
| --- | --- | --- | --- |
| `BenchmarkSortedIDs100` | `14024 ns/op` | `1848 B/op` | `3 allocs/op` |
| `BenchmarkMemoryStoreSave100` | `18114 ns/op` | `35544 B/op` | `104 allocs/op` |
| `BenchmarkClientGetDownloads100` | `17825 ns/op` | `35544 B/op` | `104 allocs/op` |

### Почему нет сравнения с другими торрент-клиентами

Сетевую скорость загрузки нельзя честно сравнить одной цифрой с `qBittorrent`, `Transmission` или другими клиентами без одинаковых условий:

- один и тот же торрент;
- одинаковое количество живых сидов и пиров;
- одинаковое окно теста;
- одинаковая сеть;
- одинаковые лимиты и настройки клиента.

Поэтому в репозитории оставлены воспроизводимые benchmark-тесты внутренней логики, а не случайные замеры swarm-скорости.

## CI/CD

В репозитории настроены два workflow:

- `ci.yml` — запускается на `push` и `pull_request`, гоняет тесты, benchmark-тесты и сборку на `Windows` и `Ubuntu`;
- `release.yml` — запускается на git-теги вида `v*`, собирает бинарники под `Windows`, `Linux` и `macOS`, а потом публикует GitHub Release.

Как выпустить релиз:

```powershell
git tag v0.1.0
git push origin v0.1.0
```

## Ограничения и замечания

- проект остаётся pet-проектом, а не заменой большим desktop-клиентам;
- скорость загрузки зависит не только от кода, но и от swarm, трекеров и доступных пиров;
- при недоступном Redis состояние живёт только в памяти;
- PostgreSQL пока не используется в основной логике и лежит в `docker-compose.yml` как задел на будущее.

## Topics для GitHub

Для страницы репозитория подойдут такие topics:

- `go`
- `torrent`
- `cli`
- `tui`
- `redis`
- `bubbletea`
- `docker-compose`
- `github-actions`
