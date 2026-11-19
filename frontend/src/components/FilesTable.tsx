// Таблица файлов: чекбокс / превью / имя / размер / время / пластик.
// «—» — значение не найдено (всегда в конце сортировки); сортировка по
// всем колонкам; скелетон во время загрузки; зум превью при наведении.
import { useMemo, useState } from 'react';
import { t } from '../i18n';
import type { FileMeta } from '../lib/api';
import { formatBytes, formatDuration, formatGrams } from '../lib/format';

// fileKey — идентичность файла для выбора (удаление работает по этим путям).
export const fileKey = (f: FileMeta) => f.Path + '/' + f.Name;

type SortKey = 'name' | 'size' | 'time' | 'plastic';

// zoom — увеличенное превью (по центру окна, позиция в CSS).
type Zoom = { src: string };

export default function FilesTable({
  files,
  loading,
  selected,
  onToggle,
  onToggleAll,
}: {
  files: FileMeta[];
  loading: boolean;
  selected: ReadonlySet<string>;
  onToggle: (key: string) => void;
  onToggleAll: () => void;
}) {
  const [sortKey, setSortKey] = useState<SortKey>('name');
  const [sortAsc, setSortAsc] = useState(true);
  const [zoom, setZoom] = useState<Zoom | null>(null);

  const sorted = useMemo(() => {
    const arr = [...files];
    arr.sort((a, b) => {
      if (sortKey === 'name') {
        const r = a.Name.localeCompare(b.Name, 'ru');
        return sortAsc ? r : -r;
      }
      const va =
        sortKey === 'size' ? a.Size : sortKey === 'time' ? a.PrintTime : a.Filament;
      const vb =
        sortKey === 'size' ? b.Size : sortKey === 'time' ? b.PrintTime : b.Filament;
      // «—» (null) — всегда внизу, независимо от направления.
      if (va == null && vb == null) return 0;
      if (va == null) return 1;
      if (vb == null) return -1;
      return sortAsc ? va - vb : vb - va;
    });
    return arr;
  }, [files, sortKey, sortAsc]);

  const toggleSort = (key: SortKey) => {
    if (key === sortKey) {
      setSortAsc(!sortAsc);
    } else {
      setSortKey(key);
      setSortAsc(true);
    }
  };

  const arrow = (key: SortKey) =>
    sortKey === key ? (sortAsc ? ' ↑' : ' ↓') : '';

  const showZoom = (src: string) => setZoom({ src, x: 0, y: 0 });

  const allChecked = files.length > 0 && selected.size === files.length;

  // Зум-элемент вне <section>: .card использует backdrop-filter, что делает
  // её containing block для position: fixed (координаты бы «поехали»).
  return (
    <>
    <section className="card files-card">
      <h2 className="card-title">
        {t('filesTitle')}
        {!loading ? ` (${files.length})` : ''}
      </h2>
      {loading ? (
        <div className="skeleton" aria-hidden>
          {Array.from({ length: 6 }, (_, i) => (
            <div className="skeleton-row" key={i} />
          ))}
        </div>
      ) : files.length === 0 ? (
        <p className="empty">{t('memoryEmpty')}</p>
      ) : (
        <div className="table-wrap">
          <table className="files-table">
            <thead>
              <tr>
                <th className="col-check">
                  <input
                    type="checkbox"
                    className="checkbox"
                    title={t('selectAll')}
                    aria-label={t('selectAll')}
                    checked={allChecked}
                    onChange={onToggleAll}
                  />
                </th>
                <th className="col-thumb" aria-hidden />
                <th className="sortable" onClick={() => toggleSort('name')}>
                  {t('colName')}
                  {arrow('name')}
                </th>
                <th className="sortable num" onClick={() => toggleSort('size')}>
                  {t('colSize')}
                  {arrow('size')}
                </th>
                <th className="sortable num" onClick={() => toggleSort('time')}>
                  {t('colTime')}
                  {arrow('time')}
                </th>
                <th className="sortable num" onClick={() => toggleSort('plastic')}>
                  {t('colPlastic')}
                  {arrow('plastic')}
                </th>
              </tr>
            </thead>
            <tbody>
              {sorted.map((f) => {
                const k = fileKey(f);
                return (
                  <tr key={k} className={selected.has(k) ? 'selected' : ''}>
                    <td className="col-check">
                      <input
                        type="checkbox"
                        className="checkbox"
                        checked={selected.has(k)}
                        onChange={() => onToggle(k)}
                      />
                    </td>
                    <td className="col-thumb">
                      {f.Thumb ? (
                        <img
                          className="thumb"
                          src={f.Thumb}
                          alt=""
                          loading="lazy"
                          decoding="async"
                          onMouseEnter={() => showZoom(f.Thumb)}
                        />
                      ) : (
                        <span className="thumb thumb-none" aria-hidden />
                      )}
                    </td>
                    <td className="name" title={f.Name}>
                      {f.Name}
                    </td>
                    <td className="num">{formatBytes(f.Size)}</td>
                    <td className="num">
                      {f.PrintTime != null ? formatDuration(f.PrintTime) : '—'}
                    </td>
                    <td className="num">
                      {f.Filament != null ? formatGrams(f.Filament) : '—'}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </section>
    {zoom ? (
      <img
        className="zoom-thumb"
        src={zoom.src}
        alt=""
        onMouseLeave={() => setZoom(null)}
      />
    ) : null}
    </>
  );
}
