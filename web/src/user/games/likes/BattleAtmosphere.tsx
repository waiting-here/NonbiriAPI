import { useEffect, useState } from 'react';
import { createPortal } from 'react-dom';
import { useDuelText } from '../common/duel/copy';
import './atmosphere.css';

export function BattleAtmosphere({
  mode,
  reduced,
}: {
  readonly mode: 'accelerated' | 'danger' | null;
  readonly reduced: boolean;
}) {
  const t = useDuelText();
  const [hidden, setHidden] = useState(() => document.visibilityState !== 'visible');
  useEffect(() => {
    const visibility = () => setHidden(document.visibilityState !== 'visible');
    document.addEventListener('visibilitychange', visibility);
    return () => document.removeEventListener('visibilitychange', visibility);
  }, []);
  if (!mode) return null;
  return (
    <>
      <span className={`likes-atmosphere-status is-${mode}`} role="status">
        <span aria-hidden="true">{mode === 'danger' ? '⚠' : 'ϟ'}</span>
        {mode === 'danger'
          ? t('过载', 'OVERLOAD')
          : t('倍速模式 · 全力加速', 'SPEED MODE · FULL THROTTLE')}
      </span>
      {createPortal(
        <div
          className={`likes-atmosphere likes-atmosphere--${mode}`}
          data-paused={reduced || hidden}
          aria-hidden="true"
        >
          <div className="likes-atmosphere__flow" />
          <div className="likes-atmosphere__edge" />
          <div className="likes-atmosphere__signal" />
        </div>,
        document.body,
      )}
    </>
  );
}
