import type { ReactNode } from 'react';
import { AuthRequired, ErrorState, LoadingState } from '@shared/components/States';
import { isNotFoundError, isUnauthorized } from '@shared/query/http';
import { useUserSession } from '../data';

export function UserPageGate({ children }: { children: ReactNode }) {
  const session = useUserSession();
  if (session.isPending) return <LoadingState />;
  if (session.error) {
    if (isUnauthorized(session.error) || isNotFoundError(session.error)) return <AuthRequired station="user" />;
    return <ErrorState error={session.error} onRetry={() => void session.refetch()} />;
  }
  if (!session.data?.user) return <AuthRequired station="user" />;
  return <>{children}</>;
}
