import { useCallback, useState } from 'react';

export interface CursorPager {
  page: number;
  cursor: string | null;
  reset: () => void;
  previous: () => void;
  next: (cursor: string) => void;
}

export function useCursorPager(): CursorPager {
  const [cursors, setCursors] = useState<(string | null)[]>([null]);
  const page = cursors.length;
  const reset = useCallback(() => setCursors([null]), []);
  const previous = useCallback(() => setCursors((current) => current.length > 1 ? current.slice(0, -1) : current), []);
  const next = useCallback((cursor: string) => {
    if (!cursor) return;
    setCursors((current) => [...current, cursor]);
  }, []);
  return { page, cursor: cursors[cursors.length - 1], reset, previous, next };
}
