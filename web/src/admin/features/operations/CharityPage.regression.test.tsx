import { screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../test/unit/support';
import { CharityPage } from '../../pages/CharityPage';

const pendingDonationSummary = {
  id: '1',
  status: 'pending',
  revision: '1',
  description: 'pending donation',
  review_result: null,
  created_at: 1,
  updated_at: 2,
  key_count: '1',
  state_counts: {
    available: '0',
    pending: '1',
    disabled: '0',
    suspended: '0',
    exhausted: '0',
    expired: '0',
    ended: '0',
  },
  source_count: '1',
  sources: [
    {
      kind: 'custom',
      connector_type: 'openai-compatible',
      base_url: 'https://controlled.example/v1',
    },
  ],
  handling: {
    state: 'pending',
    revision: '1',
    processed_at: null,
    processed_by_role: null,
    closed_at: null,
    closed_reason: null,
  },
  reviewer: null,
  owner: { user_id: '1', discord_id: null, display_name: 'fixture-user' },
};

describe('administrator charity page composition', () => {
  it('keeps review controls on donations and hides their filters on source browsing', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = new URL(String(input), 'http://localhost');
        if (url.pathname === '/admin/api/session') {
          return new Response(JSON.stringify({ admin: { username: 'fixture-admin' } }), {
            headers: { 'Content-Type': 'application/json' },
          });
        }
        if (url.pathname === '/admin/api/donations') {
          expect(url.search).toBe('?page=1&page_size=20');
          return new Response(
            JSON.stringify({
              data: [pendingDonationSummary],
              next_cursor: null,
              pagination: { page: '1', page_size: 20, total_items: '1', total_pages: '1' },
            }),
            { headers: { 'Content-Type': 'application/json' } },
          );
        }
        if (url.pathname === '/admin/api/donation-sources') {
          expect(url.search).toBe('?scope=active&page=1&page_size=20');
          return new Response(
            JSON.stringify({
              data: [],
              next_cursor: null,
              pagination: { page: '1', page_size: 20, total_items: '0', total_pages: '1' },
            }),
            { headers: { 'Content-Type': 'application/json' } },
          );
        }
        throw new Error(`Unexpected request: ${url.pathname}${url.search}`);
      }),
    );
    const rendered = await renderWithProviders(<CharityPage />, {
      station: 'admin',
      role: 'admin',
    });

    expect(await screen.findByRole('combobox', { name: 'Follow-up status' })).toBeInTheDocument();
    expect(await screen.findByRole('button', { name: 'Review' })).toBeInTheDocument();
    await rendered.user.click(screen.getByRole('tab', { name: 'Browse by source' }));
    expect(await screen.findByRole('heading', { name: 'No source groups' })).toBeInTheDocument();
    expect(screen.getByRole('combobox', { name: 'Source scope' })).toHaveValue('active');
    expect(screen.queryByRole('combobox', { name: 'Follow-up status' })).not.toBeInTheDocument();
    expect(screen.queryByRole('combobox', { name: 'Donation status' })).not.toBeInTheDocument();
    expect(
      screen.queryByRole('searchbox', { name: 'Search public description or donation ID' }),
    ).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Review' })).not.toBeInTheDocument();
    expect(screen.queryByTestId('grouped-charity-panel')).not.toBeInTheDocument();
  });
});
