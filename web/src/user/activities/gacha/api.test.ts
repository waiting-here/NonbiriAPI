import { QueryClient } from '@tanstack/react-query';
import { expect, it, vi } from 'vitest';
import { apiFetch } from '@shared/query/http';
import { createGachaPort } from './api';
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

it.each(['bulk-permanent', 'bulk-limited', 'ticket-n'])(
  'refreshes site wallets only for credit-funded redemption: %s',
  async (product) => {
    const client = new QueryClient();
    client.setQueryData(['user', 'session'], { user: { id: 'player' } });
    client.setQueryData(['user', 'credits'], { balance: '10000' });
    vi.mocked(apiFetch).mockResolvedValue({ snapshot: gachaSnapshot() });
    await createGachaPort(client, 'player').act(
      { action: 'redeem', product, revision: 1 },
      'purchase-0123456789abcdef',
    );
    const creditPurchase = product.startsWith('bulk-');
    expect(client.getQueryState(['user', 'session'])?.isInvalidated).toBe(creditPurchase);
    expect(client.getQueryState(['user', 'credits'])?.isInvalidated).toBe(creditPurchase);
    client.clear();
  },
);
