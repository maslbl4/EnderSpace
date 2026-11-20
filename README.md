# EnderSpace

> Менеджер внутренней памяти Creality Ender-3 V3 KE: файлы, время печати, граммы, удаление.

![Platform](https://img.shields.io/badge/platform-Windows-blue)
![Wails](https://img.shields.io/badge/Wails-v3-purple)
![License](https://img.shields.io/badge/license-MIT-green)

---

## About (English)

A desktop app (Go + WebView via [Wails v3](https://v3.wails.io)) for managing the internal
storage of a Creality Ender-3 V3 KE printer:

- Internal storage: used / free / total (8 GB eMMC, shared with firmware — values are
  estimates, with a separate estimated firmware/system line)
- G-code file list: name, size, print time, filament in grams (parsed from the G-code header,
  with fallbacks), high-quality previews
- Batch file deletion with a confirmation dialog and a partial-failure report
- Russian / English interface (toggle in the header, choice is remembered)

The app supports both operating systems of the Ender-3 V3 KE and picks the
right driver automatically on connect:

- **Creality OS** (stock firmware) — the printer's own HTTP :80 + WebSocket :9999
  protocol, documented in [`docs/api-contract.md`](docs/api-contract.md);
- **NebulaOS** (Klipper + Moonraker) — standard Moonraker REST + WebSocket JSON-RPC
  on port 7125.

No extra servers, a single self-contained Windows binary.

Building, debugging and configuration docs are below (in Russian).

---

## Почему EnderSpace?

Веб-интерфейс принтера не показывает время печати и расход филамента по файлам, а удаление — по одному файлу, без подстраховки. EnderSpace — **один самодостаточный бинарник (Go + WebView)** без серверов и Docker.

| | EnderSpace | Веб UI принтера |
|---|---|---|
| Время печати / граммы по файлу | **Да** (из заголовка G-кода) | Нет |
| Массовое удаление | **Да**, с отчётом «удалено k из n» | По одному |
| Последнее подключение | Запоминается в конфиге | Нет |
| Зависимости | Go + WebView2 (есть в Win 11) | — |

**Работает с двумя ОС принтера**: Creality OS (стоковая прошивка, HTTP :80 + WS :9999 — протокол задокументирован в [`docs/api-contract.md`](docs/api-contract.md)) и NebulaOS (Klipper + Moonraker, порт 7125). Драйвер определяется автоматически при подключении.

---

## Возможности

**Подключение**
- Ручной IP (запоминается в `config.json`)
- Creality OS (стоковая прошивка) и NebulaOS (Klipper + Moonraker) — драйвер выбирается автоматически

**Память**
- Занято / свободно / всего для 8 GB eMMC (оценка «≈» — память общая с прошивкой)
- Занято = сумма размеров файлов
- Система (прошивка + системные файлы) — отдельной строкой, оценка
  (`systemBytes` в конфиге, дефолт 512 МБ; принтер реальный объём не отдаёт)

**Файлы**
- Имя, размер, время печати, граммы филамента
- Время и граммы — из заголовка G-кода (паттерны OrcaSlicer; fallback: длина филамента × плотность × сечение)
- Кэш по `(путь, размер)` — повторный Refresh не перескачивает G-коды

**Удаление**
- Массовое, последовательное, с отчётом о частичном успехе
- Блокируется, пока принтер печатает

**Язык**
- Русский / английский, переключается кнопкой в шапке (выбор запоминается)

---

## Быстрый старт

1. Соберите приложение (ниже) или запустите dev-режим: `wails3 dev`.
2. Введите IP принтера (например `192.168.3.166`) → **Подключить**.
3. В шапке появится состояние (модель, занят ли принтер печатью) и память.
4. В таблице — файлы: имя, размер, время печати, граммы.
5. Отметьте файлы → **Удалить** (подтверждение) → отчёт о результате.

---

## Сборка

**Пререквизиты:** Go 1.26+, Node.js 18+, Wails v3 CLI, Windows 11 (сборка нативная, без WSL).

```powershell
# Wails v3 CLI (если ещё не установлен)
go install github.com/wailsapp/wails/v3/cmd/wails3@latest
```

```bash
# 1. Зависимости фронтенда (один раз)
cd frontend && npm install && cd ..

# 2. Dev-режим: hot reload фронтенда (vite) + авто-пересборка Go
wails3 dev

# 3. Продакшен-сборка текущей платформы
wails3 build        # → build/bin/ender-space.exe
```

**Обычный `go build`** (в CI, скриптах):

```bash
cd frontend && npm run build && cd ..   # фронтенд эмбедится из frontend/dist
go build .            # → ender-space.exe
```

> `go build ./...` не работает: каталог `build/ios` — шаблон Wails, не компилируемый на Windows. Собирайте `.` (корневой пакет).

**Тесты и качество** (нативно, Windows):

```bash
gofmt -l internal/ main.go     # должно быть пусто
go vet ./internal/...
go test ./internal/... -count=1
```

**После изменения публичных методов `App`** (`internal/app`) перегенерируйте TS-биндинги:

```bash
wails3 generate bindings       # → frontend/bindings/ (DO NOT EDIT)
```

Генератор сам вырезает `context.Context` из сигнатур — на фронтенде методы чистые: `Connect(ip)`, `ListFiles()`, `DeleteFiles(paths)`.

---

## Отладка

**Dev-режим** — основной рабочий цикл:

```bash
wails3 dev
```

- Фронтенд: vite dev server, hot reload.
- Go: пересборка и рестарт по изменению `.go` (каталог `frontend/` в игноре — фронт идёт через vite).

**Брейкпоинты в Go** — любой IDE с поддержкой Delve (IntelliJ IDEA + Go plugin, VS Code + Go extension):

- Запустите корневой пакет `.` в режиме отладки (нужен собранный `frontend/dist` — `go build .` / `wails3 build` его кладут в эмбед).
- Или в терминале: `dlv debug .` (`go install github.com/go-delve/delve/cmd/dlv@latest`).

**Диагностика протокола принтера** — без запуска приложения:

```bash
go run ./tools/probe 192.168.3.166 [имя-файла.gcode]
```

Проходит все точки контракта Creality OS: `/info`, WS-список файлов, скачивание G-кода, превью. Полное описание — в [`docs/api-contract.md`](docs/api-contract.md).

**Сетевые требования**

| Канал | Где | Для чего |
|---|---|---|
| HTTP :80 | принтер (Creality OS) | `/info`, скачивание G-кодов и превью |
| WebSocket :9999 | принтер (Creality OS) | список файлов, состояние, удаление |
| HTTP :7125 | принтер (NebulaOS) | `/server/info`, файлы, превью, версия ОС |
| WebSocket :7125 | принтер (NebulaOS) | состояние (`print_stats`), удаление (JSON-RPC) |

---

## Конфигурация

| Платформа | Каталог |
|---|---|
| Windows | `%APPDATA%\EnderSpace\config.json` |

| Поле | Описание |
|---|---|
| `lastIP` | IP последнего принтера (подстановка при запуске) |
| `totalBytes` | Общая ёмкость eMMC; не задан → 8 GiB |
| `systemBytes` | Оценка прошивки + системных файлов; не задан → 512 MiB |

---

## Структура проекта

```
main.go                  точка входа Wails: регистрирует *app.App
internal/app/            сервисный слой (Connect/Storage/ListFiles/DeleteFiles)
internal/printer/        интерфейс Printer + драйверы ОС: Creality (HTTP+WS :9999), NebulaOS (Moonraker :7125)
internal/gcode/          парсер заголовка G-кода (время печати, граммы)
internal/config/         config.json
frontend/                React 18 + TypeScript + Vite
  bindings/              сгенерированные TS-биндинги (wails3 generate bindings)
tools/probe/             диагностический CLI по контракту принтера
docs/api-contract.md     контракт протокола (исследование живого принтера)
```

Тесты — рядом с кодом (`*_test.go`); сетевые части покрыты мок-клиентами и httptest, живой принтер не нужен.

---

## Tech Stack

| Слой | Технология |
|---|---|
| Нативное окно + RPC-мост | [Wails v3](https://v3.wails.io) |
| Бэкенд | Go 1.26 |
| WS-клиент | [gorilla/websocket](https://github.com/gorilla/websocket) |
| UI | React 18 + TypeScript + Vite |
| Локализация | `frontend/src/i18n/` (ru + en), `t(key)`, переключатель RU/EN в шапке |

---

## Вклад

Pull requests приветствуются. Правила, которые легко нарушить:

1. **После изменения методов `App`** — `wails3 generate bindings` (файлы `frontend/bindings/` не править руками).
2. **Все строки UI** — через `i18n/` (`t(key)`; новые ключи добавлять в `ru.ts` и `en.ts`), без хардкода.
3. **Бэкенд без Wails**: сервисная логика живёт в `internal/` и тестируется без приложения; `main.go` остаётся тонким.
4. Перед коммитом: `gofmt`, `go vet ./internal/...`, `go test ./internal/... -count=1`.
