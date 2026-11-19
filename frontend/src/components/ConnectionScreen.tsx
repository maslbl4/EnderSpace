// Экран подключения: поле IP + «Подключить» + недавние принтеры (localstorage).
import { useEffect, useRef, useState } from 'react';
import type { FormEvent } from 'react';
import { t, useI18n } from '../i18n';
import { api } from '../lib/api';
import LangToggle from './LangToggle';

// recentIPs — IP успешно подключённых принтеров в localstorage:
// свежие вперёд, без дублей, максимум RECENT_LIMIT.
const RECENT_KEY = 'enderspace.recentIPs';
const RECENT_LIMIT = 5;

function loadRecent(): string[] {
  try {
    const raw = localStorage.getItem(RECENT_KEY);
    if (!raw) {
      return [];
    }
    const list = JSON.parse(raw);
    return Array.isArray(list) ? list.filter((v) => typeof v === 'string') : [];
  } catch {
    return []; // localstorage недоступен/мусор — без подсказок
  }
}

function saveRecent(ip: string): string[] {
  const next = [ip, ...loadRecent().filter((v) => v !== ip)].slice(0, RECENT_LIMIT);
  try {
    localStorage.setItem(RECENT_KEY, JSON.stringify(next));
  } catch {
    /* запись не критична */
  }
  return next;
}

// normalizeIp — то же, что нормализация в Go: без scheme и хвостового "/".
function normalizeIp(ip: string): string {
  const v = ip.trim();
  return v.replace(/^https?:\/\//, '').replace(/\/+$/, '');
}

// validIp — формат a.b.c.d (0–255), ничего больше (hostname не поддерживаем).
function validIp(ip: string): boolean {
  const parts = ip.split('.');
  if (parts.length !== 4) {
    return false;
  }
  return parts.every((p) => /^\d{1,3}$/.test(p) && Number(p) <= 255);
}

export default function ConnectionScreen({ onConnected }: { onConnected: () => void }) {
  useI18n(); // перерисовка при смене языка
  const [ip, setIp] = useState('');
  const [recent, setRecent] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    setRecent(loadRecent());
  }, []);

  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (busy) {
      return;
    }
    const value = ip.trim();
    if (!validIp(value)) {
      setError(t('errorBadIP'));
      return;
    }
    setBusy(true);
    setError(null);
    api
      .connect(value)
      .then(() => {
        setRecent(saveRecent(normalizeIp(value))); // только успешные подключения
        onConnected();
      })
      .catch((err: unknown) => setError(err instanceof Error ? err.message : String(err)))
      .finally(() => setBusy(false));
  };

  const pick = (v: string) => {
    setIp(v);
    setError(null);
    inputRef.current?.focus();
  };

  return (
    <main className="container">
      <header className="connect-head">
        <div className="brand">
          <h1 className="title">
            <span className="title-accent">{t('appTitle')}</span>
          </h1>
        </div>
        <LangToggle />
      </header>
      <p className="subtitle">{t('connectSubtitle')}</p>

      <form className="connect" onSubmit={submit}>
        <label className="field-label" htmlFor="printer-ip">
          {t('ipLabel')}
        </label>
        <div className="input-box">
          <input
            ref={inputRef}
            id="printer-ip"
            className="input"
            type="text"
            inputMode="decimal"
            autoComplete="off"
            spellCheck={false}
            placeholder={t('ipPlaceholder')}
            value={ip}
            disabled={busy}
            onChange={(e) => {
              setIp(e.target.value);
              if (error) {
                setError(null);
              }
            }}
          />
          <button className="btn" type="submit" disabled={busy}>
            {busy ? t('connecting') : t('connect')}
          </button>
        </div>
        {error ? <p className="error">{error}</p> : null}
        {recent.length > 0 ? (
          <div className="recent">
            <span className="recent-label">{t('recentPrinters')}:</span>
            {recent.map((v) => (
              <button
                key={v}
                type="button"
                className="recent-chip"
                title={v}
                disabled={busy}
                onClick={() => pick(v)}
              >
                {v}
              </button>
            ))}
          </div>
        ) : null}
      </form>
    </main>
  );
}
