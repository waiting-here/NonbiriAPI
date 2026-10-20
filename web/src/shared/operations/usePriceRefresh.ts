import { useEffect, useRef } from 'react';

/** Refresh a server price snapshot at its next reported boundary. */
export function usePriceRefresh(
  serverNow: number | undefined,
  changes: readonly (number | null)[],
  receivedAt: number,
  refresh: () => Promise<unknown>,
  enabled = true,
) {
  const callback = useRef(refresh);
  useEffect(() => {
    callback.current = refresh;
  }, [refresh]);
  const next = changes.reduce<number | null>(
    (earliest, time) =>
      time === null ? earliest : earliest === null ? time : Math.min(earliest, time),
    null,
  );
  useEffect(() => {
    if (!enabled || serverNow === undefined || next === null) return;
    const alreadyElapsed = Math.max(0, Date.now() - receivedAt);
    const started = performance.now();
    let timer: ReturnType<typeof setTimeout>;
    const schedule = () => {
      const delay = (next - serverNow) * 1000 - alreadyElapsed - (performance.now() - started);
      timer = setTimeout(
        () => {
          if (delay > 2_147_483_647) schedule();
          else void callback.current().catch(() => undefined);
        },
        Math.max(0, Math.min(delay, 2_147_483_647)),
      );
    };
    schedule();
    return () => clearTimeout(timer);
  }, [enabled, next, receivedAt, serverNow]);
}
