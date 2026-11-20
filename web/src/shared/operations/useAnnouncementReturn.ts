import { useEffect } from 'react';
import { useLocation } from 'react-router';

let lastAction: { entry: string; id: string; top: number } | undefined;

/** Restore the exact link or button once its list has rendered again. */
export function useAnnouncementReturn(ready: boolean) {
  const location = useLocation();
  const source = location.state?.announcementSource;
  useEffect(() => {
    const action = lastAction;
    if (!ready || !action) return;
    if (action.entry !== location.key && action.entry !== source) return;
    const target = document.getElementById(action.id);
    if (!target) return;
    const frame = requestAnimationFrame(() => {
      target.focus({ preventScroll: true });
      window.scrollTo({ top: action.top, behavior: 'instant' });
      lastAction = undefined;
    });
    return () => cancelAnimationFrame(frame);
  }, [location.key, ready, source]);

  return {
    announcementSource: location.key,
    remember: (target: HTMLElement) => {
      lastAction = { entry: location.key, id: target.id, top: window.scrollY };
    },
  };
}
