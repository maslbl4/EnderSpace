// Экран подключения: поле IP + «Подключить» + последнее значение из конфига.
import { useEffect, useState } from 'react';
import type { FormEvent } from 'react';
import { t } from '../i18n';
import { api } from '../lib/api';

// validIp — формат a.b.c.d (0–255), ничего больше (hostname не поддерживаем).
function validIp(ip: string): boolean {
  const parts = ip.split('.');
  if (parts.length !== 4) {
    return false;
  }
  return parts.every((p) => /^\d{1,3}$/.test(p) && Number(p) <= 255);
}

export default function ConnectionScreen({ onConnected }: { onConnected: () => void }) {
  const [ip, setIp] = useState('');
  const [lastIp, setLastIp] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Последний IP из конфига бэкенда (показ под полем, не подставляется).
  useEffect(() => {
    api
      .status()
      .then((st) => {
        if (!st.Connected && st.IP) {
          setLastIp(st.IP);
        }
      })
      .catch(() => {
        /* бэкенд не готов — без подсказки */
      });
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
      .then(() => onConnected())
      .catch((err: unknown) => setError(err instanceof Error ? err.message : String(err)))
      .finally(() => setBusy(false));
  };

  return (
    <main className="container">
      <header className="brand">
        <h1 className="title">
          <span className="title-accent">{t('appTitle')}</span>
        </h1>
      </header>
      <p className="subtitle">{t('connectSubtitle')}</p>

      <form className="connect" onSubmit={submit}>
        <label className="field-label" htmlFor="printer-ip">
          {t('ipLabel')}
        </label>
        <div className="input-box">
          <input
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
        {lastIp ? (
          <p className="last">
            {t('lastPrinter')}: {lastIp}
          </p>
        ) : null}
      </form>
    </main>
  );
}
