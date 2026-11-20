import { useQuery, useQueryClient, type QueryClient } from '@tanstack/react-query';
import {
  captureStationSession,
  clearStationSession,
  stationSessionMatches,
  StationSessionChangedError,
} from '@shared/charityManagement';
import { charityKeys, type CharityRole } from '@shared/operations/charity';
import type { PagePager } from '@shared/operations/usePagePager';
import { isUnauthorized } from '@shared/query/http';

export function sessionAccount(role: CharityRole, value: unknown): string | undefined {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) return undefined;
  const root = value as Record<string, unknown>;
  if (role === 'admin') {
    const admin = root.admin;
    if (admin === null || typeof admin !== 'object' || Array.isArray(admin)) return undefined;
    const username = (admin as Record<string, unknown>).username;
    return typeof username === 'string' && username.length > 0 ? username : undefined;
  }
  const user = root.user;
  if (user === null || typeof user !== 'object' || Array.isArray(user)) return undefined;
  const userRoot = user as Record<string, unknown>;
  const id = userRoot.id;
  const level = userRoot.effective_level;
  return typeof id === 'string' && id.length > 0 && (level === 5 || level === 6) ? id : undefined;
}

async function guardedRead<T>(
  client: QueryClient,
  role: CharityRole,
  account: string | undefined,
  request: () => Promise<T>,
): Promise<T> {
  const frame = role === 'admin' ? 'admin' : 'steward';
  const snapshot = captureStationSession(client, frame);
  if (
    !account ||
    sessionAccount(
      role,
      client.getQueryData(role === 'admin' ? ['admin', 'session'] : ['user', 'session']),
    ) !== account
  ) {
    throw new StationSessionChangedError();
  }
  try {
    const result = await request();
    if (!stationSessionMatches(client, frame, snapshot)) throw new StationSessionChangedError();
    return result;
  } catch (error) {
    if (!stationSessionMatches(client, frame, snapshot)) throw new StationSessionChangedError();
    if (isUnauthorized(error)) clearStationSession(client, frame);
    throw error;
  }
}

function pagePlaceholder<T>(
  previous: T | undefined,
  previousQuery: { queryKey: readonly unknown[] } | undefined,
  identity: readonly unknown[],
  page: string,
  pageSize: number,
): T | undefined {
  if (!previous || !previousQuery) return undefined;
  const previousKey = previousQuery.queryKey;
  if (
    previousKey.length !== identity.length + 2 ||
    previousKey[identity.length] === page ||
    previousKey[identity.length + 1] !== pageSize
  ) {
    return undefined;
  }
  for (let index = 0; index < identity.length; index += 1) {
    if (previousKey[index] !== identity[index]) return undefined;
  }
  return previous;
}

interface BindingScope {
  role: CharityRole;
  account: string | undefined;
  modelId: string;
}

// Keep earlier pages only within the same account, model, filters and page size.
export function useBindingPage<T>(
  scope: BindingScope,
  filterKey: readonly string[],
  pager: PagePager,
  enabled: boolean,
  load: (signal: AbortSignal) => Promise<T>,
) {
  const client = useQueryClient();
  const identity = [
    ...charityKeys.root(scope.role),
    'binding-picker',
    scope.account ?? 'no-account',
    scope.modelId,
    ...filterKey,
  ];
  return useQuery<T>({
    queryKey: [...identity, pager.page, pager.pageSize],
    queryFn: ({ signal }) => guardedRead(client, scope.role, scope.account, () => load(signal)),
    enabled,
    placeholderData: (previous, query) =>
      pagePlaceholder(previous, query, identity, pager.page, pager.pageSize),
    retry: false,
  });
}
