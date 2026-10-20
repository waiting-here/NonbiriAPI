export const KEY_STATES = [
  'available',
  'disabled',
  'suspended',
  'exhausted',
  'rate_limited',
  'pending',
  'expired',
  'ended',
  'all',
] as const;
export type KeyStateFilter = (typeof KEY_STATES)[number];
