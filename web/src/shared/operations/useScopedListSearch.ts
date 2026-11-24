import { useState } from 'react';
import { useLocation } from 'react-router';
import { useSearchState } from './useSearchState';

/** Retain private list searches in the current account's history entry. */
export function useScopedListSearch(scope: string) {
  const location = useLocation();
  const [, setParams] = useSearchState();
  const saved = location.state?.listSearch;
  const applied =
    scope && saved?.scope === scope && typeof saved.value === 'string' && saved.value.length <= 160
      ? (saved.value as string)
      : '';
  const [edit, setEdit] = useState({ scope, applied, value: applied });
  const draft = edit.scope === scope && edit.applied === applied ? edit.value : applied;
  const setDraft = (value: string) => setEdit({ scope, applied, value });
  const apply = (value = draft, update?: (params: URLSearchParams) => void) => {
    const query = value.trim();
    const listSearch = scope && query ? { scope, value: query } : undefined;
    setEdit({ scope, applied: query, value: query });
    setParams(
      (params) => {
        update?.(params);
        params.set('page', '1');
        return params;
      },
      { state: { ...location.state, listSearch } },
    );
  };
  return {
    applied,
    draft,
    setDraft,
    apply,
    listSearch: applied ? { scope, value: applied } : undefined,
  };
}
