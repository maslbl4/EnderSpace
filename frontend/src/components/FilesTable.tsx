// Таблица файлов: чекбокс / превью / имя / размер / время / пластик.
// «—» — значение не найдено (всегда в конце сортировки); сортировка по
// всем колонкам; скелетон во время загрузки; зум превью при наведении.
import { useEffect, useMemo, useRef, useState } from 'react';
import type { MouseEvent as ReactMouseEvent } from 'react';
import { t, useI18n } from '../i18n';
import type { FileMeta } from '../lib/api';
import { formatBytes, formatDate, formatDuration, formatGrams } from '../lib/format';

// fileKey — идентичность файла для выбора (удаление работает по этим путям).
export const fileKey = (f: FileMeta) => f.Path + '/' + f.Name;

type SortKey = 'name' | 'size' | 'date' | 'time' | 'plastic';

// Ресайз колонок мышкой: перетаскиваются все, кроме чекбокса и превью.
// Ширины живут только в состоянии (между запусками не сохраняются).
// Минимумы — по ширине заголовка: текст «Загружен»/«Пластик» не обрывается.
const MIN_COL_WIDTH: Record<SortKey, number> = {
  name: 140,
  size: 80,
  date: 100,
  time: 90,
  plastic: 90,
};
// Фиксированные колонки (чекбокс 34px + превью 52px) для расчёта максимума.
const FIXED_COLS_WIDTH = 34 + 52;

// zoom — увеличенное превью (по центру окна, позиция в CSS).
type Zoom = { src: string };

