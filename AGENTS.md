# EnderSpace

Менеджер внутренней памяти Creality Ender-3 V3 KE (Go + Wails v3 + React/TS).
Протокол принтера: [`docs/api-contract.md`](docs/api-contract.md) (HTTP :80 + WS :9999, не Moonraker).

## Сборка и тесты (Windows, нативно)

```powershell
$env:TMP=$env:TEMP="C:\Users\YOUR_USER\go-cache"   # AV-обход: линковка Go exe
wails3 dev        # dev-режим (vite :9245 + авто-пересборка Go)
wails3 build      # → build/bin/ender-space.exe
```

```bash
gofmt -l internal/ main.go
go vet ./internal/...
go test ./internal/... -count=1
```

`go build ./...` сломан шаблоном `build/ios` — собирать `go build .` (фронтенд эмбедится из `frontend/dist`).

## Тестовый принтер

- Модель по `GET /info`: `F005` (Ender-3 V3 KE), eMMC 8 GB (общая с прошивкой)
- Живые проверки — как Go-тесты с `ENDERSPACE_LIVE=1` (см. `internal/app/*_test.go`)

## Ограничения окружения

- PowerShell: `curl` = `Invoke-WebRequest` (использовать `curl.exe`)
- Wails v3 CLI: `go install github.com/wailsapp/wails/v3/cmd/wails3@latest`
