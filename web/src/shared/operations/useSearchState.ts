import { useCallback } from 'react';
import {
  createSearchParams,
  useLocation,
  useSearchParams,
  type SetURLSearchParams,
} from 'react-router';

const pending = new WeakMap<ReturnType<typeof useLocation>, URLSearchParams>();

/** Compose successive control edits before a router transition has rendered. */
export function useSearchState(): readonly [URLSearchParams, SetURLSearchParams] {
  const location = useLocation();
  const [params, setParams] = useSearchParams();
  const update = useCallback<SetURLSearchParams>(
    (value, options) => {
      const previous = pending.get(location) ?? params;
      const next = createSearchParams(
        typeof value === 'function' ? value(new URLSearchParams(previous)) : value,
      );
      if (next.toString() === previous.toString() && options?.state === undefined) return;
      pending.set(location, next);
      setParams(next, options);
    },
    [location, params, setParams],
  );
  return [params, update];
}
