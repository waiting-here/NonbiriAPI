import { ApiError } from './query/http';

export type CloudflareProxyMode = 'worker' | 'legacy' | 'direct';

export function normalizeCloudflareProxyMode(value: unknown): CloudflareProxyMode {
  if (value !== 'worker' && value !== 'legacy' && value !== 'direct')
    throw new ApiError('invalid_response', 'Invalid Cloudflare proxy mode.', 200);
  return value;
}
