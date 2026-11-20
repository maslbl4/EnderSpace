# EnderSpace

Менеджер внутренней памяти Creality Ender-3 V3 KE (Go + Wails v3 + React/TS).
Поддерживается стоковая прошивка Creality (HTTP :80 + WS :9999, протокол:
[`docs/api-contract.md`](docs/api-contract.md)) и NebulaOS (Klipper + Moonraker, :7125).
Драйвер определяется автоматически при подключении (см. «Драйверы принтеров»).

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

## Эмулятор принтера (без железа)

In-memory реализация `printer.Printer` (8 образцовых G-кодов, печать ускорена
до 1 минуты) — весь UI-флоу проверяется без принтера:

```powershell
$env:ENDERSPACE_EMULATOR=1; wails3 dev        # dev
build\bin\ender-space.exe -emulator           # собранное exe
$env:ENDERSPACE_EMU_PRINT_MS=10000            # (опц.) печать за 10 с
```

IP из формы подключения принимается любой.

## Драйверы принтеров

`internal/printer` — общий интерфейс `printer.Printer` и общие типы; каждый
драйвер — соседний пакет, слой приложения и фронтенд не меняются:

- `internal/printer/creality` — стоковая прошивка F005 (HTTP :80 + WS :9999),
  протокол: [`docs/api-contract.md`](docs/api-contract.md);
- `internal/printer/nebulaos` — NebulaOS (Klipper + Moonraker: REST + WS
  JSON-RPC на :7125). Версия ОС — `GET /machine/nebulaos/info`
  (`build.nebulaos_version`), фолбэк — `klipper_version` из `/server/info`.
  Путь файла в Moonraker — относительно gcode-корня; драйвер мапит его в
  `File.Path` с префиксом `gcodes/`, поэтому «Path/Name» совпадает с
  `print_stats.filename`.

Детект драйвера при `Connect` — `internal/app/detect.go`: сначала зонд
Creality (`GET /info`), не вышло — NebulaOS (`GET /server/info`). Выбранный
драйвер запоминается в конфиге (`lastDriver`, рядом с `lastIP`) и при
повторном подключении к тому же IP зондится первым. Живая проверка
NebulaOS: `TestLiveNebulaos` с `ENDERSPACE_LIVE=1` (после прошивки принтера;
сейчас — только mock-тесты на httptest).

## Ограничения окружения

- PowerShell: `curl` = `Invoke-WebRequest` (использовать `curl.exe`)
- Wails v3 CLI: `go install github.com/wailsapp/wails/v3/cmd/wails3@latest`
