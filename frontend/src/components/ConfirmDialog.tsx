// Модальное подтверждение удаления: список файлов, сколько освободится,
// Отмена/Удалить; во время удаления — спиннер и блокировка кнопок.
import { plural, t, useI18n } from '../i18n';
import { formatBytes } from '../lib/format';

const MAX_LISTED = 8; // дальше — «…ещё N»

export default function ConfirmDialog({
  names,
  bytes,
  deleting,
  onCancel,
  onConfirm,
}: {
  names: string[];
  bytes: number;
  deleting: boolean;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  useI18n(); // перерисовка при смене языка
  const shown = names.slice(0, MAX_LISTED);
  const rest = names.length - shown.length;

  return (
    <div
      className="modal-backdrop"
      onMouseDown={() => {
        if (!deleting) {
          onCancel();
        }
      }}
    >
      <div
        className="modal"
        role="dialog"
        aria-modal="true"
        aria-label={t('deleteTitle')}
        onMouseDown={(e) => e.stopPropagation()}
      >
        <h3 className="modal-title">{t('deleteTitle')}</h3>
        <ul className="modal-list">
          {shown.map((n) => (
            <li key={n} title={n}>
              {n}
            </li>
          ))}
          {rest > 0 ? <li className="muted">{t('moreFiles').replace('%d', String(rest))}</li> : null}
        </ul>
        <p className="modal-note">
          {names.length}{' '}
          {plural(names.length, [t('fileOne'), t('fileFew'), t('fileMany')])} ·{' '}
          {t('deleteFree').replace('%s', formatBytes(bytes))}
        </p>
        <div className="modal-actions">
          <button className="btn btn-ghost" onClick={onCancel} disabled={deleting}>
            {t('deleteCancel')}
          </button>
          <button className="btn btn-danger" onClick={onConfirm} disabled={deleting}>
            {deleting ? (
              <>
                <span className="spinner" aria-hidden />
                {t('deleting')}
              </>
            ) : (
              t('deleteDelete')
            )}
          </button>
        </div>
      </div>
    </div>
  );
}
