import { useSyncExternalStore } from 'react';
import { browserTimeZone } from '../time';

const listeners = new Set<() => void>();
let observedZone = browserTimeZone();
let polling: ReturnType<typeof setInterval> | undefined;

export function checkBrowserTimeZone() {
  const zone = browserTimeZone();
  if (zone === observedZone) return;
  observedZone = zone;
  listeners.forEach((listener) => listener());
}

function subscribe(listener: () => void) {
  listeners.add(listener);
  if (listeners.size === 1) {
    window.addEventListener('focus', checkBrowserTimeZone);
    window.addEventListener('pageshow', checkBrowserTimeZone);
    document.addEventListener('visibilitychange', checkBrowserTimeZone);
    polling = setInterval(checkBrowserTimeZone, 30_000);
    checkBrowserTimeZone();
  }
  return () => {
    listeners.delete(listener);
    if (listeners.size) return;
    window.removeEventListener('focus', checkBrowserTimeZone);
    window.removeEventListener('pageshow', checkBrowserTimeZone);
    document.removeEventListener('visibilitychange', checkBrowserTimeZone);
    clearInterval(polling);
  };
}

export function useBrowserTimeZone(): string | null {
  return useSyncExternalStore(subscribe, browserTimeZone, () => null);
}
