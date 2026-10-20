import { useEffect, useRef } from 'react';

/** Re-read the current page at its earliest cooldown deadline; eligibility stays server-owned. */
export function useRateLimitRefresh(
  deadlines: readonly (number | null | undefined)[],
  refresh: () => Promise<unknown>,
) {
  const callback = useRef(refresh);
  useEffect(() => {
    callback.current = refresh;
  }, [refresh]);
  const next = deadlines.reduce<number | null>(
    (earliest, value) =>
      value == null ? earliest : earliest === null ? value : Math.min(earliest, value),
    null,
  );
  useEffect(() => {
    if (next === null) return;
    let timer: ReturnType<typeof setTimeout>;
    const schedule = () => {
      const delay = next * 1000 - Date.now();
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
  }, [next]);
}
