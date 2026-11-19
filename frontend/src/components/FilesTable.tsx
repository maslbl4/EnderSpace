// Таблица файлов: превью / имя / размер / время / пластик.
// «—» — значение не найдено; сортировка по имени и размеру;
// скелетон во время загрузки. Чекбоксы — функционал Task 9.
import { useMemo, useState } from 'react';
import { t } from '../i18n';
import type { FileMeta } from '../lib/api';
import { formatBytes, formatDuration, formatGrams } from '../lib/format';

type SortKey = 'name' | 'size';

export default function FilesTable({
  files,
  loading,
}: {
  files: FileMeta[];
  loading: boolean;
}) {
  const [sortKey, setSortKey] = useState<SortKey>('name');
  const [sortAsc, setSortAsc] = useState(true);

  const sorted = useMemo(() => {
    const arr = [...files];
    arr.sort((a, b) => {
      const r =
        sortKey === 'name' ? a.Name.localeCompare(b.Name, 'ru') : a.Size - b.Size;
      return sortAsc ? r : -r;
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

  return (
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
                <th className="num">{t('colTime')}</th>
                <th className="num">{t('colPlastic')}</th>
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
  );
}
