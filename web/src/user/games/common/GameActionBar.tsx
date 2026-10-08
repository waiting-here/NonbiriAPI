import { useLayoutEffect, useRef, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';

export function GameActionBar({ cost, children }: { cost: ReactNode; children: ReactNode }) {
  const { t } = useTranslation();
  const ref = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    const bar = ref.current;
    if (!bar || typeof ResizeObserver === 'undefined') return;
    const root = bar.ownerDocument.documentElement;
    const measure = () =>
      root.style.setProperty('--game-actionbar-h', `${bar.getBoundingClientRect().height}px`);
    const observer = new ResizeObserver(measure);
    measure();
    observer.observe(bar);
    return () => {
      observer.disconnect();
      root.style.removeProperty('--game-actionbar-h');
    };
  }, []);
  return (
    <div ref={ref} className="game-actionbar nb-actionbar">
      <span className="game-actionbar__cost">
        {t('user.games.presentation.cost')} <strong>{cost}</strong>
      </span>
      {children}
    </div>
  );
}
