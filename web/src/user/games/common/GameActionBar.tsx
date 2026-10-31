import { useLayoutEffect, useRef, useState, useSyncExternalStore, type ReactNode } from 'react';
import { createPortal } from 'react-dom';
import { useTranslation } from 'react-i18next';

const mobileQuery = '(width < 40rem)';
function subscribeSize(changed: () => void) {
  const media = window.matchMedia?.(mobileQuery);
  media?.addEventListener('change', changed);
  return () => media?.removeEventListener('change', changed);
}

export function GameActionBar({ cost, children }: { cost: ReactNode; children: ReactNode }) {
  const { t } = useTranslation();
  const anchor = useRef<HTMLSpanElement>(null);
  const ref = useRef<HTMLDivElement>(null);
  const mobile = useSyncExternalStore(
    subscribeSize,
    () => window.matchMedia?.(mobileQuery).matches ?? false,
    () => false,
  );
  const [host, setHost] = useState<Element | null>(null);
  useLayoutEffect(() => {
    // Keep fixed controls outside frosted cards while retaining the game's palette.
    setHost(
      mobile ? (anchor.current?.closest('.game-page, .likes-game, .bidding-game') ?? null) : null,
    );
  }, [mobile]);
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
  }, [host]);
  const bar = (
    <div ref={ref} className="game-actionbar nb-actionbar">
      <span className="game-actionbar__cost">
        {t('user.games.presentation.cost')} <strong>{cost}</strong>
      </span>
      {children}
    </div>
  );
  return (
    <>
      <span ref={anchor} hidden />
      {host ? createPortal(bar, host) : bar}
    </>
  );
}
