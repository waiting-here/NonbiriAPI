import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../test/unit/support';
import { PriceTemplateManagement } from './PriceTemplateManagement';
import type { CharityPriceTemplate } from '@shared/operations/charityPriceTemplates';
vi.mock('../AsyncLeaveDialog', () => ({ AsyncLeaveDialog: () => null }));
const template: CharityPriceTemplate = {
  recurring_discount: { enabled: false, time_zone: 'UTC', default_percent: 100, periods: [] },

  id: '3',
  name: 'Full token prices',
  description: 'All price fields',
  enabled: true,
  revision: '4',
  created_at: 1,
  updated_at: 2,
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
  discount: { enabled: true, percent: 80, start_at: 2000000000, end_at: 2000003600 },
};
const json = (value: unknown, status = 200) =>
  new Response(JSON.stringify(value), { status, headers: { 'content-type': 'application/json' } });
function fixtures(write: (body: Record<string, unknown>, init: RequestInit) => Promise<Response>) {
  const writes: RequestInit[] = [];
  let deleted = false;
  vi.stubGlobal(
    'fetch',
    vi.fn<typeof fetch>(async (input, init) => {
      if (init?.method && init.method !== 'GET') {
        writes.push(init);
        if (init.method === 'DELETE') deleted = true;
        return write(JSON.parse(String(init.body)), init);
      }
      const url = new URL(String(input), 'https://example.test');
      if (url.pathname.endsWith('/time-context')) return json({ mode: 'site', offset_minutes: 0 });
      if (url.pathname.endsWith('/3'))
        return deleted
          ? json({ error: { code: 'not_found', message: 'Deleted' } }, 404)
          : json(template);
      return json({
        data: [template],
        next_cursor: null,
        pagination: { page: '1', page_size: 20, total_items: '1', total_pages: '1' },
      });
    }),
  );
  return writes;
}
afterEach(() => vi.unstubAllGlobals());
async function open(route = '/?charity_section=templates&price_template=3') {
  const view = await renderWithProviders(<PriceTemplateManagement accountId="fixture-admin" />, {
    station: 'admin',
    route,
  });
  view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'fixture-admin' } });
  return view;
}
describe('independent price-template management', () => {
  it('edits every complete-price field independently, then deletes its fixed target', async () => {
    const writes = fixtures(async (body, init) =>
      init.method === 'DELETE'
        ? new Response(null, { status: 204 })
        : json({ ...template, ...body, revision: '5' }),
    );
    const view = await open();
    await screen.findByDisplayValue('Full token prices');
    fireEvent.change(screen.getByLabelText('Template name'), {
      target: { value: 'Changed template' },
    });
    fireEvent.change(screen.getByLabelText('Template description'), {
      target: { value: 'Changed description' },
    });
    await view.user.click(screen.getByRole('switch', { name: 'Enable this price template' }));
    await view.user.click(screen.getByText('Quota and reserve', { selector: 'strong' }));
    fireEvent.change(screen.getByLabelText('Call reservation (credits)'), {
      target: { value: '' },
    });
    await view.user.click(
      screen.getByRole('button', { name: 'Save template “Full token prices”' }),
    );
    await waitFor(() => expect(writes).toHaveLength(1));
    expect(JSON.parse(String(writes[0]!.body))).toEqual({
      name: 'Changed template',
      description: 'Changed description',
      enabled: false,
      pricing: template.pricing,
      token_reserve_credits: null,
      discount: template.discount,
      recurring_discount: template.recurring_discount,
      expected_revision: '4',
    });
    await screen.findByText('Editing “Changed template” · revision 5');
    await view.user.click(screen.getByRole('button', { name: 'Delete price template' }));
    const dialog = screen.getByRole('alertdialog', { name: 'Delete template “Changed template”?' });
    expect(dialog).toHaveTextContent('Prices already filled or saved in models stay unchanged');
    await view.user.click(within(dialog).getByRole('button', { name: 'Delete price template' }));
    await waitFor(() => expect(writes).toHaveLength(2));
    expect(JSON.parse(String(writes[1]!.body))).toEqual({ expected_revision: '5' });
    await screen.findByRole('heading', { name: 'Price templates' });
  });
  it('creates a new template without a model, pins the saved target, and retries an unknown result with the same identity', async () => {
    let attempts = 0;
    const writes = fixtures(async (body) => {
      if (attempts++ === 0) throw new TypeError('Network disconnected');
      return json({ ...template, ...body, revision: String(attempts - 1) });
    });
    const view = await open('/?charity_section=templates&price_template=new');
    fireEvent.change(screen.getByLabelText('Template name'), {
      target: { value: 'New request template' },
    });
    await view.user.click(screen.getByRole('button', { name: 'Create price template' }));
    await view.user.click(await screen.findByRole('button', { name: 'Retry the original action' }));
    await screen.findByText('Editing “New request template” · revision 1');
    expect(writes).toHaveLength(2);
    expect(writes[0]!.body).toEqual(writes[1]!.body);
    expect(new Headers(writes[0]!.headers).get('Idempotency-Key')).toEqual(
      new Headers(writes[1]!.headers).get('Idempotency-Key'),
    );
    expect(JSON.parse(String(writes[0]!.body))).toMatchObject({
      pricing: { mode: 'per_request', user_price: '0', donor_reward: '0' },
      token_reserve_credits: null,
    });
    fireEvent.change(screen.getByLabelText('Template description'), {
      target: { value: 'Saved target stays fixed' },
    });
    await view.user.click(
      screen.getByRole('button', { name: 'Save template “New request template”' }),
    );
    await waitFor(() => expect(writes).toHaveLength(3));
    expect(writes[2]!.method).toBe('PUT');
    expect(JSON.parse(String(writes[2]!.body))).toMatchObject({
      expected_revision: '1',
      description: 'Saved target stays fixed',
    });
  });

  it('keeps a conflicting draft while loading the latest revision', async () => {
    const writes = fixtures(async () =>
      json({ error: { code: 'revision_conflict', message: 'Changed' } }, 409),
    );
    const view = await open();
    await screen.findByDisplayValue('Full token prices');
    fireEvent.change(screen.getByLabelText('Template description'), {
      target: { value: 'Preserve this draft' },
    });
    await view.user.click(
      screen.getByRole('button', { name: 'Save template “Full token prices”' }),
    );
    await screen.findByText(
      'This template changed. Your draft is kept. Load the latest revision before saving again.',
    );
    await view.user.click(
      screen.getByRole('button', { name: 'Load latest revision and keep draft' }),
    );
    await screen.findByText('Loaded “Full token prices”, revision 4. Your draft is unchanged.');
    expect(screen.getByLabelText('Template description')).toHaveValue('Preserve this draft');
    expect(writes).toHaveLength(1);
  });
});
