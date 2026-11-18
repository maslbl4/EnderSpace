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
| GET | `/downloads/humbnail/<name>.png` | PNG 96×96 (200, image/png) |
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

Время печати и граммы в списке **не приходят** — берутся из заголовка G-кода
(см. `internal/gcode`), либо граммы считаются из `length` × плотности.

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

## Хранение (eMMC)

Эндпоинтов df/storage **нет** (проверено: `/info` и `/printer` ничего не
отдают о ёмкости). Расчёт: total = 8 GB (eMMC, общая с прошивкой — константа
из конфига), used = сумма `size` по списку, free = total − used.
Отображается с пометкой «≈» (это оценка: eMMC ещё едят прошивка и система).
