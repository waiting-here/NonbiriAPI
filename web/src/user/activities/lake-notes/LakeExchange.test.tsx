import { screen } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../test/unit/support';
import { ApiError } from '@shared/query/http';
import { LakeExchange } from './LakeNotesPage';
import { useLakeCopy } from './copy';
import { initialProfile, RULES_ID } from './rules';
import type { ProfileView, Quote } from './api';

const preview = vi.hoisted(() => vi.fn());
afterEach(() => preview.mockReset());
vi.mock('./api', async (original) => ({
  ...(await original<typeof import('./api')>()),
  getQuote: preview,
}));
vi.mock('../../features/economy/queries', () => ({
  economySessionRequest: (_client: unknown, request: () => unknown) => request(),
}));
vi.mock('@shared/operations/useRetainedOperation', () => ({
  useRetainedOperation: () => ({ isPending: false, outcome: 'idle', error: null, reset: vi.fn() }),
}));

function fixture(): ProfileView {
  return {
    readonly: false,
    revision: '1',
    rules_id: RULES_ID,
    profile: { ...initialProfile(), coins: '5' },
    wallet: { general_milli: '1000', game_milli: '2000' },
    cast: null,
    settings: {
      enabled: true,
      revision: '1',
      exchanges: {
        coins_to_general: {
          enabled: true,
          source_amount: '10',
          target_amount: '1000',
          daily_source_limit: '100',
        },
        general_to_coins: { enabled: false, source_amount: '', target_amount: '' },
        coins_to_game: { enabled: false, source_amount: '', target_amount: '' },
        game_to_coins: { enabled: false, source_amount: '', target_amount: '' },
      },
    },
  };
}
function Exchange() {
  const { t } = useLakeCopy();
  return <LakeExchange view={fixture()} account="one" text={t} />;
}

it('distinguishes real funds from the remaining daily exchange allowance', async () => {
  const view = fixture();
  preview.mockResolvedValueOnce({
    direction: 'coins_to_general',
    quantity: '1',
    source_amount: '10',
    target_amount: '1000',
    source_lot: '10',
    target_lot: '1000',
    settings_revision: '1',
    profile_revision: '1',
    coins: '5',
    wallet: view.wallet,
    daily_source_remaining: '100',
    daily_reset_at: 1800000000,
  } satisfies Quote);
  const rendered = await renderWithProviders(<Exchange />, { station: 'user' });
  expect(screen.getByText(/Available balance:/)).toHaveTextContent('5');
  await rendered.user.click(screen.getByRole('button', { name: 'Preview exchange' }));
  expect(await screen.findByText(/Remaining daily exchange allowance:/)).toHaveTextContent('100');
  expect(screen.getByText(/Available balance:/)).toHaveTextContent('5');
});

it('keeps the requested quantity after an insufficient-funds preview failure', async () => {
  preview.mockRejectedValueOnce(new ApiError('insufficient_credits', 'Insufficient funds', 403));
  const rendered = await renderWithProviders(<Exchange />, { station: 'user' });
  const quantity = screen.getByRole('textbox', { name: 'Number of batches' });
  await rendered.user.clear(quantity);
  await rendered.user.type(quantity, '3');
  await rendered.user.click(screen.getByRole('button', { name: 'Preview exchange' }));
  expect(await screen.findByRole('alert')).toHaveTextContent('not enough available balance');
  expect(quantity).toHaveValue('3');
});
