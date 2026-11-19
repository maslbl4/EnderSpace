// Роутинг состояний: нет подключения → ConnectionScreen, есть → основной экран.
// Основной экран — заглушка до Task 8 (память + таблица файлов).
import { useEffect, useState } from 'react';
import ConnectionScreen from './components/ConnectionScreen';
import { t } from './i18n';
import { api } from './lib/api';
import type { PrinterStatus } from './lib/api';
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

  // Снимок статуса + периодическая проверка: обрыв связи возвращает
  // на экран подключения.
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
          /* бэкенд занят — попробуем в следующий цикл */
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

  const label =
    status?.IP != null && status.IP !== ''
      ? `${modelName(status.Model)} · ${status.IP}`
      : '';

  return (
    <main className="container">
      <header className="brand">
        <h1 className="title">
          <span className="title-accent">{t('appTitle')}</span>
        </h1>
        <span className="badge">{status?.Printing ? t('printing') : t('connected')}</span>
      </header>
      {label ? <p className="subtitle">{label}</p> : null}
      <p className="subtitle">{t('mainSoon')}</p>
      <button className="btn" onClick={disconnect}>
        {t('disconnect')}
      </button>
    </main>
  );
}

export default App;
