import { screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import { FailureResetControl } from './FailureResetControl';
import type { FailureResetRef, FailureSelection } from '@shared/operations/failureReset';
import { OwnerFailureReset } from '../../user/features/economy/OwnerFailureReset';

const json = (value: unknown, status = 200) =>
  new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } });
describe('failure reset controls', () => {
  it('selects everything before batching, and continues an unknown response with the same body and key', async () => {
    const calls: {
      path: string;
      body: { items?: FailureResetRef[]; selection?: FailureSelection; cursor?: string | null };
      key: string | null;
    }[] = [];
    let fail = true;
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input, options) => {
        const path = new URL(String(input), 'http://localhost').pathname;
        const body = JSON.parse(options.body);
        const key = new Headers(options.headers).get('Idempotency-Key');
        calls.push({ path, body, key });
        if (path.endsWith('/selection')) {
          const start = body.cursor ? 101 : 1;
          return json({
            items: Array.from({ length: start === 1 ? 100 : 1 }, (_, i) => ({
              donation_id: '1',
              key_id: String(start + i),
              expected_revision: '7',
            })),
            next_cursor: start === 1 ? 'next' : null,
          });
        }
        if (fail) {
          fail = false;
          return json({ error: { code: 'internal', message: 'Retry this request' } }, 500);
        }
        const revision = String(
          BigInt(body.items[0].expected_revision) + BigInt(body.items.length),
        );
        return json({
          results: body.items.map((item: FailureResetRef) => ({
            donation_id: item.donation_id,
            key_id: item.key_id,
            status: 'reset',
            revision,
          })),
          counts: {
            processed: String(body.items.length),
            reset: String(body.items.length),
            skipped: '0',
          },
        });
      }),
    );
    const view = await renderWithProviders(
      <FailureResetControl
        role="admin"
        selection={{ view: 'donations', status: 'approved' }}
        choices={[]}
      />,
      { station: 'admin', locale: 'en' },
    );
    view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'fixture' } });
    await view.user.click(screen.getByText('Reset failure count', { selector: 'summary' }));
    await view.user.click(screen.getByRole('button', { name: 'Reset all matching keys' }));
    await screen.findByRole('button', { name: 'Continue' });
    expect(calls.map((call) => call.path.endsWith('/selection'))).toEqual([true, true, false]);
    expect(calls[0].key).toBeNull();
    expect(calls[0].body.selection).toEqual({ view: 'donations', status: 'approved' });
    await view.user.click(screen.getByRole('button', { name: 'Continue' }));
    await screen.findByText('Finished · 101 selected · 101 reset · 0 skipped');
    expect(calls[3]).toEqual(calls[2]);
    expect(calls[4].key).not.toBe(calls[2].key);
    expect(calls[4].body.items![0].expected_revision).toBe('107');
  });
  it('retains checked references when pages change and shows per-item conflicts', async () => {
    const writes: FailureResetRef[][] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn(async (_input, options) => {
        const { items } = JSON.parse(options.body);
        writes.push(items);
        return json({
          results: items.map((item: FailureResetRef) => ({
            donation_id: item.donation_id,
            key_id: item.key_id,
            status: 'conflict',
            revision: '9',
          })),
          counts: { processed: String(items.length), reset: '0', skipped: String(items.length) },
        });
      }),
    );
    const first = {
      id: '1',
      label: 'First key',
      target: { donation_id: '1', key_id: '1', expected_revision: '7' },
    };
    const second = {
      id: '2',
      label: 'Second key',
      target: { donation_id: '1', key_id: '2', expected_revision: '7' },
    };
    const props = {
      role: 'admin' as const,
      selection: { view: 'donation_keys' as const, donation_id: '1' },
    };
    const view = await renderWithProviders(<FailureResetControl {...props} choices={[first]} />, {
      station: 'admin',
      locale: 'en',
    });
    view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'fixture' } });
    await view.user.click(screen.getByText('Reset failure count', { selector: 'summary' }));
    await view.user.click(screen.getByLabelText('First key'));
    view.rerender(<FailureResetControl {...props} choices={[second]} />);
    await view.user.click(screen.getByLabelText('Second key'));
    await view.user.click(screen.getByRole('button', { name: 'Reset selected (2)' }));
    await screen.findByText('Finished · 2 selected · 0 reset · 2 skipped');
    expect(writes).toEqual([[first.target, second.target]]);
    expect(screen.getAllByText(/Changed during selection or reset/)).toHaveLength(2);
  });
  it('retries an owner reset with its original revision after the page refreshes', async () => {
    const calls: { body: { expected_revision: string }; key: string | null }[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn(async (_input, options) => {
        calls.push({
          body: JSON.parse(options.body),
          key: new Headers(options.headers).get('Idempotency-Key'),
        });
        return calls.length === 1
          ? json({ error: { code: 'internal', message: 'Response lost' } }, 500)
          : json({ donation_id: '1', key_id: '2', revision: '8', failure_streak: '0' });
      }),
    );
    const view = await renderWithProviders(
      <OwnerFailureReset donationID="1" keyID="2" revision="7" disabled={false} />,
      { station: 'user', locale: 'en' },
    );
    view.queryClient.setQueryData(['user', 'session'], {
      user: { id: '3', username: 'fixture', effective_level: 1 },
    });
    await view.user.click(screen.getByRole('button', { name: 'Reset failure count' }));
    await screen.findByRole('button', { name: 'Continue' });
    view.rerender(<OwnerFailureReset donationID="1" keyID="2" revision="8" disabled={false} />);
    await view.user.click(screen.getByRole('button', { name: 'Continue' }));
    await screen.findByText('Failure count reset.');
    expect(calls[1]).toEqual(calls[0]);
  });
  it('clears an operation when a management request loses its current role', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => json({ error: { code: 'forbidden', message: 'Access denied' } }, 403)),
    );
    const revoked = vi.fn();
    const view = await renderWithProviders(
      <FailureResetControl
        role="steward"
        selection={{ view: 'donations' }}
        choices={[]}
        onCapabilityLoss={revoked}
      />,
      { station: 'user', locale: 'en' },
    );
    view.queryClient.setQueryData(['user', 'session'], {
      user: { id: '3', username: 'fixture', effective_level: 6 },
    });
    await view.user.click(screen.getByText('Reset failure count', { selector: 'summary' }));
    await view.user.click(screen.getByRole('button', { name: 'Reset all matching keys' }));
    await waitFor(() => expect(revoked).toHaveBeenCalledOnce());
    expect(fetch).toHaveBeenCalledOnce();
    expect(screen.queryByRole('button', { name: 'Continue' })).not.toBeInTheDocument();
  });
});
