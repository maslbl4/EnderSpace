// Форматирование значений для отображения (ru-локаль: запятая, пробелы в разрядах).

const UNITS = ['Б', 'КБ', 'МБ', 'ГБ'] as const;

// formatBytes: до 1023 — «Б», иначе 1 знак после запятой: «4,2 МБ», «1,5 ГБ».
export function formatBytes(bytes: number): string {
  let v = bytes;
  let i = 0;
  while (v >= 1024 && i < UNITS.length - 1) {
    v /= 1024;
    i++;
  }
  const s = i === 0 ? String(Math.round(v)) : v.toFixed(1).replace('.', ',');
  return `${s} ${UNITS[i]}`;
}

// formatDuration: «9ч 20м» / «42м» / «35с».
export function formatDuration(totalSeconds: number): string {
  const h = Math.floor(totalSeconds / 3600);
  const m = Math.round((totalSeconds % 3600) / 60);
  if (h > 0) {
    return `${h}ч ${m}м`;
  }
  if (m > 0) {
    return `${m}м`;
  }
  return `${Math.max(1, Math.round(totalSeconds))}с`;
}

// formatGrams: «18 г», «45,2 г», «1 240 г» (пробелы в разрядах).
export function formatGrams(grams: number): string {
  const s =
    grams >= 100 ? String(Math.round(grams)) : grams.toFixed(1).replace('.', ',');
  return `${s.replace(/\B(?=(\d{3})+(?!\d))/g, ' ')} г`;
}
