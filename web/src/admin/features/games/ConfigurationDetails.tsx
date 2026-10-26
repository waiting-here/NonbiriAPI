import type { ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { Fold } from '@shared/components/ui/Fold';

export function GameConfigurationDetails({
  enabled,
  children,
}: {
  enabled: boolean;
  children: ReactNode;
}) {
  const { t } = useTranslation();
  // Initialize from the saved switch; later edits keep the user's disclosure choice.
  return (
    <Fold title={t('admin.games.controls.settings')} defaultOpen={enabled}>
      {children}
    </Fold>
  );
}
