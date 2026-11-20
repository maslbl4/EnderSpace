// Роутинг состояний: нет подключения → ConnectionScreen, есть → основной экран.
// Основной экран: память + таблица файлов грузятся сразу после подключения;
// выбор файлов (чекбоксы) → подтверждение → удаление → авто-refresh.
import { useCallback, useEffect, useMemo, useState } from 'react';
import { Events } from '@wailsio/runtime';
import ConnectionScreen from './components/ConnectionScreen';
import ConfirmDialog from './components/ConfirmDialog';
import FilesTable, { fileKey } from './components/FilesTable';
import StorageCard from './components/StorageCard';
import { plural, t, useI18n } from './i18n';
import { api } from './lib/api';
import type { FileMeta, PrinterStatus, Storage } from './lib/api';
import { formatBytes, formatDuration, formatGrams } from './lib/format';
import { modelName } from './lib/model';
import LangToggle from './components/LangToggle';

// Данные события files:progress (Go: app.EnrichProgress, без json-тегов).
type EnrichProgress = { Done: number; Total: number };

// Событие files:enriched добирает только время/граммы/превью — имя/размер
// из фазы 1 сохраняем.
const mergeEnriched = (f: FileMeta, fm: FileMeta): FileMeta => ({
  ...f,
  PrintTime: fm.PrintTime ?? f.PrintTime,
  Filament: fm.Filament ?? f.Filament,
  Thumb: fm.Thumb || f.Thumb,
});

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
  const [printOpen, setPrintOpen] = useState(false);
  const [starting, setStarting] = useState(false);
  const [toast, setToast] = useState<string | null>(null);
  // Фаза 2: null — обогащение не идёт, иначе {Done, Total} из событий.
  const [enrichProgress, setEnrichProgress] = useState<EnrichProgress | null>(null);
  // Таблица показывается только после files:done — к этому моменту у всех
  // файлов собраны время/граммы/превью. До неё — прогресс-полоска.
  const [tableReady, setTableReady] = useState(false);
  // Свёрнута ли карточка файлов (только шапка с количеством). Не
  // сохраняется между запусками — по умолчанию таблица открыта.
  const [filesCollapsed, setFilesCollapsed] = useState(false);

  const loading = !tableReady && !loadError;

  // Фаза 1: память и список файлов параллельно; ошибка — баннер с
  // «Повторить». До files:done таблица скрыта, прогресс-полоска — сразу.
  // После перезагрузки выбор обрезаем до существующих файлов.
  const load = useCallback(async () => {
    setLoadError(null);
    setTableReady(false);
    setFiles(null);
    setEnrichProgress({ Done: 0, Total: 0 }); // Total узнаем из фазы 1
    try {
      const [st, fl] = await Promise.all([api.storage(), api.listFiles()]);
      setStorage(st);
      setFiles(fl);
      setEnrichProgress({ Done: 0, Total: fl.length });
      setSelected((prev) => {
        const keys = new Set(fl.map(fileKey));
        return new Set([...prev].filter((k) => keys.has(k)));
      });
    } catch (err: unknown) {
      setLoadError(err instanceof Error ? err.message : String(err));
      setEnrichProgress(null);
    }
  }, []);

  // Первичная загрузка + ⟳.
  useEffect(() => {
    load();
  }, [load]);

  // Фаза 2: события бэкенда — таблица заполняется по мере прихода
  // (время/граммы/превью), прогресс-полоска гаснет на files:done.
  useEffect(() => {
    const offProgress = Events.On('files:progress', (ev) => {
      const p = ev.data as EnrichProgress;
      setEnrichProgress({ Done: p.Done, Total: p.Total });
    });
    const offEnriched = Events.On('files:enriched', (ev) => {
      const fm = ev.data as FileMeta;
      setFiles((prev) =>
        prev ? prev.map((f) => (fileKey(f) === fileKey(fm) ? mergeEnriched(f, fm) : f)) : prev,
      );
    });
    const offDone = Events.On('files:done', () => {
      // Все файлы обработаны: показываем таблицу целиком.
      setTableReady(true);
      setEnrichProgress(null);
    });
    return () => {
      offProgress();
      offEnriched();
      offDone();
    };
  }, []);

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
  // Печатается ли выбранный файл (printFileName из состояния, по basename):
  // его нельзя удалить/запускать повторно.
  const printingName = status?.PrintFile ? status.PrintFile.split('/').pop() ?? '' : '';
  const selectedHasPrinting = selectedList.some((f) => f.Name === printingName);

  // Печать: ровно один выбранный файл.
  const printTarget = selectedList.length === 1 ? selectedList[0] : null;
  const printEstimate = printTarget
    ? [
        printTarget.PrintTime != null ? formatDuration(printTarget.PrintTime) : null,
        printTarget.Filament != null ? formatGrams(printTarget.Filament) : null,
      ]
        .filter(Boolean)
        .join(' · ')
    : '';

  const doStartPrint = async () => {
    if (!printTarget) {
      return;
    }
    setStarting(true);
    try {
      await api.startPrint(`${printTarget.Path}/${printTarget.Name}`);
      setPrintOpen(false);
      setToast(t('printStarted'));
      // load() не вызываем: статус «печатает» придёт через опрос состояния,
      // а перезагрузка списка заново запустила бы фазу обогащения.
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : String(err);
      setPrintOpen(false);
      setToast(`${t('printFailed')}${msg}`);
    } finally {
      setStarting(false);
    }
  };

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

  // «Ender-3 V3 KE · V1.1.0.15 · 192.168.3.166»; версия прошивки — если
  // принтер её сообщил (поле modelVersion в WS-снимке).
  const label = status?.IP
    ? `${modelName(status.Model)}${status.FirmwareVersion ? ` · ${status.FirmwareVersion}` : ''} · ${status.IP}`
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

      {/* Таблица показывается только после files:done; до неё — заметная
          панель-заглушка с прогрессом («Загружаем файлы… 23/58»).
          Total появляется, когда фаза 1 отдаст список. */}
      {!tableReady && !loadError ? (
        <div className="loading-panel card">
          <div
            className="loading-count"
            role="progressbar"
            aria-valuenow={enrichProgress?.Done ?? 0}
            aria-valuemin={0}
            aria-valuemax={enrichProgress?.Total ?? 0}
            aria-label={t('enriching')}
          >
            {t('enriching')}
            {enrichProgress && enrichProgress.Total > 0
              ? ` ${enrichProgress.Done}/${enrichProgress.Total}`
              : ''}
          </div>
          <div className="enrich-bar">
            {enrichProgress && enrichProgress.Total > 0 ? (
              <div
                className="enrich-fill"
                style={{ width: `${(enrichProgress.Done / enrichProgress.Total) * 100}%` }}
              />
            ) : null}
          </div>
        </div>
      ) : null}

      {tableReady && files !== null ? (
        <FilesTable
          files={files}
          loading={false}
          selected={selected}
          printingName={printingName}
          collapsed={filesCollapsed}
          onToggle={toggle}
          onToggleAll={toggleAll}
          onToggleCollapsed={() => setFilesCollapsed((v) => !v)}
        />
      ) : null}

      {tableReady && files !== null && !filesCollapsed ? (
        <div className="footer-bar">
          <span className="footer-info">
            {t('selected')}: {selectedList.length}{' '}
            {plural(selectedList.length, [t('fileOne'), t('fileFew'), t('fileMany')])}
            {selectedList.length > 0 ? ` · ${formatBytes(selectedBytes)}` : ''}
          </span>
          <button
            className="btn"
            disabled={selectedList.length !== 1 || printing || starting || deleting}
            title={printing ? t('printingBanner') : undefined}
            onClick={() => setPrintOpen(true)}
          >
            {t('printSelected')}
          </button>
          <button
            className="btn btn-danger"
            disabled={
              selectedList.length === 0 || printing || selectedHasPrinting || deleting
            }
            title={
              printing
                ? t('printingBanner')
                : selectedHasPrinting
                  ? t('deleteBlockedPrinting')
                  : undefined
            }
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
          busy={deleting}
          onCancel={() => setConfirmOpen(false)}
          onConfirm={doDelete}
        />
      ) : null}

      {printOpen && printTarget ? (
        <ConfirmDialog
          kind="print"
          names={[printTarget.Name]}
          bytes={printTarget.Size}
          estimate={printEstimate}
          busy={starting}
          onCancel={() => setPrintOpen(false)}
          onConfirm={doStartPrint}
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
