# go-torrent-cli

Small CLI torrent client in Go.

## What it uses

- Redis for current download state.
- PostgreSQL is ready in docker compose for future app data.

## Quick start

1. Install [Go 1.25+](https://go.dev/doc/install)
2. Install [Docker Desktop](https://www.docker.com/products/docker-desktop/)
3. Install [`make`](https://gnuwin32.sourceforge.net/packages/make.htm)
4. Start services:

```bash
make up
```

5. Run the client:

```bash
make run
```

## Main commands

```bash
make up
make down
make restart
make logs
make ps
make run
make build
make test
make clean
```

## How it works

- `make up` starts Redis and PostgreSQL from `docker-compose.yml`.
- The app uses Redis on `127.0.0.1:6379` by default.
- If Redis is not ready, the app still works, but state stays only in memory.
- PostgreSQL is not used in code yet. It is there for future features.

## Run app

```powershell
go run .\cmd\app\main.go
```

## Add torrent

```powershell
go run .\cmd\app\main.go C:\path\movie.torrent
```

## Add magnet

```powershell
go run .\cmd\app\main.go "magnet:?xt=..."
```

## Build

For Windows:

```powershell
go build -o .\bin\go-torrent-cli.exe .\cmd\app
```

For Linux or macOS:

```bash
go build -o ./bin/go-torrent-cli ./cmd/app
```

Or with make:

```bash
make build
```

## Redis settings

Use environment vars if you want custom Redis settings.

```powershell
$env:TORRENT_REDIS_ADDR="127.0.0.1:6379"
$env:TORRENT_REDIS_PASSWORD=""
$env:TORRENT_REDIS_DB="0"
$env:TORRENT_REDIS_PREFIX="go-torrent-cli"
```
