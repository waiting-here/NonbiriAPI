import { useState } from 'react';
import type { QueryKey } from '@tanstack/react-query';
import {
  isForbidden,
  isMfaRequired,
  isNotFoundError,
  isStewardPermissionDenied,
  isUnauthorized,
} from './http';

/** Keep a read-only result through a transient replacement failure within its existing scope. */
export function useRetainedQueryData<T>(
  query: { data: T | undefined; error: unknown },
  scope: QueryKey,
  enabled = true,
): T | undefined {
  const key = JSON.stringify(scope);
  const discard =
    !enabled ||
    isUnauthorized(query.error) ||
    isForbidden(query.error) ||
    isStewardPermissionDenied(query.error) ||
    isMfaRequired(query.error) ||
    isNotFoundError(query.error);
  const data = discard ? undefined : query.data;
  const [last, setLast] = useState({ key, data });
  if (
    last.key !== key ||
    (data !== undefined && last.data !== data) ||
    (discard && last.data !== undefined)
  )
    setLast({ key, data });
  return data ?? (!discard && last.key === key ? last.data : undefined);
}
