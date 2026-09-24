import { ApiError } from '@shared/query/http';
import {
  beginElevation,
  deleteCurrentAccount,
  exportAccount,
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
  gameCheckin: Object.freeze({
    state: 'available' as const,
    load: (signal?: AbortSignal) => getHomeCheckinStatus(signal, 'game'),
    submit: (signal?: AbortSignal) => submitHomeCheckin(signal, 'game'),
  }),
  games: Object.freeze({ state: 'available' as const, load: getHomeGameSummary }),
  announcements: Object.freeze({ state: 'available' as const, load: getHomeAnnouncements }),
});

const unavailable = async (): Promise<never> => {
  throw new CapabilityUnavailableError();
};

/**
 * A caller without lifecycle integration can explicitly disable export and
 * deletion without issuing a request or manufacturing a successful result.
 */
export const disabledAccountLifecycleAdapter: AccountLifecycleAdapter = Object.freeze({
  capabilities: Object.freeze({ exportAccount: false, deleteAccount: false }),
  beginElevation: unavailable,
  exportAccount: unavailable,
  deleteAccount: unavailable,
  readAccountAuthority: unavailable,
});

export const productionAccountLifecycleAdapter = Object.freeze<AccountLifecycleAdapter>({
  capabilities: Object.freeze({ exportAccount: true, deleteAccount: true }),
  beginElevation: async (_intent, accountId) => {
    if (!/^[1-9][0-9]*$/.test(accountId))
      throw new ApiError('invalid_request', 'Invalid account id.', 400);
    return beginElevation();
  },
  exportAccount: ({ accountId, elevatedToken }) => exportAccount(accountId, elevatedToken),
  deleteAccount: ({ accountId, elevatedToken, confirmation }) =>
    deleteCurrentAccount(accountId, elevatedToken, confirmation),
  readAccountAuthority,
});
