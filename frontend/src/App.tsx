// Роутинг состояний: нет подключения → ConnectionScreen, есть → основной экран.
// Основной экран: память + таблица файлов грузятся сразу после подключения;
// выбор файлов (чекбоксы) → подтверждение → удаление → авто-refresh.
import { useCallback, useEffect, useMemo, useState } from 'react';
import ConnectionScreen from './components/ConnectionScreen';
import ConfirmDialog from './components/ConfirmDialog';
import FilesTable, { fileKey } from './components/FilesTable';
import StorageCard from './components/StorageCard';
import { plural, t, useI18n } from './i18n';
import { api } from './lib/api';
import type { FileMeta, PrinterStatus, Storage } from './lib/api';
import { formatBytes } from './lib/format';
import { modelName } from './lib/model';
import LangToggle from './components/LangToggle';

function App() {
  const [connected, setConnected] = useState(false);

  if (!connected) {
    return <ConnectionScreen onConnected={() => setConnected(true)} />;
  }
  return <MainScreen onDisconnected={() => setConnected(false)} />;
}

function MainScreen({ onDisconnected }: { onDisconnected: () => void }) {
  useI18n(); // перерисовка при смене языка
  const [status, setStatus] = useState<PrinterStatus | null>(null);
  const [storage, setStorage] = useState<Storage | null>(null);
  const [files, setFiles] = useState<FileMeta[] | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [selected, setSelected] = useState<ReadonlySet<string>>(new Set());
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [toast, setToast] = useState<string | null>(null);

  const loading = files === null && !loadError;

  // Память и список файлов параллельно; ошибка — баннер с «Повторить».
  // После перезагрузки выбор обрезаем до существующих файлов.
  const load = useCallback(async () => {
    setLoadError(null);
    setFiles(null);
    try {
      const [st, fl] = await Promise.all([api.storage(), api.listFiles()]);
      setStorage(st);
      setFiles(fl);
      setSelected((prev) => {
        const keys = new Set(fl.map(fileKey));
        return new Set([...prev].filter((k) => keys.has(k)));
      });
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

  // Тост гаснет сам.
  useEffect(() => {
    if (!toast) {
      return;
    }
    const id = setTimeout(() => setToast(null), 8000);
    return () => clearTimeout(id);
  }, [toast]);

  const disconnect = () => {
    api
      .disconnect()
      .catch(() => {})
      .finally(onDisconnected);
  };

  const toggle = (k: string) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(k)) {
        next.delete(k);
      } else {
        next.add(k);
      }
      return next;
    });
  };

  const toggleAll = () => {
    setSelected((prev) =>
      prev.size === (files?.length ?? 0) ? new Set<string>() : new Set((files ?? []).map(fileKey)),
    );
  };

  const selectedList = useMemo(
    () => (files ?? []).filter((f) => selected.has(fileKey(f))),
    [files, selected],
  );
  const selectedBytes = selectedList.reduce((s, f) => s + f.Size, 0);

  const printing = !!status?.Printing;

  // Удаление: бэкенд шлёт «deleted k of n: …» при частичном сбое —
  // по числам локализируем отчёт; полный успех — молча refresh.
  const doDelete = async () => {
    const targets = selectedList.map(fileKey);
    setDeleting(true);
    try {
      await api.deleteFiles(targets);
      setToast(null);
      setSelected(new Set());
      setConfirmOpen(false);
      load();
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : String(err);
      const m = /deleted (\d+) of (\d+)/.exec(msg);
      if (m) {
        setToast(t('deletedPartial').replace('%d', m[1]).replace('%d', m[2]));
      } else {
        setToast(`${t('deleteFailed')}${msg}`);
      }
      setConfirmOpen(false);
      load();
    } finally {
      setDeleting(false);
    }
  };

  const label = status?.IP
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
          <LangToggle />
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

      <FilesTable
        files={files ?? []}
        loading={loading}
        selected={selected}
        onToggle={toggle}
        onToggleAll={toggleAll}
      />

      {files !== null && !loading ? (
        <div className="footer-bar">
          <span className="footer-info">
            {t('selected')}: {selectedList.length}{' '}
            {plural(selectedList.length, [t('fileOne'), t('fileFew'), t('fileMany')])}
            {selectedList.length > 0 ? ` · ${formatBytes(selectedBytes)}` : ''}
          </span>
          <button
            className="btn btn-danger"
            disabled={selectedList.length === 0 || printing || deleting}
            title={printing ? t('printingBanner') : undefined}
            onClick={() => setConfirmOpen(true)}
          >
            {t('deleteSelected')}
          </button>
        </div>
      ) : null}

      {confirmOpen ? (
        <ConfirmDialog
          names={selectedList.map((f) => f.Name)}
          bytes={selectedBytes}
          deleting={deleting}
          onCancel={() => setConfirmOpen(false)}
          onConfirm={doDelete}
        />
      ) : null}

      {toast ? (
        <div className="toast" role="status">
          <span className="banner-text">{toast}</span>
          <button className="btn btn-small" onClick={() => setToast(null)}>
            ×
          </button>
        </div>
      ) : null}
    </main>
  );
}

export default App;
