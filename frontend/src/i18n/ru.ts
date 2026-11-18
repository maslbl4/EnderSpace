// Словарь строк интерфейса (русский).
// Все строки UI идут через t(key) — прямых литералов в разметке быть не должно.

const ru = {
  appTitle: 'EnderSpace',
  placeholder: 'Подключение к принтеру… (скоро)',
} as const;

export type I18nKey = keyof typeof ru;

export function t(key: I18nKey): string {
  return ru[key];
}
