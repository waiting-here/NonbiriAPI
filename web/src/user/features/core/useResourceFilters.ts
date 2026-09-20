import { useEffect, useLayoutEffect, useRef } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { useLocation } from 'react-router';
import { useSearchState } from '@shared/operations/useSearchState';
import { resourceNavigation } from '@shared/operations/resourceNavigation';
import {
  canonicalResourceFilters,
  resourceFilterFields,
  resourceFilterIdentity,
  type ResourceFilters,
  type ResourceListKind,
} from './resourceFilters';

export function useResourceFilters(
  kind: ResourceListKind,
  accountId: string,
  prefix = '',
  pageParam = 'page',
) {
  const client = useQueryClient();
  const location = useLocation();
  const [params, setParams] = useSearchState();
  const scope = `${resourceNavigation(client).epoch}:${accountId}`;
  const state = (location.state ?? {}) as Record<string, unknown>;
  const stale = state.resourceSession !== undefined && state.resourceSession !== scope;
  const raw: ResourceFilters = {};
  for (const field of resourceFilterFields[kind]) {
    const values = params.getAll(prefix + field);
    if (values.length === 1 && !stale) raw[field] = values[0];
  }
  let filters: ResourceFilters;
  try {
    filters = canonicalResourceFilters(kind, raw);
  } catch {
    filters = {};
  }
  const identity = resourceFilterIdentity(kind, filters);
  const canonical = new URLSearchParams(identity);
  const needsNormalization = resourceFilterFields[kind].some(
    (field) =>
      params.getAll(prefix + field).length > 1 ||
      (params.get(prefix + field) ?? '') !== (canonical.get(field) ?? ''),
  );
  useEffect(() => {
    if (
      !stale &&
      !needsNormalization &&
      (state.resourceSession === scope || (!identity && !params.has(pageParam)))
    )
      return;
    setParams(
      (previous) => {
        const next = new URLSearchParams(previous);
        for (const field of resourceFilterFields[kind]) {
          next.delete(prefix + field);
          const value = canonical.get(field);
          if (value) next.set(prefix + field, value);
        }
        if (stale) next.set(pageParam, '1');
        return next;
      },
      {
        replace: true,
        state: { ...state, ...(stale ? { returnTo: undefined } : {}), resourceSession: scope },
      },
    );
  });
  const update = (value: ResourceFilters | ((current: ResourceFilters) => ResourceFilters)) => {
    setParams(
      (previous) => {
        const current: ResourceFilters = {};
        for (const field of resourceFilterFields[kind]) {
          const values = previous.getAll(prefix + field);
          if (values.length === 1 && !stale) current[field] = values[0];
        }
        const nextFilters = canonicalResourceFilters(
          kind,
          typeof value === 'function' ? value(current) : value,
        );
        const next = new URLSearchParams(previous);
        for (const field of resourceFilterFields[kind]) {
          next.delete(prefix + field);
          if (nextFilters[field]) next.set(prefix + field, nextFilters[field]);
        }
        next.set(pageParam, '1');
        return next;
      },
      { state: { ...state, resourceSession: scope } },
    );
  };
  return {
    kind,
    accountId,
    scope,
    filters,
    identity,
    update,
    clear: () => update({}),
    active: identity !== '',
  };
}

export type ResourceFilterControl = ReturnType<typeof useResourceFilters>;

/** Scroll positions remain in this session's memory and are erased on account changes. */
export function useResourceListScroll(accountId: string, ready: boolean, visible = true) {
  const client = useQueryClient();
  const location = useLocation();
  const nav = resourceNavigation(client);
  const search = new URLSearchParams(location.search);
  search.sort();
  const key = `${nav.epoch}:${accountId}:${location.pathname}?${search.toString()}`;
  const restored = useRef('');
  useLayoutEffect(() => {
    if (!visible) return;
    let position = window.scrollY;
    const remember = () => {
      position = window.scrollY;
    };
    window.addEventListener('scroll', remember, { passive: true });
    return () => {
      window.removeEventListener('scroll', remember);
      nav.scroll.set(key, position);
    };
  }, [key, nav, visible]);
  useEffect(() => {
    if (!visible) {
      restored.current = '';
      return;
    }
    if (!ready || restored.current === key) return;
    const position = nav.scroll.get(key);
    if (position === undefined) return;
    const frame = requestAnimationFrame(() => {
      window.scrollTo({ top: position, behavior: 'instant' });
      restored.current = key;
    });
    return () => cancelAnimationFrame(frame);
  }, [key, nav, ready, visible]);
}
