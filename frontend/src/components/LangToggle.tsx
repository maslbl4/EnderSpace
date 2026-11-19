// Переключатель языка (RU/EN): подпись — текущий язык.
import { getLang, setLang, t, useI18n } from '../i18n';

export default function LangToggle() {
  useI18n(); // перерисовка при смене языка
  return (
    <button
      className="icon-btn lang"
      title={t('langTitle')}
      aria-label={t('langTitle')}
      onClick={() => setLang(getLang() === 'ru' ? 'en' : 'ru')}
    >
      {t('langButton')}
    </button>
  );
}
