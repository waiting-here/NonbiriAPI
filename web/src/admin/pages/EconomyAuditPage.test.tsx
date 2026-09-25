import { fireEvent, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import { EconomyAuditPage } from './EconomyAuditPage';

const metrics = {
  issued: '9007199254740993123',
  reclaimed: '0',
  user_income: '9007199254740993123',
  user_expense: '0',
  internal_transfer: '0',
  operations: '1',
};
const metadata = {
  asset: 'general',
  from: 1,
  to: 2,
  unit: 'milliunits',
  scale: '1000',
  offset_minutes: 330,
  ledger_seq: '1',
  projected_seq: '1',
  snapshot_at: 1_800_000_000,
  coverage: {
    status: 'complete',
    first_ledger_seq: '1',
    first_occurred_at: 1,
    unclassified_operations: '0',
    opening_known: true,
  },
};
const summary = {
  metadata,
  flows: metrics,
  inventory: {
    user_available: metrics.issued,
    frozen: '0',
    pools: '0',
    platform: '0',
    negative_users: '1000',
    negative_frozen: '0',
    negative_pools: '0',
    negative_platform: '0',
    net: '9007199254740992123',
  },
  reconciliation: {
    status: 'matched',
    scope: 'retained_ledger',
    inventory_net: '9007199254740992123',
    ledger_net: '9007199254740992123',
    interval_opening_net: '0',
    interval_closing_net: metrics.issued,
    interval_net_change: metrics.issued,
  },
};
const hostile = '<img src=x onerror=alert(1)>';

function response(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json' },
  });
}

describe('administrator economy audit', () => {
  it('renders exact stock and flow values separately and treats ledger details as text', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const path = new URL(
          input instanceof Request ? input.url : String(input),
          window.location.origin,
        ).pathname;
        if (path === '/admin/api/session') return response({ admin: { username: 'audit-admin' } });
        if (path.endsWith('/summary')) return response(summary);
        if (path.endsWith('/series')) return response({ metadata, bucket: 'hour', data: [] });
        if (path.endsWith('/operations'))
          return response({
            metadata,
            anchor_seq: '1',
            next_cursor: null,
            data: [
              {
                id: `op_${'A'.repeat(22)}`,
                ledger_seq: '1',
                kind: 'activity_exchange',
                source_type: 'operation',
                source_id: hostile,
                created_at: 1_800_000_000,
                classification: { channel: 'picture_book', behavior: 'exchange', known: true },
                entries: [
                  { asset: 'general', account_kind: 'user', user_id: null, delta: '-1000000' },
                  { asset: 'sketch_paper', account_kind: 'user', user_id: null, delta: '1000' },
                ],
              },
            ],
          });
        throw new Error(`Unexpected request: ${path}`);
      }),
    );
    const { container } = await renderWithProviders(<EconomyAuditPage />, {
      station: 'admin',
      locale: 'en',
    });
    expect(await screen.findByRole('heading', { name: 'Current stock' })).toBeVisible();
    expect(screen.getAllByText('9,007,199,254,740,993.123').length).toBeGreaterThan(0);
    expect(screen.getByText('Negative user balances')).toBeVisible();
    expect(screen.getByLabelText('From (UTC+05:30)')).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: 'Ledger' }));
    const text = await screen.findByText(hostile);
    expect(text.tagName).toBe('CODE');
    expect(container.querySelector('img')).toBeNull();
    expect(
      screen.getAllByText('Sketch paper').find((element) => element.tagName === 'TD'),
    ).toBeInTheDocument();
    expect(screen.getAllByText('Deidentified')).toHaveLength(2);
    expect(screen.getByText(/Pagination is anchored at ledger watermark/)).toBeVisible();
  });

  it('never presents a forbidden audit as an empty inventory', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const path = new URL(
          input instanceof Request ? input.url : String(input),
          window.location.origin,
        ).pathname;
        if (path === '/admin/api/session') return response({ admin: { username: 'audit-admin' } });
        return response(
          {
            error: {
              code: 'forbidden',
              message: 'Accounting audits require administrator access.',
            },
          },
          403,
        );
      }),
    );
    await renderWithProviders(<EconomyAuditPage />, { station: 'admin', locale: 'en' });
    await waitFor(() =>
      expect(
        screen.getAllByText('This action is not allowed for the current account.').length,
      ).toBeGreaterThan(0),
    );
    expect(screen.queryByRole('heading', { name: 'Current stock' })).not.toBeInTheDocument();
  });

  it('shows a localized audit failure without exposing the server message', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const path = new URL(
          input instanceof Request ? input.url : String(input),
          window.location.origin,
        ).pathname;
        if (path === '/admin/api/session') return response({ admin: { username: 'audit-admin' } });
        return response(
          { error: { code: 'service_unavailable', message: 'private database detail' } },
          503,
        );
      }),
    );
    await renderWithProviders(<EconomyAuditPage />, { station: 'admin', locale: 'zh' });
    expect(await screen.findAllByText('账务审计暂时无法加载，请重试。')).not.toHaveLength(0);
    expect(screen.queryByText(/private database detail/)).toBeNull();
  });
});
