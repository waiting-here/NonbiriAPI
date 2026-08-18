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
