import { ApiError } from '@shared/query/http';
import {
  beginElevation,
  deleteCurrentAccount,
  exportAccountV4,
  getHomeAnnouncements,
  getHomeCheckinStatus,
  getHomeGameSummary,
  readAccountAuthority,
  submitHomeCheckin,
} from './api';
import type { AccountLifecycleAdapter, HomeAdapters } from './types';

export class CapabilityUnavailableError extends ApiError {
  constructor() {
    super('capability_unavailable', 'The required server capability is not connected.', 503);
    this.name = 'CapabilityUnavailableError';
  }
}

export const productionHomeAdapters: HomeAdapters = Object.freeze({
  checkin: Object.freeze({
    state: 'available' as const,
    load: getHomeCheckinStatus,
    submit: submitHomeCheckin,
  }),
  games: Object.freeze({ state: 'available' as const, load: getHomeGameSummary }),
  announcements: Object.freeze({ state: 'available' as const, load: getHomeAnnouncements }),
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

export const productionAccountLifecycleAdapter = Object.freeze<AccountLifecycleAdapter>({
  capabilities: Object.freeze({ exportV4: true, deleteAccount: true }),
  beginElevation: async (_intent, accountId) => {
    if (!/^[1-9][0-9]*$/.test(accountId))
      throw new ApiError('invalid_request', 'Invalid account id.', 400);
    return beginElevation();
  },
  exportV4: ({ accountId, elevatedToken }) => exportAccountV4(accountId, elevatedToken),
  deleteAccount: ({ accountId, elevatedToken, confirmation }) =>
    deleteCurrentAccount(accountId, elevatedToken, confirmation),
  readAccountAuthority,
});
