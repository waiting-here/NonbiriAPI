import { useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';

export function GameConfigurationDetails({
  enabled,
  children,
}: {
  enabled: boolean;
  children: ReactNode;
}) {
  const { t } = useTranslation();
  // Initialize from the saved switch; later edits keep the user's disclosure choice.
  const [initiallyOpen] = useState(enabled);
  return (
    <details className="ops-disclosure" open={initiallyOpen}>
      <summary>{t('admin.games.controls.settings')}</summary>
      {children}
    </details>
  );
}
