import { useCallback } from 'react';
import {
  createSearchParams,
  useLocation,
  useSearchParams,
  type SetURLSearchParams,
} from 'react-router';

interface PendingSearchUpdate {
  params: URLSearchParams;
  state: unknown;
}

const pending = new WeakMap<ReturnType<typeof useLocation>, PendingSearchUpdate>();

/** Compose successive control edits before a router transition has rendered. */
export function useSearchState(): readonly [URLSearchParams, SetURLSearchParams] {
  const location = useLocation();
  const [params, setParams] = useSearchParams();
  const update = useCallback<SetURLSearchParams>(
    (value, options) => {
      const pendingUpdate = pending.get(location);
      const previous = pendingUpdate?.params ?? params;
      const next = createSearchParams(
        typeof value === 'function' ? value(new URLSearchParams(previous)) : value,
      );
      const hasExplicitState =
        options !== undefined && Object.prototype.hasOwnProperty.call(options, 'state');
      if (next.toString() === previous.toString() && !hasExplicitState) return;
      const state = hasExplicitState
        ? options?.state
        : pendingUpdate
          ? pendingUpdate.state
          : location.state;
      pending.set(location, { params: next, state });
      setParams(next, { ...(options ?? {}), state });
    },
    [location, params, setParams],
  );
  return [params, update];
}
