// Форматирование значений для отображения. Единицы и разделители —
// по текущему языку (i18n): ru «4,2 МБ / 9ч 20м / 1 240 г»,
// en «4.2 MB / 9h 20m / 1,240 g».
import { getLang } from '../i18n';

const RU = {
  units: ['Б', 'КБ', 'МБ', 'ГБ'],
  dot: ',',
  thousands: ' ',
  h: 'ч',
  m: 'м',
  s: 'с',
  g: 'г',
} as const;

const EN = {
  units: ['B', 'KB', 'MB', 'GB'],
  dot: '.',
  thousands: ',',
  h: 'h',
  m: 'm',
  s: 's',
  g: 'g',
} as const;

const loc = () => (getLang() === 'en' ? EN : RU);

// formatBytes: до 1023 — «Б»/«B», иначе 1 знак после десятичного разделителя.
export function formatBytes(bytes: number): string {
  const L = loc();
  let v = bytes;
  let i = 0;
  while (v >= 1024 && i < L.units.length - 1) {
    v /= 1024;
    i++;
  }
  const s = i === 0 ? String(Math.round(v)) : v.toFixed(1).replace('.', L.dot);
  return `${s} ${L.units[i]}`;
}

// formatDuration: «9ч 20м» / «9h 20m», «42м» / «42m», «35с» / «35s».
export function formatDuration(totalSeconds: number): string {
  const L = loc();
  const h = Math.floor(totalSeconds / 3600);
  const m = Math.round((totalSeconds % 3600) / 60);
  if (h > 0) {
    return `${h}${L.h} ${m}${L.m}`;
  }
  if (m > 0) {
    return `${m}${L.m}`;
  }
  return `${Math.max(1, Math.round(totalSeconds))}${L.s}`;
}

// formatGrams: «18 г» / «18 g», «45,2 г» / «45.2 g», «1 240 г» / «1,240 g».
export function formatGrams(grams: number): string {
  const L = loc();
  const s =
    grams >= 100 ? String(Math.round(grams)) : grams.toFixed(1).replace('.', L.dot);
  return `${s.replace(/\B(?=(\d{3})+(?!\d))/g, L.thousands)} ${L.g}`;
}
