import { fireEvent, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../test/unit/support';
import type { CharityModel, CharityPriceConfig } from '@shared/operations/charity';
import { PriceTools } from './PriceTools';

const request: CharityPriceConfig = {
  pricing: { mode: 'per_request', user_price: '3.5', donor_reward: '1.75' },
  token_reserve_credits: null,
  discount: { enabled: false, percent: 0, start_at: null, end_at: null },
  recurring_discount: { enabled: false, time_zone: 'UTC', default_percent: 0, periods: [] },
};
const token: CharityPriceConfig = {
  pricing: {
    mode: 'per_token',
    user_prices: {
      uncached_input: '20',
      cache_write_input: '24',
      cache_read_input: '2',
      output: '60',
    },
    donor_rewards: {
      uncached_input: '10',
      cache_write_input: '12',
      cache_read_input: '1',
      output: '30',
    },
    long_context: {
      input_threshold: '100000',
      user_prices: {
        uncached_input: '40',
        cache_write_input: '48',
        cache_read_input: '4',
        output: '120',
      },
    },
    context_penalty: { input_threshold: '200000', fine: '500' },
  },
  token_reserve_credits: '25.125',
  discount: { enabled: true, percent: 80, start_at: null, end_at: null },
  recurring_discount: {
    enabled: true,
    time_zone: 'Asia/Tokyo',
    default_percent: 50,
    periods: [{ weekdays: [1, 7], start_time: '22:00', end_time: '02:00', percent: 0 }],
  },
};
const model = (index: number): CharityModel => ({
  ...request,
  effective_discount: { source: 'none', percent: 100, next_change_at: null },
  server_now: 1800000000,
  id: String(index),
  provider: 'Provider',
  model: `model-${index}`,
  full_name: `[公益]Provider/model-${index}`,
  model_types: ['chat_completions'],
  route_strategy: 'ordered',
  enabled: false,
  allowed_levels: [1],
  public_description: '',
  transport_rule: 'passthrough',
  flatten_tool_calls: false,
  revision: '9',
  binding_revision: '0',
  binding_count: '0',
  rolling_success: { sample_count: '0', success_count: '0', percent: null },
  created_at: 1,
  updated_at: 1,
});
const json = (value: unknown) =>
  new Response(JSON.stringify(value), { headers: { 'content-type': 'application/json' } });
const page = (data: unknown[], pageNumber = 1, total = data.length, size = 20) => ({
  data,
  next_cursor: null,
  pagination: {
    page: String(pageNumber),
    page_size: size,
    total_items: String(total),
    total_pages: String(Math.max(1, Math.ceil(total / size))),
  },
});
afterEach(() => vi.unstubAllGlobals());

describe('prices inside a model workspace', () => {
  it('applies only the complete price snapshot and exposes a separate administration entry', async () => {
    let template = {
      ...token,
      id: '3',
      name: 'Token prices',
      description: 'Full prices',
      enabled: true,
      revision: '4',
      created_at: 1,
      updated_at: 2,
    };
    const writes: { method: string; body: Record<string, unknown> }[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>(async (_input, init) => {
        const method = init?.method ?? 'GET';
        if (method === 'GET') return json(page(template.enabled ? [template] : []));
        const body = JSON.parse(String(init?.body)) as Record<string, unknown>;
        writes.push({ method, body });
        template = { ...template, ...body, revision: String(Number(template.revision) + 1) };
        return json(template);
      }),
    );
    const apply = vi.fn();
    const view = await renderWithProviders(<PriceTools role="admin" onApply={apply} />, {
      station: 'admin',
    });
    view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'fixture-admin' } });
    await view.user.click(screen.getByText('Price templates', { selector: 'strong' }));
    await view.user.click(await screen.findByRole('button', { name: 'View full prices' }));
    expect(apply).not.toHaveBeenCalled();
    await view.user.click(screen.getByRole('button', { name: 'Fill all prices' }));
    expect(apply).toHaveBeenCalledWith(token, 'Token prices', '4');
    expect(Object.keys(apply.mock.calls[0]![0]).sort()).toEqual([
      'discount',
      'pricing',
      'recurring_discount',
      'token_reserve_credits',
    ]);
    expect(writes).toHaveLength(0);
    expect(screen.getByRole('button', { name: 'Maintain price templates' })).toBeVisible();
    expect(screen.queryByLabelText('Template name')).not.toBeInTheDocument();
    await view.user.click(screen.getByRole('button', { name: 'Maintain price templates' }));
    expect(screen.queryByRole('button', { name: 'Fill all prices' })).not.toBeInTheDocument();
  });

  it('gives level 6 only enabled template reads and local filling', async () => {
    const paths: string[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>(async (input, init) => {
        expect(init?.method ?? 'GET').toBe('GET');
        paths.push(String(input));
        return json(
          page([
            {
              ...token,
              id: '3',
              name: 'Token prices',
              description: '',
              enabled: true,
              revision: '4',
              created_at: 1,
              updated_at: 2,
            },
          ]),
        );
      }),
    );
    const apply = vi.fn();
    const view = await renderWithProviders(<PriceTools role="steward" onApply={apply} />, {
      station: 'user',
    });
    await view.user.click(screen.getByText('Price templates', { selector: 'strong' }));
    await view.user.click(await screen.findByRole('button', { name: 'View full prices' }));
    await view.user.click(screen.getByRole('button', { name: 'Fill all prices' }));
    expect(apply).toHaveBeenCalledWith(token, 'Token prices', '4');
    expect(screen.queryByText('Maintain price templates')).not.toBeInTheDocument();
    expect(paths).toEqual([
      '/api/steward/charity-price-templates?state=enabled&page=1&page_size=20',
    ]);
  });

  it('browses 240 reference models by numbered pages and fills prices without copying model identity', async () => {
    const models = Array.from({ length: 240 }, (_, index) => model(index + 1));
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>(async (input, init) => {
        expect(init?.method ?? 'GET').toBe('GET');
        const url = new URL(String(input), 'https://example.test');
        const size = Number(url.searchParams.get('page_size') ?? '20');
        const current = Number(url.searchParams.get('page') ?? '1');
        return json(
          page(models.slice((current - 1) * size, current * size), current, models.length, size),
        );
      }),
    );
    const apply = vi.fn();
    const view = await renderWithProviders(<PriceTools role="admin" onApply={apply} />, {
      station: 'admin',
    });
    await view.user.click(
      screen.getByText("Reference an existing model's prices", { selector: 'strong' }),
    );
    await screen.findByText('[公益]Provider/model-1');
    const pager = screen.getByRole('navigation', { name: 'Pagination' });
    const jump = within(pager).getByRole('textbox');
    fireEvent.change(jump, { target: { value: '12' } });
    fireEvent.keyDown(jump, { key: 'Enter' });
    const row = await screen.findByRole('row', { name: /\[公益\]Provider\/model-240/ });
    await view.user.click(within(row).getByRole('button', { name: 'View full prices' }));
    await view.user.click(screen.getByRole('button', { name: 'Fill these prices' }));
    expect(apply).toHaveBeenCalledWith(request, '[公益]Provider/model-240', '9');
    expect(apply.mock.calls[0]![0]).not.toHaveProperty('provider');
    expect(apply.mock.calls[0]![0]).not.toHaveProperty('enabled');
  });
});
