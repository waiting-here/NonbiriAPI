import { createContext } from 'react';
import {
  isForbidden,
  isMfaRequired,
  isStewardPermissionDenied,
  isUnauthorized,
} from '@shared/query/http';

export const LogReadError = createContext<(error: unknown) => void>(() => {});

export function logAccessLost(error: unknown): boolean {
  return (
    isUnauthorized(error) ||
    isForbidden(error) ||
    isStewardPermissionDenied(error) ||
    isMfaRequired(error)
  );
}
