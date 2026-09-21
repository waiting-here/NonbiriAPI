import { act, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import {
  beginManagementSessionRequest,
  noteManagementSessionSuccess,
} from '@shared/charityManagement';
import { renderWithProviders } from '../../../../test/unit/support';
import { loanQuote, loanReceipt } from '../../../../test/fixtures/loans';
import { LoanCard } from './LoanCard';

const view = {
  enabled: true,
  available: true,
  reason: 'available' as const,
  tiers: ['10000', '100000', '1000000'] as [string, string, string],
};
function reply(value: unknown, status = 200) {
  return new Response(JSON.stringify(value), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}
async function renderCard() {
  const rendered = await renderWithProviders(<LoanCard account="1" loan={view} masterAvailable />, {
    station: 'user',
    role: 'user',
  });
  const session = { user: { id: '1', username: 'fixture-user', effective_level: 1 } };
  const generation = beginManagementSessionRequest(rendered.queryClient, 'steward');
  noteManagementSessionSuccess(rendered.queryClient, 'steward', session, generation);
  rendered.queryClient.setQueryData(['user', 'session'], session);
  return rendered;
}

describe('loan confirmation', () => {
  it('discloses fees only through the star, picks once per opening, and refreshes without borrowing', async () => {
    let quotes = 0,
      accepts = 0;
    const random = vi.spyOn(Math, 'random').mockReturnValue(0.2);
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        if (String(input).endsWith('/quote')) {
          quotes++;
          return reply({ ...loanQuote(), quote_token: `cXVvdGU${quotes}.c2lnbmF0dXJl` });
        }
        accepts++;
        return accepts === 1
          ? reply({ error: { code: 'conflict', message: 'quote changed' } }, 409)
          : reply(loanReceipt(), 201);
      }),
    );
    const r = await renderCard();
    await r.user.click(screen.getByRole('button', { name: 'Get a loan' }));
    const dialog = await screen.findByRole('alertdialog');
    expect(r.container).not.toHaveTextContent(/repay/i);
    expect(dialog.querySelector('.loan-nominal')).toHaveTextContent('10000 Nonbiri credits');
    expect(within(dialog).queryByText('9000')).toBeNull();
    const star = within(dialog).getByRole('button', { name: 'View fees and balance details' });
    expect(star.closest('p')).toHaveTextContent('Please confirm the amount to borrow*.');
    const calls = random.mock.calls.length;
    await r.user.click(star);
    expect(within(dialog).getByText('9000')).toBeVisible();
    expect(within(dialog).getByText('13000')).toBeVisible();
    expect(random.mock.calls.length).toBe(calls + 1);
    await r.user.tab();
    expect(random.mock.calls.length).toBe(calls + 1);
    await r.user.click(star);
    expect(within(dialog).queryByText('9000')).toBeNull();
    await r.user.click(star);
    expect(random.mock.calls.length).toBe(calls + 2);
    await r.user.click(within(dialog).getByRole('button', { name: 'Confirm loan' }));
    await waitFor(() => expect(quotes).toBe(2));
    await waitFor(() =>
      expect(within(dialog).getByRole('button', { name: 'Confirm loan' })).toBeEnabled(),
    );
    expect(accepts).toBe(1);
    expect(within(dialog).queryByText('9000')).toBeNull();
    await r.user.click(within(dialog).getByRole('button', { name: 'Confirm loan' }));
    expect(await screen.findByText('Loan received')).toBeVisible();
    expect(accepts).toBe(2);
  });
  it('retries an uncertain result with the same key and token after the quote expires', async () => {
    const writes: { key: string | null; body: string }[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        if (String(input).endsWith('/quote')) return reply(loanQuote());
        writes.push({
          key: new Headers(init?.headers).get('Idempotency-Key'),
          body: String(init?.body),
        });
        if (writes.length === 1) throw new TypeError('connection lost');
        return reply(loanReceipt(), 201);
      }),
    );
    const r = await renderCard();
    await r.user.click(screen.getByRole('button', { name: 'Get a loan' }));
    await r.user.click(
      within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Confirm loan' }),
    );
    await screen.findByRole('button', { name: 'Check this loan' });
    const later = Date.now() + 61_000;
    vi.spyOn(Date, 'now').mockReturnValue(later);
    await act(async () => {
      await new Promise((resolve) => window.setTimeout(resolve, 1100));
    });
    await r.user.click(screen.getByRole('button', { name: 'Check later' }));
    expect(screen.getByRole('button', { name: 'Get a loan' })).toBeDisabled();
    await r.user.click(screen.getByRole('button', { name: 'Check this loan' }));
    await r.user.click(
      within(screen.getByRole('alertdialog')).getByRole('button', { name: 'Check this loan' }),
    );
    await screen.findByText('Loan received');
    expect(writes).toHaveLength(2);
    expect(writes[0].key?.length).toBeGreaterThanOrEqual(22);
    expect(writes[1]).toEqual(writes[0]);
  });
  it('does not expose a late quote after the account changes', async () => {
    let resolve!: (response: Response) => void;
    vi.stubGlobal(
      'fetch',
      vi.fn(
        () =>
          new Promise<Response>((yes) => {
            resolve = yes;
          }),
      ),
    );
    const r = await renderCard();
    await r.user.click(screen.getByRole('button', { name: 'Get a loan' }));
    const generation = beginManagementSessionRequest(r.queryClient, 'steward');
    noteManagementSessionSuccess(
      r.queryClient,
      'steward',
      { user: { id: '2', username: 'other', effective_level: 1 } },
      generation,
    );
    await act(async () => {
      resolve(reply(loanQuote()));
    });
    expect(screen.queryByRole('alertdialog')).toBeNull();
  });
});
