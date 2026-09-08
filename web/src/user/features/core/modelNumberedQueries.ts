import { useQuery, type QueryKey, type UseQueryResult } from '@tanstack/react-query';
import { getBindingCandidatesPage, listModelsPage } from './pageApi';
import { coreKeys } from './queries';
import type { NumberedPage, PageWindow } from './pageTypes';
import type { BindingCandidate, CandidateFilters, Model } from './types';

type NumberedCandidateFilters = Omit<CandidateFilters, 'cursor' | 'limit'>;

function hasPrefix(query: { queryKey: QueryKey } | undefined, prefix: QueryKey): boolean {
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

function candidatePageKey(
  accountId: string,
  modelId: string,
  filters: NumberedCandidateFilters,
  window: PageWindow,
) {
  return [
    ...coreKeys.candidatesRoot(accountId, modelId),
    'numbered',
    filters.endpointId ?? '',
    filters.keyId ?? '',
    filters.source ?? '',
    filters.query ?? '',
    window.page,
    window.pageSize,
  ] as const;
}

export function useNumberedModels(
  accountId: string,
  window: PageWindow,
  enabled = true,
): UseQueryResult<NumberedPage<Model>, Error> {
  const root = coreKeys.modelsRoot(accountId);
  return useQuery<NumberedPage<Model>, Error, NumberedPage<Model>>({
    queryKey: [...root, 'numbered', ...pageWindowKey(window)],
    queryFn: ({ signal }) => listModelsPage(window, signal),
    enabled: enabled && Boolean(accountId),
    placeholderData: sameScopePlaceholder<NumberedPage<Model>>(root),
  });
}

export function useNumberedBindingCandidates(
  accountId: string,
  modelId: string | undefined,
  filters: NumberedCandidateFilters,
  window: PageWindow,
  enabled = true,
): UseQueryResult<NumberedPage<BindingCandidate>, Error> {
  const root = modelId
    ? coreKeys.candidatesRoot(accountId, modelId)
    : [...coreKeys.modelsRoot(accountId), 'binding-candidates', 'none'];
  return useQuery<NumberedPage<BindingCandidate>, Error, NumberedPage<BindingCandidate>>({
    queryKey: modelId
      ? candidatePageKey(accountId, modelId, filters, window)
      : [...root, 'numbered', window.page, window.pageSize],
    queryFn: ({ signal }) => {
      if (!modelId) throw new Error('model id is required');
      return getBindingCandidatesPage(modelId, filters, window, signal);
    },
    enabled: enabled && Boolean(accountId && modelId),
    staleTime: 5_000,
    placeholderData: modelId
      ? sameScopePlaceholder<NumberedPage<BindingCandidate>>(
          candidatePageKey(accountId, modelId, filters, window).slice(0, -2),
        )
      : undefined,
  });
}
