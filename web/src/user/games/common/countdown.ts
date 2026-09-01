import { useEffect, useRef, useState } from 'react';

interface CountdownClock {
  readonly identity: string;
  readonly remaining: number;
}

/**
 * Projects a server timestamp into a local display countdown. The projection
 * can trigger a re-read, but it never decides the authoritative terminal state.
 */
export function useAuthoritativeCountdown(
  identity: string,
  deadline: number | null,
  serverNow: number,
  onElapsed: () => void,
): number | null {
  const initial = deadline === null ? null : Math.max(0, deadline - serverNow);
  const [clock, setClock] = useState<CountdownClock | null>(null);
  const elapsedIdentity = useRef<string | null>(null);
  const remaining = clock?.identity === identity ? clock.remaining : initial;

  useEffect(() => {
    if (deadline === null) return undefined;
    const baseline = Math.max(0, deadline - serverNow);
    const receivedAt = Date.now();
    const timer = window.setInterval(() => {
      const next = Math.max(0, baseline - Math.floor((Date.now() - receivedAt) / 1_000));
      setClock((current) =>
        current?.identity === identity && current.remaining === next
          ? current
          : { identity, remaining: next },
      );
      if (next === 0 && elapsedIdentity.current !== identity) {
        elapsedIdentity.current = identity;
        onElapsed();
      }
    }, 250);
    return () => window.clearInterval(timer);
  }, [deadline, identity, onElapsed, serverNow]);

  return remaining;
}
