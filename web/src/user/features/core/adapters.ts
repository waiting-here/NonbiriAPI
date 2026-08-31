import { ApiError } from '@shared/query/http';
import type { AccountLifecycleAdapter, HomeAdapters } from './types';

export class CapabilityUnavailableError extends ApiError {
  constructor() {
    super('capability_unavailable', 'The required server capability is not connected.', 503);
    this.name = 'CapabilityUnavailableError';
  }
}

export const productionHomeAdapters: HomeAdapters = Object.freeze({
  checkin: { state: 'unavailable' as const },
  games: { state: 'unavailable' as const },
  announcements: { state: 'unavailable' as const },
});

const unavailable = async (): Promise<never> => {
  throw new CapabilityUnavailableError();
};

/**
 * Export v4 and final account deletion are intentionally disabled until the
 * lifecycle integration is available. Keeping this adapter
 * in production makes the missing dependency explicit without issuing a
 * legacy request or manufacturing a successful result.
 */
export const disabledAccountLifecycleAdapter: AccountLifecycleAdapter = Object.freeze({
  capabilities: Object.freeze({ exportV4: false, deleteAccount: false }),
  beginElevation: unavailable,
  exportV4: unavailable,
  deleteAccount: unavailable,
  readAccountAuthority: unavailable,
});
