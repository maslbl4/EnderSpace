// Роутинг состояний: нет подключения → ConnectionScreen, есть → основной экран.
// Основной экран: память + таблица файлов грузятся сразу после подключения.
import { useCallback, useEffect, useState } from 'react';
import ConnectionScreen from './components/ConnectionScreen';
import FilesTable from './components/FilesTable';
import StorageCard from './components/StorageCard';
import { t } from './i18n';
import { api } from './lib/api';
import type { FileMeta, PrinterStatus, Storage } from './lib/api';
import { modelName } from './lib/model';

function App() {
  const [connected, setConnected] = useState(false);

  if (!connected) {
    return <ConnectionScreen onConnected={() => setConnected(true)} />;
  }
  return <MainScreen onDisconnected={() => setConnected(false)} />;
}

function MainScreen({ onDisconnected }: { onDisconnected: () => void }) {
  const [status, setStatus] = useState<PrinterStatus | null>(null);
  const [storage, setStorage] = useState<Storage | null>(null);
  const [files, setFiles] = useState<FileMeta[] | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);

  const loading = files === null && !loadError;

  // Память и список файлов параллельно; ошибка — баннер с «Повторить».
  const load = useCallback(async () => {
    setLoadError(null);
    setFiles(null);
    try {
      const [st, fl] = await Promise.all([api.storage(), api.listFiles()]);
      setStorage(st);
      setFiles(fl);
    } catch (err: unknown) {
      setFiles(null);
      setLoadError(err instanceof Error ? err.message : String(err));
    }
  }, []);

  // Первичная загрузка + ⟳.
  useEffect(() => {
    load();
  }, [load]);

  // Состояние принтера: обрыв связи возвращает на экран подключения.
  useEffect(() => {
    let alive = true;
    const refresh = () => {
      api
        .status()
        .then((st) => {
          if (!alive) {
            return;
          }
          setStatus(st);
          if (!st.Connected) {
            onDisconnected();
          }
        })
        .catch(() => {
          /* бэкенд занят — следующий цикл */
        });
    };
    refresh();
    const id = setInterval(refresh, 3000);
    return () => {
      alive = false;
      clearInterval(id);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const disconnect = () => {
    api
      .disconnect()
      .catch(() => {})
      .finally(onDisconnected);
  };

  const printing = !!status?.Printing;
  const label =
    status?.IP
      ? `${modelName(status.Model)} · ${status.IP}`
      : t('connectSubtitle');

  return (
    <main className="container main">
      <header className="main-head">
        <div className="brand">
          <h1 className="title">
            <span className="title-accent">{t('appTitle')}</span>
          </h1>
          <span className={`badge ${printing ? 'badge-warn' : ''}`}>
            <span className={`dot ${printing ? 'dot-warn' : ''}`} />
            {printing ? t('printing') : t('online')}
          </span>
        </div>
        <div className="head-actions">
          <button
            className="icon-btn"
            title={t('refresh')}
            aria-label={t('refresh')}
            onClick={load}
            disabled={loading}
          >
            ⟳
          </button>
          <button className="btn btn-ghost" onClick={disconnect}>
            {t('disconnect')}
          </button>
        </div>
      </header>
      <p className="subtitle main-subtitle">{label}</p>

      {printing ? <div className="banner banner-warn">{t('printingBanner')}</div> : null}
      {loadError ? (
        <div className="banner banner-error">
          <span className="banner-text">{loadError}</span>
          <button className="btn btn-small" onClick={load}>
            {t('retry')}
          </button>
        </div>
      ) : null}

      {storage ? <StorageCard storage={storage} /> : loading ? (
        <div className="card storage-card">
          <div className="skeleton-row" />
        </div>
      ) : null}

      <FilesTable files={files ?? []} loading={loading} />
    </main>
  );
}

export default App;
