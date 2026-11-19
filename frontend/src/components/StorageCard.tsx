// Карточка внутренней памяти: прогресс-бар (файлы + система) и раскладка
// «Занято / Свободно / Всего» + отдельная строка «Система».
// Free и System — всегда оценки («≈»): принтер df не отдаёт, eMMC общая
// с прошивкой.
import { t, useI18n } from '../i18n';
import type { Storage } from '../lib/api';
import { formatBytes } from '../lib/format';

export default function StorageCard({ storage }: { storage: Storage }) {
  useI18n(); // перерисовка при смене языка
  const total = storage.Total > 0 ? storage.Total : 1;
  const filesPct = Math.min(100, Math.round((storage.Used / total) * 100));
  const systemPct = Math.min(100 - filesPct, Math.round((storage.System / total) * 100));

  return (
    <section className="card storage-card">
      <h2 className="card-title">{t('storageTitle')}</h2>
      <div
        className="meter"
        role="progressbar"
        aria-valuenow={filesPct + systemPct}
        aria-valuemin={0}
        aria-valuemax={100}
      >
        <div className="meter-fill" style={{ width: `${filesPct}%` }} />
        <div className="meter-fill meter-system" style={{ width: `${systemPct}%` }} />
      </div>
      <p className="storage-line">
        {t('storageUsed')} {formatBytes(storage.Used)} · {t('storageFree')} ≈{' '}
        {formatBytes(storage.Free)} · {t('storageTotal')} {formatBytes(storage.Total)}{' '}
        ({t('storageEmmc')})
      </p>
      <p className="storage-line">
        {t('storageSystem')} ≈ {formatBytes(storage.System)} · {t('storageSystemNote')}
      </p>
    </section>
  );
}
