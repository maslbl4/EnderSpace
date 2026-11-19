// Словарь строк интерфейса (русский).
// Все строки UI идут через t(key) — прямых литералов в разметке быть не должно.

const ru = {
  appTitle: 'EnderSpace',
  connectSubtitle: 'Менеджер внутренней памяти принтера Creality',
  ipLabel: 'IP-адрес принтера',
  ipPlaceholder: '192.168.3.100',
  connect: 'Подключить',
  connecting: 'Подключение…',
  lastPrinter: 'Последний адрес',
  errorBadIP: 'Введите IP в формате 192.168.3.100',
  connected: 'Подключено',
  printing: 'Печатает',
  disconnect: 'Отключить',
  mainSoon: 'Память и файлы появятся в следующем шаге',
} as const;

export type I18nKey = keyof typeof ru;

export function t(key: I18nKey): string {
  return ru[key];
}