export default function FilesTable({
  files,
  loading,
  selected,
  printingName,
  onToggle,
  onToggleAll,
}: {
  files: FileMeta[];
  loading: boolean;
  selected: ReadonlySet<string>;
  printingName?: string; // basename печатающегося файла ("" — нет)
  onToggle: (key: string) => void;
  onToggleAll: () => void;
}) {
  useI18n(); // перерисовка при смене языка
  // По умолчанию — по дате загрузки, новые сверху (по убыванию).
  const [sortKey, setSortKey] = useState<SortKey>('date');
  const [sortAsc, setSortAsc] = useState(false);
  const [zoom, setZoom] = useState<Zoom | null>(null);
  // Ручные ширины колонок (px); undefined — дефолт из CSS.
  const [colWidths, setColWidths] = useState<Partial<Record<SortKey, number>>>({});
  const thRefs = useRef<Partial<Record<SortKey, HTMLTableCellElement | null>>>({});

  // onResizeStart — drag правого края колонки: стартовая ширина берётся из
  // реально отрендеренного th (учитывает и дефолт CSS, и предшествующий
  // drag), дальше — mousemove по окну до mouseup. Защита от рывков:
  // целые пиксели (дробные ширины дёргают раскладку), пропуск обновлений
  // без изменения и потолок — колонка не может зажать соседние ниже их
  // текущей ширины (иначе таблица вылезла бы в горизонтальный скроллбар
  // посреди перетаскивания).
  const onResizeStart = (col: SortKey) => (e: ReactMouseEvent) => {
    e.preventDefault();
    const th = thRefs.current[col];
    if (!th) {
      return;
    }
    const startX = e.clientX;
    const startW = th.getBoundingClientRect().width;
    const lo = MIN_COL_WIDTH[col];
    const others = (Object.keys(MIN_COL_WIDTH) as SortKey[])
      .filter((c) => c !== col)
      .reduce((sum, c) => {
        const el = thRefs.current[c];
        const cur = el ? el.getBoundingClientRect().width : MIN_COL_WIDTH[c];
        return sum + Math.max(MIN_COL_WIDTH[c], cur);
      }, 0);
    const wrap = th.closest('.table-wrap');
    const hi = wrap ? Math.max(lo, wrap.clientWidth - others - FIXED_COLS_WIDTH) : Number.POSITIVE_INFINITY;
    let lastW = -1;
    let moved = false;
    const onMove = (ev: MouseEvent) => {
      const nw = Math.min(hi, Math.max(lo, Math.round(startW + (ev.clientX - startX))));
      if (nw === lastW) {
        return;
      }
      lastW = nw;
      moved = true;
      setColWidths((prev) => ({
        ...prev,
        [col]: nw,
      }));
    };
    const onUp = () => {
      window.removeEventListener('mousemove', onMove);
      window.removeEventListener('mouseup', onUp);
      document.body.style.cursor = '';
      document.body.style.userSelect = '';
      // Если колонку таскали, следующий click (обычно по th) не должен
      // переключить сортировку.
      if (moved) {
        const suppress = (ev: Event) => {
          ev.stopPropagation();
          ev.preventDefault();
        };
        window.addEventListener('click', suppress, { capture: true, once: true });
      }
    };
    window.addEventListener('mousemove', onMove);
    window.addEventListener('mouseup', onUp);
    document.body.style.cursor = 'col-resize';
    document.body.style.userSelect = 'none';
  };

  const sorted = useMemo(() => {
    const arr = [...files];
    arr.sort((a, b) => {
      if (sortKey === 'name') {
        const r = a.Name.localeCompare(b.Name, 'ru');
        return sortAsc ? r : -r;
      }
      const val = (f: FileMeta) =>
        sortKey === 'size'
          ? f.Size
          : sortKey === 'date'
            ? f.Epoch
            : sortKey === 'time'
              ? f.PrintTime
              : f.Filament;
      const va = val(a);
      const vb = val(b);
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

  // Зум-превью: показывается при наведении на миниатюру и пропадает, когда
  // курсор уходит и с миниатюры, и с превью (короткая задержка, чтобы успеть
  // «доехать» курсором до превью в центре окна). Escape тоже закрывает.
  const HIDE_DELAY = 200; // мс
  const hideTimer = useRef<number | null>(null);

  const cancelHide = () => {
    if (hideTimer.current !== null) {
      window.clearTimeout(hideTimer.current);
      hideTimer.current = null;
    }
  };

  const showZoom = (src: string) => {
    cancelHide();
    setZoom({ src });
  };

  const scheduleHide = () => {
    cancelHide();
    hideTimer.current = window.setTimeout(() => {
      hideTimer.current = null;
      setZoom(null);
    }, HIDE_DELAY);
  };

  // Таймер не должен пережить размонтирование.
  useEffect(() => cancelHide, []);

  // Escape закрывает превью.
  useEffect(() => {
    if (!zoom) {
      return;
    }
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        setZoom(null);
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [zoom]);

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
                <th
                  className="sortable"
                  ref={(el) => {
                    thRefs.current.name = el;
                  }}
                  style={colWidths.name != null ? { width: colWidths.name } : undefined}
                  onClick={() => toggleSort('name')}
                >
                  {t('colName')}
                  {arrow('name')}
                  <span
                    className="th-resize"
                    onMouseDown={onResizeStart('name')}
                    onClick={(e) => e.stopPropagation()}
                  />
                </th>
                <th
                  className="sortable num col-size"
                  ref={(el) => {
                    thRefs.current.size = el;
                  }}
                  style={colWidths.size != null ? { width: colWidths.size } : undefined}
                  onClick={() => toggleSort('size')}
                >
                  {t('colSize')}
                  {arrow('size')}
                  <span
                    className="th-resize"
                    onMouseDown={onResizeStart('size')}
                    onClick={(e) => e.stopPropagation()}
                  />
                </th>
                <th
                  className="sortable col-date"
                  ref={(el) => {
                    thRefs.current.date = el;
                  }}
                  style={colWidths.date != null ? { width: colWidths.date } : undefined}
                  onClick={() => toggleSort('date')}
                >
                  {t('colDate')}
                  {arrow('date')}
                  <span
                    className="th-resize"
                    onMouseDown={onResizeStart('date')}
                    onClick={(e) => e.stopPropagation()}
                  />
                </th>
                <th
                  className="sortable num col-time"
                  ref={(el) => {
                    thRefs.current.time = el;
                  }}
                  style={colWidths.time != null ? { width: colWidths.time } : undefined}
                  onClick={() => toggleSort('time')}
                >
                  {t('colTime')}
                  {arrow('time')}
                  <span
                    className="th-resize"
                    onMouseDown={onResizeStart('time')}
                    onClick={(e) => e.stopPropagation()}
                  />
                </th>
                <th
                  className="sortable num col-plastic"
                  ref={(el) => {
                    thRefs.current.plastic = el;
                  }}
                  style={colWidths.plastic != null ? { width: colWidths.plastic } : undefined}
                  onClick={() => toggleSort('plastic')}
                >
                  {t('colPlastic')}
                  {arrow('plastic')}
                  <span
                    className="th-resize"
                    onMouseDown={onResizeStart('plastic')}
                    onClick={(e) => e.stopPropagation()}
                  />
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
                          onMouseLeave={scheduleHide}
                        />
                      ) : (
                        <span className="thumb thumb-none" aria-hidden />
                      )}
                    </td>
                    <td className="name" title={f.Name}>
                      {f.Name}
                      {printingName === f.Name ? (
                        <span className="chip-printing">{t('rowPrinting')}</span>
                      ) : null}
                    </td>
                    <td className="num">{formatBytes(f.Size)}</td>
                    <td className="date">{formatDate(f.Epoch)}</td>
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
        onMouseEnter={cancelHide}
        onMouseLeave={scheduleHide}
      />
    ) : null}
    </>
  );
}
