import { QueryClient } from '@tanstack/react-query';
import { expect, it, vi } from 'vitest';
import { ApiError, apiFetch } from '@shared/query/http';
import { createGachaPort } from './api';
import { creditHistoryKeys } from '../../features/credits/queries';
import { gachaSnapshot } from '../../../../test/fixtures/gacha';

vi.mock('@shared/query/http', async (original) => ({
  ...(await original<typeof import('@shared/query/http')>()),
  apiFetch: vi.fn(),
}));
vi.mock('@shared/charityManagement', async (original) => ({
  ...(await original<typeof import('@shared/charityManagement')>()),
  stationSessionWrite: async (
    _client: QueryClient,
    _scope: string,
    request: () => Promise<unknown>,
  ) => request(),
}));

it.each(['bulk-permanent', 'bulk-limited', 'ticket-n', 'ticket-ssr-unlimited'])(
  'refreshes site wallets only for credit-funded redemption: %s',
  async (product) => {
    const client = new QueryClient();
    client.setQueryData(['user', 'session'], { user: { id: 'player' } });
    const historyKey = [...creditHistoryKeys.account('player'), { page: '2' }];
    const otherHistoryKey = [...creditHistoryKeys.account('other-player'), { page: '2' }];
    client.setQueryData(historyKey, { current_balance: '10000' });
    client.setQueryData(otherHistoryKey, { current_balance: '200' });
    vi.mocked(apiFetch).mockResolvedValue({ snapshot: gachaSnapshot() });
    await createGachaPort(client, 'player').act(
      { action: 'redeem', product, revision: 1 },
      'purchase-0123456789abcdef',
    );
    const creditPurchase = product.startsWith('bulk-');
    expect(client.getQueryState(['user', 'session'])?.isInvalidated).toBe(creditPurchase);
    expect(client.getQueryState(historyKey)?.isInvalidated).toBe(creditPurchase);
    expect(client.getQueryState(otherHistoryKey)?.isInvalidated).toBe(false);
    client.clear();
  },
);

it('expires the credit history after a lost response without repeating the purchase', async () => {
  const client = new QueryClient();
  client.setQueryData(['user', 'session'], { user: { id: 'player' } });
  const historyKey = [...creditHistoryKeys.account('player'), { page: '1' }];
  client.setQueryData(historyKey, {});
  const failure = new ApiError('network_error', 'Response lost', 0);
  vi.mocked(apiFetch).mockClear().mockRejectedValue(failure);
  await expect(
    createGachaPort(client, 'player').act(
      { action: 'redeem', product: 'bulk-permanent', revision: 1 },
      'purchase-0123456789abcdef',
    ),
  ).rejects.toBe(failure);
  expect(apiFetch).toHaveBeenCalledTimes(1);
  expect(client.getQueryState(historyKey)?.isInvalidated).toBe(true);
  client.clear();
});
