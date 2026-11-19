// Карточка внутренней памяти: прогресс-бар + «Занято / Свободно / Всего».
// Free — всегда оценка («≈»): eMMC общая с прошивкой, df-эндпоинта нет.
import { t, useI18n } from '../i18n';
import type { Storage } from '../lib/api';
import { formatBytes } from '../lib/format';

export default function StorageCard({ storage }: { storage: Storage }) {
  useI18n(); // перерисовка при смене языка
  const pct =
    storage.Total > 0
      ? Math.min(100, Math.round((storage.Used / storage.Total) * 100))
      : 0;

  return (
    <section className="card storage-card">
      <h2 className="card-title">{t('storageTitle')}</h2>
      <div
        className="meter"
        role="progressbar"
        aria-valuenow={pct}
        aria-valuemin={0}
        aria-valuemax={100}
      >
        <div className="meter-fill" style={{ width: `${pct}%` }} />
      </div>
      <p className="storage-line">
        {t('storageUsed')} {formatBytes(storage.Used)} · {t('storageFree')} ≈{' '}
        {formatBytes(storage.Free)} · {t('storageTotal')} {formatBytes(storage.Total)}{' '}
        ({t('storageEmmc')})
      </p>
    </section>
  );
}
