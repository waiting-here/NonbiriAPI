import type { ReactNode } from 'react';
import { useTranslation } from 'react-i18next';

export function GameActionBar({ cost, children }: { cost: ReactNode; children: ReactNode }) {
  const { t } = useTranslation();
  return (
    <div className="game-actionbar nb-actionbar">
      <span className="game-actionbar__cost">
        {t('user.games.presentation.cost')} <strong>{cost}</strong>
      </span>
      {children}
    </div>
  );
}
