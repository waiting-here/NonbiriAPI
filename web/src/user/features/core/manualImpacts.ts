import { useQuery } from '@tanstack/react-query';
import { ApiError } from '@shared/query/http';
import { listKeyBindingsPage } from './browseData';
import { coreKeys } from './queries';

export interface ManualImpact {
  bindingId: string;
  modelId: string;
  modelName: string;
}

export type ManualImpacts =
  | { state: 'complete'; impacts: ManualImpact[]; count: string }
  | { state: 'too_many'; impacts: []; count: string };

// Replacement writes already have a fixed 256-binding limit. Read only that
// exact pair on demand; never fetch every model owned by the account.
export async function readManualImpacts(
  endpointId: string,
  keyId: string,
  upstreamModel: string,
  signal?: AbortSignal,
): Promise<ManualImpacts> {
  const first = await listKeyBindingsPage(
    endpointId,
    keyId,
    { page: '1', pageSize: 100 },
    upstreamModel,
    signal,
  );
  const count = first.pagination.total_items;
  if (BigInt(count) > 256n) return { state: 'too_many', impacts: [], count };
  const bindings = [...first.data];
  for (let page = 2; page <= Number(first.pagination.total_pages); page += 1) {
    const next = await listKeyBindingsPage(
      endpointId,
      keyId,
      { page: String(page), pageSize: 100 },
      upstreamModel,
      signal,
    );
    if (next.pagination.total_items !== count || next.pagination.page !== String(page)) {
      throw new ApiError(
        'conflict',
        'The affected connections changed. Refresh before editing.',
        409,
      );
    }
    bindings.push(...next.data);
  }
  if (
    bindings.length !== Number(count) ||
    new Set(bindings.map((binding) => binding.id)).size !== bindings.length
  ) {
    throw new ApiError(
      'conflict',
      'The affected connections changed. Refresh before editing.',
      409,
    );
  }
  return {
    state: 'complete',
    count,
    impacts: bindings.map((binding) => ({
      bindingId: binding.id,
      modelId: binding.model_id,
      modelName: binding.model_full_name,
    })),
  };
}

export function useManualImpacts(
  accountId: string,
  endpointId: string,
  keyId: string,
  upstreamModel: string,
  pairRevision: string,
  enabled: boolean,
) {
  return useQuery({
    queryKey: [
      ...coreKeys.endpointRouting(accountId, endpointId, [keyId]),
      'manual-impact',
      upstreamModel,
      pairRevision,
    ],
    queryFn: ({ signal }) => readManualImpacts(endpointId, keyId, upstreamModel, signal),
    enabled: enabled && Boolean(accountId),
    staleTime: 0,
    retry: false,
  });
}
