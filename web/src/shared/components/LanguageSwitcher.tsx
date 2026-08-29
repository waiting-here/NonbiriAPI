import { useTranslation } from 'react-i18next';
import { LANGUAGE_STORAGE_KEY } from '@shared/i18n';

const LANGUAGES = [
  { code: 'zh', label: '中文' },
  { code: 'en', label: 'EN' },
] as const;

type LanguageCode = (typeof LANGUAGES)[number]['code'];

export function LanguageSwitcher() {
  const { t, i18n } = useTranslation();
  const current: LanguageCode = i18n.resolvedLanguage?.startsWith('zh') ? 'zh' : 'en';

  const select = (code: LanguageCode) => {
    try {
      window.localStorage.setItem(LANGUAGE_STORAGE_KEY, code);
    } catch {
      // Language switching remains usable when storage is blocked.
    }
    void i18n.changeLanguage(code);
  };

  return (
    <div className="lang-switcher" role="group" aria-label={t('shell.langLabel')}>
      {LANGUAGES.map(({ code, label }) => (
        <button
          key={code}
          type="button"
          aria-pressed={current === code}
          aria-current={current === code ? 'true' : undefined}
          onClick={() => select(code)}
        >
          {label}
        </button>
      ))}
    </div>
  );
}
