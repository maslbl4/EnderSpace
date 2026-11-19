// i18n: выбор языка (русский / английский). Единая точка входа — t(key).
// Язык хранится в localStorage и переключается в любое время:
// компоненты, вызывающие useI18n(), перерисовываются при смене.
import { useSyncExternalStore } from 'react';
import { ru } from './ru';
import type { I18nKey } from './ru';
import { en } from './en';

export type { I18nKey } from './ru';

export type Lang = 'ru' | 'en';

const dicts: Record<Lang, Record<I18nKey, string>> = { ru, en };

const STORAGE_KEY = 'enderspace.lang';

function initialLang(): Lang {
  try {
    const v = localStorage.getItem(STORAGE_KEY);
    if (v === 'ru' || v === 'en') {
      return v;
    }
  } catch {
    /* localStorage недоступен — русский по умолчанию */
  }
  return 'ru';
}

let lang: Lang = initialLang();
const listeners = new Set<() => void>();

export function getLang(): Lang {
  return lang;
}

export function setLang(l: Lang): void {
  if (l === lang) {
    return;
  }
  lang = l;
  try {
    localStorage.setItem(STORAGE_KEY, l);
  } catch {
    /* игнорируем: язык просто не сохранится */
  }
  listeners.forEach((f) => f());
}

export function t(key: I18nKey): string {
  return dicts[lang][key];
}

// plural — плюрализация: ru по русской таблице окончаний, en — ед./мн.
export function plural(n: number, forms: [string, string, string]): string {
  if (lang === 'en') {
    return n === 1 ? forms[0] : forms[1];
  }
  const n10 = n % 10;
  const n100 = n % 100;
  if (n10 === 1 && n100 !== 11) {
    return forms[0];
  }
  if (n10 >= 2 && n10 <= 4 && (n100 < 12 || n100 > 14)) {
    return forms[1];
  }
  return forms[2];
}

// useI18n — подписка на смену языка (перерисовка компонента).
export function useI18n(): Lang {
  return useSyncExternalStore(
    (cb) => {
      listeners.add(cb);
      return () => {
        listeners.delete(cb);
      };
    },
    () => lang,
  );
}
