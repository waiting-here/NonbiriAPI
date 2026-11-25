import { useTranslation } from 'react-i18next';
import type { Theme } from './context';
import { useTheme } from './useTheme';

// A compact dropdown replaces the old binary toggle so the user can pick
// light, dark, or follow-system. 'Follow system' is the first-time default
// (see ThemeProvider): nothing is persisted until the user changes it.
export function ThemeToggle() {
  const { t } = useTranslation();
  const { theme, setTheme } = useTheme();
  return (
    <select
      className="theme-select"
      value={theme}
      onChange={(event) => setTheme(event.target.value as Theme)}
      aria-label={t('shell.themeLabel')}
      title={t('shell.themeLabel')}
    >
      <option value="system">{t('shell.themeSystem')}</option>
      <option value="light">{t('shell.themeLight')}</option>
      <option value="dark">{t('shell.themeDark')}</option>
    </select>
  );
}

export function DisplayPreferences() {
  const { t } = useTranslation();
  const { density, setDensity, fontSize, setFontSize } = useTheme();
  return (
    <>
      <label>
        {t('common.deviceDisplay.density')}
        <select
          value={density}
          onChange={(event) =>
            setDensity(event.target.value === 'compact' ? 'compact' : 'comfortable')
          }
        >
          <option value="comfortable">{t('common.deviceDisplay.comfortable')}</option>
          <option value="compact">{t('common.deviceDisplay.compact')}</option>
        </select>
      </label>
      <label>
        {t('common.deviceDisplay.font')}
        <select
          value={fontSize}
          onChange={(event) => setFontSize(event.target.value === 'large' ? 'large' : 'default')}
        >
          <option value="default">{t('common.deviceDisplay.standard')}</option>
          <option value="large">{t('common.deviceDisplay.large')}</option>
        </select>
      </label>
    </>
  );
}
