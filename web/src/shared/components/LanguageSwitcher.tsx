import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { LANGUAGE_STORAGE_KEY, prepareLanguage, type SupportedLanguage } from '@shared/i18n';

const LANGUAGES = [
  { code: 'zh', label: '中文' },
  { code: 'en', label: 'EN' },
] as const;

export function LanguageSwitcher({
  save,
}: {
  save?: (code: SupportedLanguage) => Promise<boolean>;
}) {
  const { t, i18n } = useTranslation();
  const current = i18n.resolvedLanguage?.startsWith('zh') ? 'zh' : 'en';
  const [busy, setBusy] = useState(false);
  const [failed, setFailed] = useState(false);

  async function select(code: SupportedLanguage) {
    if (busy || current === code) return;
    setBusy(true);
    setFailed(false);
    try {
      await prepareLanguage(i18n, code);
      if (save && !(await save(code))) return;
      await i18n.changeLanguage(code);
      try {
        window.localStorage.setItem(LANGUAGE_STORAGE_KEY, code);
      } catch {
        // Language selection also works when storage is blocked.
      }
    } catch {
      setFailed(true);
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <div
        className="lang-switcher"
        role="group"
        aria-label={t('shell.langLabel')}
        aria-busy={busy}
      >
        {LANGUAGES.map(({ code, label }) => (
          <button
            key={code}
            type="button"
            lang={code}
            aria-pressed={current === code}
            aria-current={current === code ? 'true' : undefined}
            disabled={busy}
            onClick={() => void select(code)}
          >
            {label}
          </button>
        ))}
      </div>
      {failed ? (
        <span className="field-error" role="alert">
          {t('shell.languageRetry')}
        </span>
      ) : null}
    </>
  );
}
