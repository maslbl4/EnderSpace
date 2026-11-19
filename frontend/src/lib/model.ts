// Человекочитаемые названия моделей по коду из GET /info
// (таблица stores_gcode_in_usr_data из OrcaSlicer).

const modelNames: Record<string, string> = {
  F001: 'Ender-3 V3 SE/Standard',
  F002: 'Ender-3 V3 Plus',
  F003: 'CR-10 SE',
  F004: 'Ender-5 Max',
  F005: 'Ender-3 V3 KE',
};

// modelName — название модели; неизвестный код — как есть, пусто — «—».
export function modelName(code: string): string {
  if (!code) {
    return '—';
  }
  return modelNames[code] ?? code;
}
