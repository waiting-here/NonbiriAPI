import { screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { useNavigate } from 'react-router';
import { renderWithProviders } from '../../../../test/unit/support';
import { DonationHandlingPanel } from './DonationHandlingPanel';

const pending = {
  state: 'pending',
  revision: '1',
  processed_at: null,
  processed_by_role: null,
  closed_at: null,
  closed_reason: null,
};
const json = (value: unknown) =>
  new Response(JSON.stringify(value), { headers: { 'content-type': 'application/json' } });

function HandlingHistory() {
  const navigate = useNavigate();
  return (
    <>
      <button type="button" onClick={() => navigate(-1)}>
        Previous filter
      </button>
      <DonationHandlingPanel accountId="5" />
    </>
  );
}

describe('minimal approved donation handling', () => {
  it('lets level 5 mark the whole unbound donation and reads no donor, source, or key detail', async () => {
    let processed = false;
    const paths: string[] = [];
    const writes: Record<string, unknown>[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>(async (input, init) => {
        const url = new URL(String(input), 'https://example.test');
        paths.push(url.pathname);
        if ((init?.method ?? 'GET') === 'POST') {
          expect(url.pathname).toBe('/api/steward/donations/7/handling/processed');
          writes.push(JSON.parse(String(init?.body)) as Record<string, unknown>);
          processed = true;
          return json({
            donation_id: '7',
            handling: {
              ...pending,
              state: 'processed',
              revision: '2',
              processed_at: 100,
              processed_by_role: 'steward',
            },
          });
        }
        expect(url.pathname).toBe('/api/steward/donation-handling');
        const handling = processed
          ? {
              ...pending,
              state: 'processed',
              revision: '2',
              processed_at: 100,
              processed_by_role: 'steward',
            }
          : pending;
        const rows =
          (url.searchParams.get('handling') === 'processed') === processed
            ? [{ donation_id: '7', created_at: 1, key_count: '4', bound_key_count: '0', handling }]
            : [];
        return json({
          data: rows,
          next_cursor: null,
          pagination: {
            page: '1',
            page_size: 20,
            total_items: String(rows.length),
            total_pages: '1',
          },
        });
      }),
    );
    const view = await renderWithProviders(<HandlingHistory />, {
      station: 'user',
    });
    view.queryClient.setQueryData(['user', 'session'], {
      user: { id: '5', username: 'fixture-level5', effective_level: 5 },
    });
    const process = await screen.findByRole('button', {
      name: 'Mark the whole donation as processed',
    });
    expect(
      screen.getByText('This action marks the whole donation, including all 4 keys.'),
    ).toBeVisible();
    expect(screen.getByText('4 keys, 0 with connections.')).toBeVisible();
    await view.user.click(process);
    await waitFor(() => expect(writes).toEqual([{ expected_handling_revision: '1' }]));
    await view.user.click(await screen.findByRole('button', { name: 'View processed' }));
    await screen.findByText('4 keys, 0 with connections.');
    const filter = screen.getByRole('textbox');
    await view.user.type(filter, '7');
    await view.user.click(screen.getByRole('button', { name: 'Search' }));
    await screen.findByText('4 keys, 0 with connections.');
    await view.user.clear(filter);
    await view.user.type(filter, '9');
    expect(filter).toHaveValue('9');
    await view.user.click(screen.getByRole('button', { name: 'Previous filter' }));
    await waitFor(() => expect(filter).toHaveValue(''));
    expect(
      screen.queryByRole('button', { name: 'Mark the whole donation as processed' }),
    ).not.toBeInTheDocument();
    expect(
      paths.every(
        (path) =>
          path === '/api/steward/donation-handling' ||
          path === '/api/steward/donations/7/handling/processed',
      ),
    ).toBe(true);
    expect(screen.queryByLabelText('Provider')).not.toBeInTheDocument();
  });
});
