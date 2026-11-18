# API-контракт принтера Creality Ender-3 V3 KE (F005)

> Черновик на основе spike (вручную, 2026-11-18) против реального принтера 192.168.3.166.
> Формализовать в Task 2 (гейт: повторная проверка `tools/probe`).

## Модель

Протокол принтера — НЕ Moonraker REST. Кастомный веб-сервер (Go) + WebSocket.

## HTTP (порт 80)

| Метод | Путь | Ответ |
|---|---|---|
| GET | `/info` | `{"mac":"FCEE110432F7","model":"F005"}` |
| GET | `/downloads/gcode/<name>` | тело G-кода (200, размер = `size` из списка). ВНИМАНИЕ: в начале файла — multipart-обёртка (сервер хранит сырое тело загрузки) |
| GET | `/downloads/humbnail/<name>.png` | PNG 96×96 |
| POST | `/upload/<name>` | multipart, поле `file`; ответ `{"code":200}` |

## WebSocket (порт 9999)

Подключение: `ws://<ip>:9999`. Сообщения — JSON.

### Запрос списка файлов

Запрос:
```json
{"method":"get","params":{"reqGcodeFile":1}}
```

Ответ (поле `retGcodeFileInfo`):
```json
{"retGcodeFileInfo":{"totalNum":58,"fileInfo":"..."}}
```

`fileInfo` — строка; записи разделены `;`, поля внутри записи — `:`:

| # | Поле | Пример |
|---|---|---|
| 0 | path | `\/usr\/data\/printer_data\/gcodes` |
| 1 | name | `orcs_3_PLA_2h30m.gcode` |
| 2 | size (байты) | `16076907` |
| 3 | layer (мм) | `0.100000` |
| 4 | epoch (создание/загрузка) | `1794988291` |
| 5 | length — длина филамента (мм) | `3048` |
| 6 | thumbnail path | `\/tmp\/creality\/local_gcode\/humbnail\/orcs_3_PLA_2h30m.png` |

Время печати и граммы из списка НЕ приходят — берутся из заголовка G-кода:
- `; estimated printing time (normal mode) = 2h 29m 52s`
- `; total filament used [g] = 9.09`
- `; filament used [mm] = 3048.81` (совпадает с полем 5)

### Операции над файлами

Операция (одна на сообщение):
```json
{"method":"set","params":{"opGcodeFile":"<операция>"}}
```

| Операция | Формат |
|---|---|
| delete | `deleteprt:<path>/<name>` |
| print  | `printprt:<path>/<name>` |
| rename | `renameprt:<path>/<oldName>:<path>/<newName>` |

### Периодические пуши состояния

Принтер шлёт push каждые ~1–2 с со всеми параметрами; релевантные поля:
`printProgress`, `printJobTime`, `printLeftTime`, `printFileName`, `state`,
`nozzleTemp`, `bedTemp0`, `layer`, `TotalLayer`, `model`, `hostname`.

## Хранение (eMMC)

Эндпоинтов `df`/storage нет. total = 8 GB (константа из конфига, общая с прошивкой),
used = сумма `size`, free = total − used. Показывать с «≈».
