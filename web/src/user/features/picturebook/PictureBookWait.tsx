import { useEffect, useState, useSyncExternalStore } from 'react';
import { usePictureBookText } from '@shared/picturebook/copy';
import type { TaskStatus } from '@shared/picturebook/publicTypes';
import frameOne from '@shared/limitedactivities/picture-book-wait-1.webp';
import frameTwo from '@shared/limitedactivities/picture-book-wait-2.webp';
import frameThree from '@shared/limitedactivities/picture-book-wait-3.webp';
import frameFour from '@shared/limitedactivities/picture-book-wait-4.webp';
import './pictureBookWait.css';

const frames = [frameOne, frameTwo, frameThree, frameFour];
const motionQuery = '(prefers-reduced-motion: reduce)';

function subscribeMotion(changed: () => void) {
  const media = window.matchMedia?.(motionQuery);
  media?.addEventListener('change', changed);
  return () => media?.removeEventListener('change', changed);
}
function reducedMotion() {
  return window.matchMedia?.(motionQuery).matches ?? false;
}
function subscribeVisibility(changed: () => void) {
  document.addEventListener('visibilitychange', changed);
  return () => document.removeEventListener('visibilitychange', changed);
}
function visiblePage() {
  return document.visibilityState !== 'hidden';
}

export function PictureBookWait({ status }: { readonly status: TaskStatus }) {
  const t = usePictureBookText();
  const reduced = useSyncExternalStore(subscribeMotion, reducedMotion, () => true);
  const visible = useSyncExternalStore(subscribeVisibility, visiblePage, () => false);
  const [paused, setPaused] = useState(false);
  const [frame, setFrame] = useState(0);
  const [loaded, setLoaded] = useState(0);
  const [failed, setFailed] = useState(0);
  const active = status === 'dispatching' || status === 'running';
  const playing = active && visible && !reduced && !paused && loaded === 15 && failed === 0;
  useEffect(() => {
    if (!playing) return;
    const timer = window.setInterval(() => setFrame((value) => (value + 1) % frames.length), 2000);
    return () => window.clearInterval(timer);
  }, [playing]);
  if ((!active && status !== 'queued') || (failed & 1) !== 0) return null;
  const shown = reduced || !active || failed !== 0 ? 0 : frame;
  return (
    <div className="picturebook-wait">
      <div className="picturebook-wait__frames" aria-hidden="true">
        {frames.map((src, index) => (
          <img
            key={src}
            src={src}
            alt=""
            width="1280"
            height="720"
            className={index === shown ? 'picturebook-wait__frame--visible' : undefined}
            onLoad={() => setLoaded((value) => value | (1 << index))}
            onError={() => setFailed((value) => value | (1 << index))}
          />
        ))}
      </div>
      {active && !reduced && failed === 0 ? (
        <button
          type="button"
          className="nb-btn nb-btn--secondary"
          aria-pressed={paused}
          onClick={() => setPaused((value) => !value)}
        >
          {paused
            ? t('恢复等待动画', 'Resume waiting animation')
            : t('暂停等待动画', 'Pause waiting animation')}
        </button>
      ) : null}
    </div>
  );
}
