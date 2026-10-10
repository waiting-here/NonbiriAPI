import { useEffect, useRef } from 'react';

// Each scene mounts its own clock; hiding or pausing retains its remaining time.
export function SceneClock({
  milliseconds,
  paused,
  advance,
}: {
  milliseconds: number;
  paused: boolean;
  advance(): void;
}) {
  const callback = useRef(advance),
    remainingTime = useRef(milliseconds);
  useEffect(() => {
    callback.current = advance;
  });
  useEffect(() => {
    if (milliseconds <= 0) return;
    let remaining = remainingTime.current,
      started = 0,
      timer = 0;
    const stop = () => {
      if (!timer) return;
      window.clearTimeout(timer);
      remaining = Math.max(0, remaining - (performance.now() - started));
      remainingTime.current = remaining;
      timer = 0;
    };
    const resume = () => {
      stop();
      if (paused || document.hidden) return;
      started = performance.now();
      timer = window.setTimeout(() => {
        timer = 0;
        callback.current();
      }, remaining);
    };
    resume();
    document.addEventListener('visibilitychange', resume);
    return () => {
      stop();
      document.removeEventListener('visibilitychange', resume);
    };
  }, [milliseconds, paused]);
  return null;
}
