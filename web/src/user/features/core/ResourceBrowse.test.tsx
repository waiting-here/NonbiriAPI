import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { screen, waitFor } from '@testing-library/react';
import { useLocation, useNavigate } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../test/unit/support';
import { KeyBrowseSummary } from './ResourceBrowse';
import type { EndpointKey, KeyBindingView } from './types';

const binding = (n: number): KeyBindingView => ({
  id: String(n),
  model_id: String(n),
  model_full_name: `owner/model-${n}`,
  endpoint_id: '11',
  endpoint_key_id: '21',
  endpoint_base_url: 'https://example.com/v1',
  connector_type: 'openai-compatible',
  endpoint_note: '',
  display_head: 'head',
  display_tail: 'tail',
  key_note: '',
  upstream_model_id: 'shared-upstream',
  ord: 0,
  max_concurrency: 0,
  max_rpm: 0,
  state: 'available',
});
const key = {
  ...JSON.parse(
    readFileSync(
      resolve(process.cwd(), '../internal/resources/testdata/endpoint_key.json'),
      'utf8',
    ),
  ),
  browse: {
    model_count: '21',
    binding_count: '21',
    donation_eligibility: 'eligible',
    available_binding_count: '21',
    preview: [binding(1), binding(2), binding(3)],
    discovery: {
      state: 'unknown',
      revision: '1',
      result: null,
      safe_class: 'none',
      observed_at: null,
      count: null,
    },
  },
} as EndpointKey;

function Screen() {
  const location = useLocation();
  const navigate = useNavigate();
  return (
    <>
      <output data-testid="location">{location.search}</output>
      <button onClick={() => navigate(-1)}>Browser back</button>
      <button onClick={() => navigate(1)}>Browser forward</button>
      <KeyBrowseSummary accountId="1" endpointId="11" keyData={key} onRefresh={() => {}} />
    </>
  );
}

afterEach(() => {
  vi.unstubAllGlobals();
  window.localStorage.removeItem('nonbiri:user:key-bindings-page-size:v1');
});

describe('resource connection browsing', () => {
  it('opens lazily, reads every connection page and restores close/back/forward without losing outer pages', async () => {
    const fetchMock = vi.fn<typeof fetch>(async (input) => {
      const url = new URL(String(input), 'http://user.test');
      expect(url.pathname).toBe('/api/endpoints/11/keys/21/bindings');
      const page = url.searchParams.get('page');
      return new Response(
        JSON.stringify({
          data: page === '2' ? [binding(21)] : Array.from({ length: 20 }, (_, i) => binding(i + 1)),
          next_cursor: null,
          pagination: { page, page_size: 20, total_items: '21', total_pages: '2' },
        }),
        { headers: { 'content-type': 'application/json' } },
      );
    });
    vi.stubGlobal('fetch', fetchMock);
    const rendered = await renderWithProviders(<Screen />, {
      station: 'user',
      route: '/endpoints/11?keys_page=3&keys_page_size=50',
    });
    expect(screen.getAllByRole('link')).toHaveLength(3);
    expect(fetchMock).not.toHaveBeenCalled();
    await rendered.user.click(screen.getByRole('button', { name: 'Browse all 21 connections' }));
    expect(await screen.findByText('Page 1 of 2 · 21 items')).toBeVisible();
    expect(screen.getAllByRole('link')).toHaveLength(20);
    await rendered.user.click(screen.getByRole('button', { name: 'Next' }));
    expect(await screen.findByText('Page 2 of 2 · 21 items')).toBeVisible();
    expect(screen.getByRole('link', { name: 'owner/model-21' })).toHaveAttribute(
      'href',
      '/models?model_id=21',
    );
    await rendered.user.click(screen.getByRole('button', { name: 'Close' }));
    expect(screen.getAllByRole('link')).toHaveLength(3);
    expect(screen.getByTestId('location')).toHaveTextContent('?keys_page=3&keys_page_size=50');
    expect(screen.getByTestId('location')).not.toHaveTextContent('routes_');
    await rendered.user.click(screen.getByRole('button', { name: 'Browser back' }));
    expect(await screen.findByText('Page 2 of 2 · 21 items')).toBeVisible();
    await rendered.user.click(screen.getByRole('button', { name: 'Browser forward' }));
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Browse all 21 connections' })).toHaveAttribute(
        'aria-expanded',
        'false',
      ),
    );
    expect(screen.getByTestId('location')).not.toHaveTextContent('routes_');
  });

  it('restores an expanded deep link and replaces its rows when a read loses permission', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>(
        async () =>
          new Response(JSON.stringify({ error: { code: 'forbidden', message: 'denied' } }), {
            status: 403,
            headers: { 'content-type': 'application/json' },
          }),
      ),
    );
    await renderWithProviders(<Screen />, {
      station: 'user',
      route: '/endpoints/11?routes_21_page=2&routes_21_page_size=20',
    });
    expect(
      await screen.findByText('Your current session no longer permits this operation.'),
    ).toBeVisible();
    expect(screen.queryByRole('link')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Close' })).toHaveAttribute('aria-expanded', 'true');
  });
});
