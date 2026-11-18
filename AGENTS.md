# EnderSpace

Менеджер внутренней памяти Creality Ender-3 V3 KE. План: `C:\Users\60230352\.opencode\plan\plan.md`.

## Принтер (тестовый)

- IP: **192.168.3.166**
- Модель по `GET /info`: `F005` (Ender-3 V3 KE), mac `FCEE110432F7`
- Internal storage: 8 GB eMMC (общая с прошивкой)
- Веб-сервер: Go, кастомный (НЕ полный Moonraker: `/server/files/list`, `/printer` — 404)
- Корень UI: `/` — Vue SPA (бандл `/static/js/app.*.js`); API-эндпоинты — искать в бандле
