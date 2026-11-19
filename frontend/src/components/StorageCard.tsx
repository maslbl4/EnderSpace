// Карточка внутренней памяти: прогресс-бар (модели + система) с тултипом
// при наведении, легенда с буллетами и раскладка «Свободно / Всего».
// Free и System — всегда оценки («≈»): принтер df не отдаёт, eMMC общая
// с прошивкой.
import { useState } from 'react';
import type { MouseEvent } from 'react';
import { t, useI18n } from '../i18n';
import type { Storage } from '../lib/api';
import { formatBytes } from '../lib/format';

// tip — содержимое тултипа над наведённым сегментом шкалы.
type Tip = { x: number; y: number; label: string; value: string; note?: string };

export default function StorageCard({ storage }: { storage: Storage }) {
  useI18n(); // перерисовка при смене языка
  const [tip, setTip] = useState<Tip | null>(null);

  const total = storage.Total > 0 ? storage.Total : 1;
  const filesPct = Math.min(100, Math.round((storage.Used / total) * 100));
  const systemPct = Math.min(100 - filesPct, Math.round((storage.System / total) * 100));

  // Тултип: по позиции курсора определяем сегмент шкалы.
  const onMeterMove = (e: MouseEvent<HTMLDivElement>) => {
    const r = e.currentTarget.getBoundingClientRect();
    const pct = ((e.clientX - r.left) / r.width) * 100;
    let tip: Tip;
    if (pct < filesPct) {
      tip = { x: e.clientX, y: e.clientY, label: t('legendModels'), value: formatBytes(storage.Used) };
    } else if (pct < filesPct + systemPct) {
      tip = {
        x: e.clientX,
        y: e.clientY,
        label: t('storageSystem'),
        value: `≈ ${formatBytes(storage.System)}`,
        note: t('storageSystemNote'),
      };
    } else {
      tip = { x: e.clientX, y: e.clientY, label: t('storageFree'), value: `≈ ${formatBytes(storage.Free)}` };
    }
    setTip(tip);
  };

  return (
    <section className="card storage-card">
      <h2 className="card-title">{t('storageTitle')}</h2>
      <div
        className="meter"
        role="progressbar"
        aria-valuenow={filesPct + systemPct}
        aria-valuemin={0}
        aria-valuemax={100}
        onMouseMove={onMeterMove}
        onMouseLeave={() => setTip(null)}
      >
        <div className="meter-fill" style={{ width: `${filesPct}%` }} />
        <div className="meter-fill meter-system" style={{ width: `${systemPct}%` }} />
      </div>
      <div className="meter-legend">
        <span className="legend-item">
          <span className="legend-dot legend-dot-files" aria-hidden />
          {t('legendModels')} {formatBytes(storage.Used)}
        </span>
        <span className="legend-item">
          <span className="legend-dot legend-dot-system" aria-hidden />
          {t('storageSystem')} ≈ {formatBytes(storage.System)} · {t('storageSystemNote')}
        </span>
      </div>
      <p className="storage-line">
        {t('storageFree')} ≈ {formatBytes(storage.Free)} · {t('storageTotal')}{' '}
        {formatBytes(storage.Total)} ({t('storageEmmc')})
      </p>
      {tip ? (
        <div className="meter-tip" style={{ left: tip.x + 14, top: tip.y + 16 }}>
          <span className="meter-tip-label">{tip.label}</span> {tip.value}
          {tip.note ? <span className="meter-tip-note"> · {tip.note}</span> : null}
        </div>
      ) : null}
    </section>
  );
}
