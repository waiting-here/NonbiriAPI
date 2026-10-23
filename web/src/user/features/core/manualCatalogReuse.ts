import { ApiError } from '@shared/query/http';
import { createManualEntries } from './api';
import { getCatalogPage } from './pageApi';
import type { OperationIdentity } from './types';

type ManualInput = { upstream_model_id: string; provider: string };

async function missingManualEntries(
  endpointId: string,
  keyId: string,
  entries: readonly ManualInput[],
  signal?: AbortSignal,
): Promise<ManualInput[]> {
  const missing = new Set(entries.map((entry) => entry.upstream_model_id));
  let page = 1n;
  while (missing.size) {
    const catalog = await getCatalogPage(
      endpointId,
      keyId,
      { page: page.toString(), pageSize: 100 },
      'manual',
      signal,
    );
    for (const entry of catalog.manual_entries) missing.delete(entry.upstream_model_id);
    if (BigInt(catalog.pagination.page) >= BigInt(catalog.pagination.total_pages)) break;
    page = BigInt(catalog.pagination.page) + 1n;
  }
  return entries.filter((entry) => missing.has(entry.upstream_model_id));
}

export async function createOrReuseManualEntries(
  endpointId: string,
  keyId: string,
  entries: readonly ManualInput[],
  identity: OperationIdentity,
  signal?: AbortSignal,
): Promise<void> {
  const missing = await missingManualEntries(endpointId, keyId, entries, signal);
  if (!missing.length) return;
  try {
    await createManualEntries(endpointId, keyId, missing, identity, signal);
  } catch (error) {
    if (!(error instanceof ApiError) || error.code !== 'conflict' || error.status !== 409)
      throw error;
    let remaining: ManualInput[];
    try {
      remaining = await missingManualEntries(endpointId, keyId, missing, signal);
    } catch {
      throw error;
    }
    if (remaining.length) throw error;
  }
}
