# API-контракт принтера Creality Ender-3 V3 KE (F005)

> Подтверждён на реальном принтере 192.168.3.166 (F005, mac FCEE110432F7)
> инструментом `tools/probe` (`go run ./tools/probe 192.168.3.166`), 2026-11-18.
> Moonraker-стандартные endpoint'ы (`/server/files/list`, `/printer`) — **404**.
> Реальный протокол: кастомный HTTP (Go) + WebSocket на порту 9999.

## HTTP (порт 80)

| Метод | Путь | Ответ |
|---|---|---|
| GET | `/info` | `{"mac":"FCEE110432F7","model":"F005"}` (200, application/json) |
| GET | `/downloads/gcode/<name>` | тело G-кода (200, application/octet-stream, `Content-Length` = `size` из списка). **Range-заголовок игнорируется** — всегда отдаётся весь файл. ВНИМАНИЕ: в начале файла — multipart-обёртка сервера (см. ниже) |
| GET | `/downloads/humbnail/<base>.png` | PNG-превью (200, image/png); `<base>` — имя файла без `.gcode` (URL-экранировать: пробелы, кириллица) |
| POST | `/upload/<name>` | multipart, поле `file`; ответ `{"code":200}` (используется UI принтера; в EnderSpace не нужно) |

### Multipart-обёртка в скачиваемом G-коде

Сервер хранит сырое тело загрузки, поэтому в начале скачиваемого файла —
заголовок multipart (≈200 байт):

```
--------------------------2a0ac08189aaaeb7
Content-Disposition: form-data; name="file"; filename="orcs_3_PLA_2h30m.gcode"
Content-Type: application/octet-stream

; HEADER_BLOCK_START
...
```

Парсер G-кода ищет маркеры комментариями `; ...` — обёртка (строки без `;`)
просто не совпадает с шаблонами и игнорируется.

## WebSocket (порт 9999)

Подключение: `ws://<ip>:9999`. Сообщения — JSON (текст).

### Запрос списка файлов

Запрос:
```json
{"method":"get","params":{"reqGcodeFile":1}}
```

Ответ (поле `retGcodeFileInfo`, приходит асинхронно в потоке push-сообщений):
```json
{ "retGcodeFileInfo": { "totalNum": 58, "fileInfo": "..." } }
```

`fileInfo` — строка; записи разделены `;`, поля внутри записи — `:`:

| # | Поле | Пример | Значение |
|---|---|---|---|
| 0 | path | `\/usr\/data\/printer_data\/gcodes` | каталог (с экранированными слэшами) |
| 1 | name | `orcs_3_PLA_2h30m.gcode` | имя файла |
| 2 | size | `16076907` | размер, байты |
| 3 | layer | `0.100000` | высота слоя, мм |
| 4 | epoch | `1794988291` | unix-time загрузки |
| 5 | length | `3048` | длина филамента, мм |
| 6 | thumbnail | `\/tmp\/creality\/local_gcode\/humbnail\/orcs_3_PLA_2h30m.png` | путь к превью |

Время печати и граммы в списке **не приходят**. Источники (по приоритету):

1. Заголовок G-кода (см. `internal/gcode`): `; estimated printing time …`,
   `; total filament used [g]`, `; filament used [mm]`.
2. **Имя файла**: OrcaSlicer/Bambu дописывают оценку времени в название —
   `orcs_3_PLA_2h30m.gcode` → 2ч 30м (`gcode.TimeFromName`).
3. **Поле `length` из этого списка**: принтер знает длину филамента в каждом
   файле — граммы = `length` × π·(1.75/2)²/1000 × плотность (заголовок или
   1.24 по умолчанию).

> Проверено на реальном принтере 2026-11-19: G-коды OrcaSlicer 2.5.1 **вообще
> не содержат** маркеров времени/грамм (весь файл 10 МБ просмотрен), зато имя
> и `length` дают оба значения. Заголовок содержит только `filament_density`,
   `filament_diameter`, `max_z_height`, `total layer number` + **два** блока
   превью (96×96 и 300×300, ≈55 КБ base64).

### Операции над файлами

Одна операция на сообщение:
```json
{"method":"set","params":{"opGcodeFile":"<операция>"}}
```

| Операция | Формат |
|---|---|
| delete | `deleteprt:<path>/<name>` |
| print  | `printprt:<path>/<name>` |
| rename | `renameprt:<path>/<oldName>:<path>/<newName>` |

Формат операций взят из бандла UI принтера (`/static/js/app.*.js`), который
таким образом управляет теми же операциями; реального удаления в spike не
производилось (на принтере были только рабочие файлы) — проверится в Task 8
на временном файле.

### Периодические пуши состояния

Принтер шлёт push каждые ~1–2 с. Первое сообщение — полный снимок состояния
(~2.4 КБ: `model`, `hostname`, `printProgress`, `printFileName`,
`printJobTime`, `printLeftTime`, `printStartTime`, `nozzleTemp`, `bedTemp0`,
`layer`, `TotalLayer`, `state` и др.), далее — компактные дельты
(`{"nozzleTemp":"210.43","printJobTime":5368,"curPosition":"X:.. Y:.. Z:.."}`
и т.п.). Клиент должен мерджить дельты в накопленное состояние.

Поле `state` — статус работы (индекс в `statusArr` UI принтера, из бандла
`/static/js/app.*.js`):

| state | statusArr | Значение |
|---|---|---|
| 0 | Printing stopped | остановлен (простой) |
| 1 | Printing | печатает |
| 2 | printing complete | завершено |
| 3 | Printing failed | сбой |
| 4 | print abort | прервано |
| 5 | Printing Paused | пауза (работа ещё идёт) |

ВНИМАНИЕ: `printFileName` **остаётся с прошлой печати** и в простое —
метку «печатает» давать только по `state ∈ {1, 5}`.

## Хранение (eMMC)

Эндпоинтов df/storage **нет** (проверено 2026-11-19: `/info` — только
`mac`+`model`; полный WS-снимок состояния — никаких полей ёмкости; бандл
веб-UI принтера — секции ёмкости тоже нет, только `downloads/*` и `upload/`).
Расчёт: total = 8 GB (eMMC, общая с прошивкой — константа из конфига),
used = сумма `size` по списку, system = оценка прошивки/системных файлов
(`systemBytes` в конфиге, дефолт 512 MiB — среднее экспертного диапазона
300–800 МБ), free = total − used − system. Всё, кроме used, отображается
с пометкой «≈» (это оценки).
