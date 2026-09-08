import { useQuery, type QueryKey, type UseQueryResult } from '@tanstack/react-query';
import { coreKeys } from './queries';
import { getCatalogPage, listEndpointKeysPage, listEndpointsPage } from './pageApi';
import type { NumberedCatalogView, NumberedPage, PageWindow } from './pageTypes';
import type { CatalogSourceType, Endpoint, EndpointKey } from './types';

function hasPrefix(query: { queryKey: QueryKey } | undefined, prefix: QueryKey) {
  const key = query?.queryKey;
  return Boolean(
    key &&
    key.length >= prefix.length &&
    prefix.every((part, index) => Object.is(key[index], part)),
  );
}

function sameScopePlaceholder<T>(prefix: QueryKey) {
  return (
    previousData: T | undefined,
    previousQuery: { queryKey: QueryKey } | undefined,
  ): T | undefined =>
    previousData !== undefined && hasPrefix(previousQuery, prefix) ? previousData : undefined;
}

function pageWindowKey(window: PageWindow): readonly ['page', string, number] {
  return ['page', window.page, window.pageSize];
}

export function useNumberedEndpoints(
  accountId: string,
  window: PageWindow,
  enabled = true,
): UseQueryResult<NumberedPage<Endpoint>, Error> {
  const root = coreKeys.endpointsRoot(accountId);
  return useQuery<NumberedPage<Endpoint>, Error, NumberedPage<Endpoint>>({
    queryKey: [...root, ...pageWindowKey(window)],
    queryFn: ({ signal }) => listEndpointsPage(window, signal),
    enabled: enabled && Boolean(accountId),
    placeholderData: sameScopePlaceholder<NumberedPage<Endpoint>>(root),
  });
}

export function useNumberedEndpointKeys(
  accountId: string,
  endpointId: string | undefined,
  window: PageWindow,
  enabled = true,
): UseQueryResult<NumberedPage<EndpointKey>, Error> {
  const root = endpointId
    ? coreKeys.endpointKeysRoot(accountId, endpointId)
    : [...coreKeys.endpointsRoot(accountId), 'keys', 'none'];
  return useQuery<NumberedPage<EndpointKey>, Error, NumberedPage<EndpointKey>>({
    queryKey: [...root, ...pageWindowKey(window)],
    queryFn: ({ signal }) => {
      if (!endpointId) throw new Error('endpoint id is required');
      return listEndpointKeysPage(endpointId, window, signal);
    },
    enabled: enabled && Boolean(accountId && endpointId),
    placeholderData: endpointId ? sameScopePlaceholder<NumberedPage<EndpointKey>>(root) : undefined,
    refetchInterval: (query) =>
      !query.state.error &&
      query.state.data?.data.some((key) => key.browse?.discovery.state === 'checking')
        ? 1_000
        : false,
  });
}

export function useNumberedCatalog(
  accountId: string,
  endpointId: string | undefined,
  keyId: string | undefined,
  source: CatalogSourceType | undefined,
  window: PageWindow,
  enabled = true,
): UseQueryResult<NumberedCatalogView, Error> {
  const root =
    endpointId && keyId
      ? coreKeys.catalogRoot(accountId, endpointId, keyId)
      : [...coreKeys.endpointsRoot(accountId), 'catalog', 'none'];
  const sourceKey = source ?? '';
  const queryKey = [...root, 'page', sourceKey, window.page, window.pageSize] as const;
  return useQuery<NumberedCatalogView, Error, NumberedCatalogView>({
    queryKey,
    queryFn: ({ signal }) => {
      if (!endpointId || !keyId) throw new Error('endpoint and key ids are required');
      return getCatalogPage(endpointId, keyId, window, source, signal);
    },
    enabled: enabled && Boolean(accountId && endpointId && keyId),
    staleTime: 5_000,
    refetchInterval: (query) =>
      !query.state.error && query.state.data?.evidence.state === 'checking' ? 1_000 : false,
    placeholderData:
      endpointId && keyId
        ? sameScopePlaceholder<NumberedCatalogView>([...root, 'page', sourceKey])
        : undefined,
  });
}
