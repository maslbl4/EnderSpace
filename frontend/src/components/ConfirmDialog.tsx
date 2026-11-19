// Модальное подтверждение действия: удаление (список файлов + сколько
// освободится) или запуск печати (один файл + оценка). Во время операции —
// спиннер и блокировка кнопок.
import { plural, t, useI18n } from '../i18n';
import { formatBytes } from '../lib/format';

const MAX_LISTED = 8; // дальше — «…ещё N»

export default function ConfirmDialog({
  kind = 'delete',
  names,
  bytes,
  estimate,
  busy,
  onCancel,
  onConfirm,
}: {
  kind?: 'delete' | 'print';
  names: string[];
  bytes: number;
  estimate?: string; // только для kind="print": «4ч 30м · 210 г»
  busy: boolean;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  useI18n(); // перерисовка при смене языка
  const shown = names.slice(0, MAX_LISTED);
  const rest = names.length - shown.length;
  const isPrint = kind === 'print';

  return (
    <div
      className="modal-backdrop"
      onMouseDown={() => {
        if (!busy) {
          onCancel();
        }
      }}
    >
      <div
        className="modal"
        role="dialog"
        aria-modal="true"
        aria-label={isPrint ? t('printTitle') : t('deleteTitle')}
        onMouseDown={(e) => e.stopPropagation()}
      >
        <h3 className="modal-title">{isPrint ? t('printTitle') : t('deleteTitle')}</h3>
        <ul className="modal-list">
          {shown.map((n) => (
            <li key={n} title={n}>
              {n}
            </li>
          ))}
          {rest > 0 ? <li className="muted">{t('moreFiles').replace('%d', String(rest))}</li> : null}
        </ul>
        <p className="modal-note">
          {isPrint
            ? estimate
              ? t('printEstimate').replace('%s', estimate)
              : t('printNoEstimate')
            : `${names.length} ${plural(names.length, [
                t('fileOne'),
                t('fileFew'),
                t('fileMany'),
              ])} · ${t('deleteFree').replace('%s', formatBytes(bytes))}`}
        </p>
        <div className="modal-actions">
          <button className="btn btn-ghost" onClick={onCancel} disabled={busy}>
            {t('deleteCancel')}
          </button>
          <button
            className={isPrint ? 'btn' : 'btn btn-danger'}
            onClick={onConfirm}
            disabled={busy}
          >
            {busy ? (
              <>
                <span className="spinner" aria-hidden />
                {isPrint ? t('starting') : t('deleting')}
              </>
            ) : isPrint ? (
              t('printStart')
            ) : (
              t('deleteDelete')
            )}
          </button>
        </div>
      </div>
    </div>
  );
}
