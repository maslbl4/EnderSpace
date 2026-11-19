// Таблица файлов: превью / имя / размер / время / пластик.
// «—» — значение не найдено (всегда в конце сортировки); сортировка по
// всем колонкам; скелетон во время загрузки; зум превью при наведении.
// Чекбоксы — функционал Task 9.
import { useMemo, useState } from 'react';
import { t } from '../i18n';
import type { FileMeta } from '../lib/api';
import { formatBytes, formatDuration, formatGrams } from '../lib/format';

type SortKey = 'name' | 'size' | 'time' | 'plastic';

// zoom — увеличенное превью: позиция = центр наведённой миниатюры.
type Zoom = { src: string; x: number; y: number };

export default function FilesTable({
  files,
  loading,
}: {
  files: FileMeta[];
  loading: boolean;
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

  const showZoom = (src: string, e: React.MouseEvent<HTMLImageElement>) => {
    const r = e.currentTarget.getBoundingClientRect();
    setZoom({ src, x: r.left + r.width / 2, y: r.top + r.height / 2 });
  };

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
                <th className="col-check" aria-hidden />
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
              {sorted.map((f) => (
                <tr key={f.Path + '/' + f.Name}>
                  <td className="col-check" />
                  <td className="col-thumb">
                    {f.Thumb ? (
                      <img
                        className="thumb"
                        src={f.Thumb}
                        alt=""
                        loading="lazy"
                        decoding="async"
                        onMouseEnter={(e) => showZoom(f.Thumb, e)}
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
              ))}
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
        style={{ left: zoom.x, top: zoom.y }}
        onMouseLeave={() => setZoom(null)}
      />
    ) : null}
    </>
  );
}
