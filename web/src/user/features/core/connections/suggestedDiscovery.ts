import { ApiError } from '@shared/query/http';
import { connectionAPI } from './api';
import type { DiscoverySource, DiscoverySourceInput } from './types';

export async function resolveSuggestedDiscovery(
  connectionId: string,
  input: DiscoverySourceInput,
  credentialVersion: string,
  key: string,
  signal: AbortSignal,
): Promise<DiscoverySource> {
  const changed = () => new ApiError('conflict', 'The credential changed.', 409);
  const credential = await connectionAPI.credential(input.credential_id, signal);
  if (!credential.enabled || credential.credential_version !== credentialVersion) throw changed();
  const matches = (source: DiscoverySource) =>
    source.enabled &&
    source.credential_available !== false &&
    source.credential_id === input.credential_id &&
    source.credential_version === credentialVersion &&
    source.base_url === input.base_url &&
    source.list_format === input.list_format;
  for (let page = 1n; ; page++) {
    const sources = await connectionAPI.sources(
      connectionId,
      { page: page.toString(), pageSize: 100 },
      input.base_url,
      signal,
    );
    const source = sources.data.find(matches);
    if (source) return source;
    if (page >= BigInt(sources.pagination.total_pages)) break;
  }
  const source = await connectionAPI.createSource(connectionId, input, key, signal);
  if (!matches(source)) throw changed();
  return source;
}
