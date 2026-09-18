import { act, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { charityKeys } from '@shared/operations/charity';
import { useLocation, useNavigate } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import { CharityPage } from '../../admin/pages/CharityPage';
import { CharityManagement } from './CharityManagement';

const sourceKey = (index: number) => `dsg_${'A'.repeat(41)}${'ABCDEFGHIJKLMNOPQRSTUVWXYZ'[index]}A`;
const safeSource = {
  kind: 'custom',
  connector_type: 'openai-compatible',
  base_url: 'https://source-0.example.test/v1',
};
const handling = {
  state: 'pending',
  revision: '1',
  processed_at: null,
  processed_by_role: null,
  closed_at: null,
  closed_reason: null,
};
const key = (index: number, state = 'available') => ({
  id: String(index + 11),
  endpoint_key_id: String(index + 111),
  display_head: `head${index + 11}`,
  display_tail: 'tail',
  safe_source: safeSource,
  physical_enabled: true,
  charity_state: state,
  limits: { price: null, calls: null, tokens: null },
  usage: {
    price_used: '0',
    price_inflight: '0',
    calls_used: '0',
    calls_inflight: '0',
    tokens_used: '0',
    tokens_inflight: '0',
  },
  token_reserve: 32,
  authorized_expires_at: null,
  expires_at: null,
  failure_disable_threshold: '10',
  streak: { generation: '1', count: '0', failure_disabled: false },
  ended_reason: null,
  safe_note: `note${index + 11}`,
  max_concurrency: 0,
  max_rpm: 0,
  binding_count: '0',
  idle: true,
});
function donation(pending = false) {
  return {
    id: '7',
    status: pending ? 'pending' : 'approved',
    revision: '1',
    handling,
    description: 'Shared donation',
    review_result: pending ? null : { decision: 'approve', reason: 'Accepted', reviewed_at: 10 },
    keys: Array.from({ length: 21 }, (_, index) => key(index, pending ? 'pending' : 'available')),
    owner: { user_id: '7', discord_id: null, display_name: 'Synthetic donor' },
    reviewer: pending ? null : { user_id: '9', role: 'admin' },
    created_at: 1,
    updated_at: 10,
  };
}
function summary(value: ReturnType<typeof donation>) {
  const { keys, ...header } = value;
  return {
    ...header,
    key_count: String(keys.length),
    source_count: '1',
    sources: [safeSource],
    state_counts: {
      pending: value.status === 'pending' ? '21' : '0',
      available: value.status === 'approved' ? '21' : '0',
      disabled: '0',
      suspended: '0',
      exhausted: '0',
      expired: '0',
      ended: '0',
    },
  };
}
function page<T>(rows: T[], url: URL) {
  const size = Number(url.searchParams.get('page_size') ?? 20);
  const pages = Math.max(1, Math.ceil(rows.length / size));
  const number = Math.min(Number(url.searchParams.get('page') ?? 1), pages);
  return {
    data: rows.slice((number - 1) * size, number * size),
    next_cursor: null,
    pagination: {
      page: String(number),
      page_size: size,
      total_items: String(rows.length),
      total_pages: String(pages),
    },
  };
}
function json(value: unknown, status = 200) {
  return new Response(JSON.stringify(value), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}
function Probe() {
  const navigate = useNavigate();
  return (
    <>
      <output data-testid="url">{useLocation().search}</output>
      <button type="button" onClick={() => navigate(-1)}>
        Browser back
      </button>
    </>
  );
}
function install(pending = false, role: 'admin' | 'steward' = 'admin') {
  const root = role === 'admin' ? '/admin/api' : '/api/steward';
  let current = donation(pending);
  const requests: URL[] = [];
  const reviews: {
    expected_revision: string;
    key_settings: { donation_key_id: string; safe_note: string }[];
  }[] = [];
  const fetchMock = vi.fn<typeof fetch>(async (input, init) => {
    const url = new URL(String(input), 'http://admin.test');
    requests.push(url);
    const method = init?.method ?? 'GET';
    if (url.pathname === '/admin/api/session')
      return json({ admin: { username: 'fixture-admin' } });
    if (url.pathname === '/admin/api/time-zones' || url.pathname === '/api/time-zones')
      return json({ version: 'go1.26.6-zoneinfo', zones: ['America/Indianapolis', 'UTC'] });
    if (url.pathname === `${root}/donation-sources`)
      return json(
        page(
          Array.from({ length: 21 }, (_, index) => ({
            source_key: sourceKey(index),
            safe_source: { ...safeSource, base_url: `https://source-${index}.example.test/v1` },
            donation_count: '1',
            key_count: '21',
            usable_key_count: '21',
            pending_donation_count: '1',
          })),
          url,
        ),
      );
    if (url.pathname === `${root}/donations`) return json(page([summary(current)], url));
    if (url.pathname === `${root}/donations/7`) return json(current);
    if (
      url.pathname === `${root}/donations/7/keys` ||
      url.pathname === `${root}/donation-sources/${sourceKey(0)}/keys`
    ) {
      return json(
        page(
          current.keys.map((entry) => ({
            ...entry,
            donation_id: '7',
            key_id: entry.id,
            donation_revision: current.revision,
            rule_count: '0',
            rules: [],
            handling,
          })),
          url,
        ),
      );
    }
    if (url.pathname === `${root}/donations/7/review` && method === 'POST') {
      const body = JSON.parse(String(init?.body));
      reviews.push(body);
      current = { ...donation(), revision: '2' };
      return json(current);
    }
    throw new Error(`Unexpected request: ${method} ${url.pathname}${url.search}`);
  });
  vi.stubGlobal('fetch', fetchMock);
  return {
    requests,
    reviews,
    setRevision: (revision: string) => {
      current = { ...current, revision };
    },
  };
}
function installBindingNavigation(role: 'admin' | 'steward', forbidden = false) {
  const fixture = install(false, role);
  const root = role === 'admin' ? '/admin/api' : '/api/steward';
  const previousFetch = globalThis.fetch;
  const models = Array.from({ length: 21 }, (_, index) => ({
    route_strategy: 'expiry_weighted',
    id: String(index + 1),
    provider: 'Provider',
    model: `Model${index + 1}`,
    full_name: `[公益]Provider/Model${index + 1}`,
    enabled: true,
    allowed_levels: [1, 2, 3, 4, 5],
    public_description: '',
    pricing: { mode: 'per_request', user_price: '1', donor_reward: '0' },
    discount: { enabled: false, percent: 0, start_at: null, end_at: null },
    flatten_tool_calls: false,
    revision: '1',
    binding_revision: '1',
    binding_count: '1',
    rolling_success: { sample_count: '0', success_count: '0', percent: null },
    created_at: 1,
    updated_at: 1,
  }));
  vi.stubGlobal(
    'fetch',
    vi.fn<typeof fetch>(async (input, init) => {
      const url = new URL(String(input), 'http://admin.test');
      expect(init?.method ?? 'GET').toBe('GET');
      if (url.pathname === `${root}/charity-models`) return json(page(models, url));
      if (url.pathname === `${root}/charity-models/1`) return json(models[0]);
      if (url.pathname === `${root}/charity-models/1/bindings`)
        return json({
          binding_revision: '1',
          bindings: [
            {
              id: '3',
              ord: 0,
              donation_id: '7',
              donation_key_id: '31',
              source: {
                connector_type: 'openai-compatible',
                canonical_base_url: safeSource.base_url,
                display_head: 'head31',
                display_tail: 'tail',
              },
              upstream_model_id: 'embedding-model',
              source_types: ['manual'],
            },
          ],
        });
      if (forbidden && url.pathname === `${root}/donations/7`)
        return json({ error: { code: 'forbidden', message: 'Permission lost' } }, 403);
      return previousFetch(input, init);
    }),
  );
  return { ...fixture, root };
}
afterEach(() => vi.unstubAllGlobals());

describe('managed donation page integration', () => {
  it.each(['admin', 'steward'] as const)(
    '%s opens a bound key on its own page and returns to the selected model and filters',
    async (role) => {
      const { requests, root } = installBindingNavigation(role);
      const view = await renderWithProviders(
        <>
          <CharityManagement frame={role} accountId="1" />
          <Probe />
        </>,
        {
          station: role === 'admin' ? 'admin' : 'user',
          route:
            '/charity?tab=charity&charity_section=models&models_page=2&model_q=Model&charity_model=1&donation_keys_page=9',
        },
      );
      await view.user.click(await screen.findByRole('button', { name: 'Manage key #31' }));
      const selected = await screen.findByRole('heading', { name: 'Key 31 · head31…tail' });
      expect(selected.closest('section.is-selected')).toBeVisible();
      expect(
        screen.queryByRole('heading', { name: 'Key 11 · head11…tail' }),
      ).not.toBeInTheDocument();
      expect(
        requests.some(
          (url) =>
            url.pathname === `${root}/donations/7/keys` && url.searchParams.get('page') === '2',
        ),
      ).toBe(true);
      await view.user.click(screen.getByRole('button', { name: 'Return to model configuration' }));
      expect(await screen.findByRole('heading', { name: '[公益]Provider/Model1' })).toBeVisible();
      expect(await screen.findByText('[公益]Provider/Model21')).toBeVisible();
      const params = new URLSearchParams(screen.getByTestId('url').textContent ?? '');
      expect(params.get('charity_model')).toBe('1');
      expect(params.get('models_page')).toBe('2');
      expect(params.get('model_q')).toBe('Model');
      expect(params.get('tab')).toBe('charity');
      expect(params.has('donation_id')).toBe(false);
      expect(params.has('donation_key')).toBe(false);
      await view.user.click(screen.getByRole('button', { name: 'Browser back' }));
      expect(await screen.findByRole('heading', { name: 'Key 31 · head31…tail' })).toBeVisible();
    },
  );
  it.each(['admin', 'steward'] as const)(
    '%s clears the management view when opening a bound key loses permission',
    async (role) => {
      installBindingNavigation(role, true);
      const lost = vi.fn();
      const view = await renderWithProviders(
        <CharityManagement frame={role} accountId="1" onCapabilityLoss={lost} />,
        {
          station: role === 'admin' ? 'admin' : 'user',
          route: '/charity?charity_section=models&charity_model=1',
        },
      );
      await view.user.click(await screen.findByRole('button', { name: 'Manage key #31' }));
      await waitFor(() => expect(lost).toHaveBeenCalled());
      expect(
        screen.queryByRole('heading', { name: 'Key 31 · head31…tail' }),
      ).not.toBeInTheDocument();
      expect(screen.queryByRole('button', { name: 'Manage key #31' })).not.toBeInTheDocument();
      expect(screen.queryByText('Synthetic donor')).not.toBeInTheDocument();
    },
  );
  it('retains an open recurring draft across skewed header and key-page revision refreshes', async () => {
    const fixture = install();
    const initialFetch = globalThis.fetch;
    let holdKeys = false;
    let releaseKeys!: () => void;
    const keysGate = new Promise<void>((resolve) => {
      releaseKeys = resolve;
    });
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>(async (input, init) => {
        const url = new URL(String(input), 'http://admin.test');
        if (url.pathname === '/admin/api/donations/7/keys/11/recurring-limits') {
          return json({
            donation_id: '7',
            key_id: '11',
            donation_revision: '1',
            server_now: 1_800_000_000,
            rules: [],
          });
        }
        if (holdKeys && url.pathname === '/admin/api/donations/7/keys') await keysGate;
        return initialFetch(input, init);
      }),
    );
    const view = await renderWithProviders(<CharityPage />, {
      station: 'admin',
      route: '/charity?donation_id=7',
    });
    await screen.findByRole('heading', { name: 'Key 11 · head11…tail' });
    const disclosure = view.container.querySelector(
      'details.recurring-limits-disclosure',
    ) as HTMLDetailsElement;
    await view.user.click(disclosure.querySelector('summary')!);
    await waitFor(() => expect(disclosure.textContent).toContain('Add recurring rule'));
    await view.user.click(within(disclosure).getByRole('button', { name: 'Add recurring rule' }));
    fireEvent.change(within(disclosure).getByRole('textbox', { name: 'Limit' }), {
      target: { value: '77' },
    });
    await waitFor(() =>
      expect(
        within(disclosure).getByRole('button', { name: 'Save recurring limits' }),
      ).toBeEnabled(),
    );
    holdKeys = true;
    fixture.setRevision('2');
    let refresh!: Promise<void>;
    act(() => {
      refresh = view.queryClient.invalidateQueries({ queryKey: charityKeys.root('admin') });
    });
    await screen.findByText('Configuration version 2', { exact: true });
    expect(disclosure).toBeInTheDocument();
    expect(disclosure.open).toBe(true);
    expect(within(disclosure).getByRole('textbox', { name: 'Limit' })).toHaveValue('77');
    expect(
      within(disclosure).getByRole('button', { name: 'Save recurring limits' }),
    ).toBeDisabled();
    await act(async () => {
      releaseKeys();
      await refresh;
    });
    expect(disclosure).toBeInTheDocument();
    expect(disclosure.open).toBe(true);
    expect(within(disclosure).getByRole('textbox', { name: 'Limit' })).toHaveValue('77');
    await waitFor(() =>
      expect(
        within(disclosure).getByRole('button', { name: 'Save recurring limits' }),
      ).toBeEnabled(),
    );
  });

  it('keeps a selected model outside the current page and search, including browser return', async () => {
    const models = Array.from({ length: 21 }, (_, index) => ({
      route_strategy: 'expiry_weighted',
      id: String(index + 1),
      provider: 'Provider',
      model: `Model${index + 1}`,
      full_name: `[公益]Provider/Model${index + 1}`,
      enabled: true,
      allowed_levels: [1, 2, 3, 4, 5],
      public_description: '',
      pricing: { mode: 'per_request', user_price: '1', donor_reward: '0' },
      discount: { enabled: false, percent: 0, start_at: null, end_at: null },
      flatten_tool_calls: false,
      revision: '1',
      binding_revision: '0',
      binding_count: '0',
      rolling_success: { sample_count: '0', success_count: '0', percent: null },
      created_at: 1,
      updated_at: 1,
    }));
    let detailReads = 0;
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>(async (input) => {
        const url = new URL(String(input), 'http://admin.test');
        if (url.pathname === '/admin/api/session')
          return json({ admin: { username: 'fixture-admin' } });
        if (url.pathname === '/admin/api/time-zones')
          return json({ version: 'go1.26.6-zoneinfo', zones: ['America/Indianapolis', 'UTC'] });
        if (url.pathname === '/admin/api/charity-models')
          return json(
            page(
              models.filter((model) => model.full_name.includes(url.searchParams.get('q') ?? '')),
              url,
            ),
          );
        if (url.pathname === '/admin/api/charity-models/1') {
          detailReads++;
          return json(models[0]);
        }
        if (url.pathname === '/admin/api/charity-models/1/bindings')
          return json({ bindings: [], binding_revision: '0' });
        if (url.pathname === '/admin/api/charity-models/1/binding-donations')
          return json({ data: [], next_cursor: null });
        if (url.pathname === '/admin/api/donation-sources') return json(page([], url));
        throw new Error(`Unexpected model request: ${url.pathname}${url.search}`);
      }),
    );
    const view = await renderWithProviders(
      <>
        <CharityPage />
        <Probe />
      </>,
      { station: 'admin', route: '/charity?charity_section=models&models_page=2&charity_model=1' },
    );
    expect(await screen.findByRole('heading', { name: '[公益]Provider/Model1' })).toBeVisible();
    expect(await screen.findByText('[公益]Provider/Model21')).toBeVisible();
    const filter = screen.getByRole('button', { name: 'Apply filter' }).closest('form')!;
    await view.user.type(within(filter).getByRole('textbox'), 'absent');
    await view.user.click(within(filter).getByRole('button', { name: 'Apply filter' }));
    expect(await screen.findByRole('heading', { name: 'No charity models' })).toBeVisible();
    expect(screen.getByRole('heading', { name: '[公益]Provider/Model1' })).toBeVisible();
    expect(detailReads).toBe(1);
    await view.user.click(screen.getByRole('button', { name: 'Return to list' }));
    expect(
      screen.queryByRole('heading', { name: '[公益]Provider/Model1' }),
    ).not.toBeInTheDocument();
    expect(screen.getByTestId('url')).toHaveTextContent('model_q=absent');
    await view.user.click(screen.getByRole('button', { name: 'Browser back' }));
    expect(await screen.findByRole('heading', { name: '[公益]Provider/Model1' })).toBeVisible();
    expect(screen.getByTestId('url')).toHaveTextContent('charity_model=1');
    expect(screen.getByTestId('url')).toHaveTextContent('model_q=absent');
  });
  it('opens an off-page source key and returns to both original page contexts', async () => {
    const { requests } = install();
    const view = await renderWithProviders(
      <>
        <CharityPage />
        <Probe />
      </>,
      {
        station: 'admin',
        route: `/charity?charity_section=sources&sources_page=2&source_key=${sourceKey(0)}&source_keys_page=2&source_scope=all`,
      },
    );
    await view.user.click(await screen.findByRole('button', { name: 'Manage key #31' }));
    await waitFor(() =>
      expect(
        requests.some(
          (url) =>
            url.pathname === '/admin/api/donations/7/keys' && url.searchParams.get('page') === '2',
        ),
      ).toBe(true),
    );
    expect(await screen.findByRole('heading', { name: 'Donation keys' })).toBeVisible();
    expect(screen.getByTestId('url')).toHaveTextContent('donation_id=7');
    expect(screen.getByTestId('url')).toHaveTextContent('donation_key=31');
    await view.user.click(screen.getByRole('button', { name: 'Return to list' }));
    expect(await screen.findByRole('button', { name: 'Manage key #31' })).toBeVisible();
    const params = new URLSearchParams(screen.getByTestId('url').textContent ?? '');
    expect(params.get('sources_page')).toBe('2');
    expect(params.get('source_keys_page')).toBe('2');
    expect(params.get('source_key')).toBe(sourceKey(0));
    expect(params.has('donation_id')).toBe(false);
  });

  it('retains review drafts on separate pages and submits every key in one revision', async () => {
    const { reviews } = install(true);
    const view = await renderWithProviders(<CharityPage />, {
      station: 'admin',
      route: '/charity?donation_id=7',
    });
    const heading = await screen.findByRole('heading', { name: 'Review pending submission' });
    const card = heading.closest('section')!;
    const firstReviewNote = () =>
      within(within(card).getAllByRole('heading', { level: 4 })[0].closest('section')!).getByRole(
        'textbox',
        { name: 'Review note' },
      );
    const firstNote = firstReviewNote();
    fireEvent.change(firstNote, { target: { value: 'first page note' } });
    await view.user.click(within(card).getByText('Next', { selector: 'button', exact: true }));
    const lastNote = within(card).getByLabelText('Review note');
    fireEvent.change(lastNote, { target: { value: 'second page note' } });
    await view.user.click(within(card).getByText('Previous', { selector: 'button', exact: true }));
    expect(firstReviewNote()).toHaveValue('first page note');
    const reviewFields = [...card.children].find((child) => child.matches('div.ops-field-grid'));
    if (!(reviewFields instanceof HTMLElement)) throw new Error('Review fields missing');
    const reason = within(reviewFields).getByLabelText('Reason');
    const confirmationLabel = [...card.children].find((child) =>
      child.matches('label.checkbox-label'),
    );
    if (!(confirmationLabel instanceof HTMLElement)) throw new Error('Review confirmation missing');
    const confirmation = within(confirmationLabel).getByRole('checkbox', {
      name: 'I confirm this review result and its per-key consequences.',
    });
    act(() => {
      fireEvent.change(reason, { target: { value: 'Reviewed all pages' } });
      fireEvent.click(confirmation);
    });
    await view.user.click(
      within(card).getByText('Approve donation', { selector: 'button', exact: true }),
    );
    await waitFor(() => expect(reviews).toHaveLength(1));
    expect(reviews[0].expected_revision).toBe('1');
    expect(reviews[0].key_settings).toHaveLength(21);
    expect(reviews[0].key_settings.find((row) => row.donation_key_id === '11')?.safe_note).toBe(
      'first page note',
    );
    expect(reviews[0].key_settings.find((row) => row.donation_key_id === '31')?.safe_note).toBe(
      'second page note',
    );
  });
});
