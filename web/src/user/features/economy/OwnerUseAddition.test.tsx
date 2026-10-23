import { screen, waitFor } from '@testing-library/react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderWithProviders } from '../../../../test/unit/support';
import { OwnerUseAddition } from './OwnerUseAddition';
import type { DonationKey } from './types';
import type { EndpointKey } from '../core/types';
const mocks = vi.hoisted(() => ({ read: vi.fn() }));
vi.mock('../core/resourceOperation', async (original) => ({
  ...(await original<typeof import('../core/resourceOperation')>()),
  readEndpointKey: mocks.read,
}));
const grant: EndpointKey = {
  id: '5',
  endpoint_id: '11',
  display_head: 'sk-a',
  display_tail: 'tail',
  note: 'Entry',
  enabled: true,
  credential_available: true,
  force_store_false: false,
  max_concurrency: 1,
  max_rpm: 60,
  suspension_state: 'none',
  revision: '2',
  created_at: 1,
  updated_at: 2,
  credential_id: '8',
  credential_version: '3',
  grant_revision: '5',
  native_services: ['chat_completions', 'responses', 'embeddings'],
  discovery_source_id: '51',
};
const member: DonationKey = {
  id: '4',
  endpointId: '11',
  endpointKeyId: '5',
  displayHead: 'sk-a',
  displayTail: 'tail',
  source: {
    kind: 'custom',
    baseUrl: 'https://upstream.example',
    connectorType: 'openai-compatible',
  },
  physicalEnabled: true,
  charityState: 'disabled',
  limits: { price: '100', calls: '20', tokens: null },
  usage: {
    priceUsed: '2',
    priceInflight: '0',
    callsUsed: '1',
    callsInflight: '0',
    tokensUsed: '0',
    tokensInflight: '0',
  },
  tokenReserve: 32,
  failureDisableThreshold: '10',
  smartBackoff: null,
  streak: { generation: '1', count: '0', failureDisabled: false },
  expiresAt: null,
  endedReason: null,
  rateLimitedUntil: null,
};
const source = {
  id: '51',
  connection_id: '2',
  name: 'Catalog',
  base_url: 'https://catalog.example',
  models_url: 'https://catalog.example/models',
  list_format: 'openai',
  credential_id: '8',
  credential_version: '3',
  revision: '3',
  discovery_source_revision: '7',
  enabled: true,
  credential_available: true,
  state: 'succeeded',
  checked_at: 1,
  safe_class: 'none',
  safe_diag: '',
  created_at: 1,
  updated_at: 2,
};
const version = (status: 'approved' | 'rejected' | 'pending') => ({
  credential_id: '8',
  credential_version: '3',
  grant_revision: '5',
  status,
  review_note: '',
  reviewed_at: null,
  expected_review_revision: 'review-1',
  discovery_source_id: null,
  discovery_source_revision: null,
  display_head: 'sk-a',
  display_tail: 'tail',
  review: { required: false, revision: null, material_available: false },
  discovery: null,
});
const row = (id: string, service: string, status: 'approved' | 'rejected' | 'pending') => ({
  id,
  donation_id: '3',
  donation_key_id: '4',
  endpoint_key_id: '5',
  native_service: service,
  enabled: false,
  discovery_enabled: false,
  revision: id === '6' ? '9' : '1',
  approved: status === 'approved' ? version(status) : null,
  pending: status === 'pending' ? version(status) : null,
  latest: version(status),
  health: null,
  capabilities: {
    can_review: false,
    can_manage: false,
    can_reset_failures: false,
    can_discover: false,
    can_bind: false,
    unavailable_reason: null,
  },
});
const json = (value: unknown, status = 200) =>
  new Response(JSON.stringify(value), { status, headers: { 'content-type': 'application/json' } });
const page = (data: unknown[]) => ({
  data,
  next_cursor: null,
  pagination: { page: '1', page_size: 20, total_items: String(data.length), total_pages: '1' },
});
beforeEach(() => {
  mocks.read.mockReset();
  mocks.read.mockResolvedValue(grant);
});

describe('owner explicit service additions', () => {
  it('defaults mainstream additions only to available services and lets the owner cancel each default', async () => {
    const writes: Record<string, unknown>[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>(async (input, init) => {
        const url = new URL(String(input), 'https://app.example');
        if (url.pathname === '/api/discovery-sources/51') return json(source);
        if (url.pathname === '/api/donations/3/uses' && init?.method === 'POST') {
          writes.push(JSON.parse(String(init.body)));
          return json(
            {
              donation_revision: '3',
              uses: [{ id: '6', revision: '10', native_service: 'responses' }],
            },
            201,
          );
        }
        if (url.pathname === '/api/donations/3/uses')
          return json(
            page([row('2', 'chat_completions', 'approved'), row('6', 'responses', 'rejected')]),
          );
        throw Error('Unexpected ' + url.pathname);
      }),
    );
    const view = await renderWithProviders(
      <OwnerUseAddition
        accountId="1"
        donationId="3"
        donationRevision="2"
        donationKey={{
          ...member,
          source: {
            ...member.source,
            kind: 'mainstream',
            channelId: 'mch_abcdefghijklmnopqrstuA',
            name: 'Main channel',
          },
        }}
      />,
      { station: 'user', role: 'user', route: '/charity' },
    );
    view.queryClient.setQueryData(['user', 'session'], {
      user: { id: '1', username: 'owner', effective_level: 1 },
    });
    expect(await screen.findByRole('checkbox', { name: 'Responses' })).toBeChecked();
    expect(screen.getByRole('checkbox', { name: 'Embeddings' })).toBeChecked();
    expect(screen.queryByRole('checkbox', { name: 'Chat completions' })).toBeNull();
    await waitFor(() =>
      expect(screen.getByRole('checkbox', { name: 'Share this catalog' })).toBeChecked(),
    );
    await view.user.click(screen.getByRole('checkbox', { name: 'Embeddings' }));
    await view.user.click(screen.getByRole('checkbox', { name: 'Share this catalog' }));
    await view.queryClient.invalidateQueries({ queryKey: ['user'] });
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Submit services for review' })).toBeEnabled(),
    );
    expect(screen.getByRole('checkbox', { name: 'Embeddings' })).not.toBeChecked();
    expect(screen.getByRole('checkbox', { name: 'Share this catalog' })).not.toBeChecked();
    await view.user.click(screen.getByRole('button', { name: 'Submit services for review' }));
    await waitFor(() => expect(writes).toHaveLength(1));
    expect(writes[0]).toMatchObject({
      items: [{ native_service: 'responses', expected_revision: '9' }],
      discovery_source_id: null,
      discovery_source_revision: null,
    });
  });

  it('selects no services by default, resubmits only inactive uses, and replays an uncertain write with its original body and key', async () => {
    let committed = false;
    const writes: { body: unknown; key: string | null }[] = [];
    const fetchMock = vi.fn<typeof fetch>(async (input, init) => {
      const url = new URL(String(input), 'https://app.example');
      if (url.pathname === '/api/discovery-sources/51') return json(source);
      if (url.pathname === '/api/donations/3/uses' && (init?.method ?? 'GET') === 'POST') {
        writes.push({
          body: JSON.parse(String(init?.body)),
          key: new Headers(init?.headers).get('Idempotency-Key'),
        });
        committed = true;
        if (writes.length === 1) throw new TypeError('Response lost');
        return json(
          {
            donation_revision: '3',
            uses: [
              { id: '6', revision: '10', native_service: 'responses' },
              { id: '7', revision: '1', native_service: 'embeddings' },
            ],
          },
          201,
        );
      }
      if (url.pathname === '/api/donations/3/uses') {
        expect(url.searchParams.get('donation_key_id')).toBe('4');
        expect(url.searchParams.get('state')).toBeNull();
        return json(
          page([
            row('2', 'chat_completions', 'approved'),
            row('6', 'responses', committed ? 'pending' : 'rejected'),
            ...(committed ? [row('7', 'embeddings', 'pending')] : []),
          ]),
        );
      }
      throw Error('Unexpected ' + url.pathname);
    });
    vi.stubGlobal('fetch', fetchMock);
    const view = await renderWithProviders(
      <OwnerUseAddition accountId="1" donationId="3" donationRevision="2" donationKey={member} />,
      { station: 'user', role: 'user', route: '/charity?donations_page=2&uses_state=approved' },
    );
    view.queryClient.setQueryData(['user', 'session'], {
      user: { id: '1', username: 'owner', effective_level: 1 },
    });
    const responses = await screen.findByRole('checkbox', { name: 'Responses' });
    expect(responses).not.toBeChecked();
    expect(screen.getByRole('checkbox', { name: 'Embeddings' })).not.toBeChecked();
    expect(screen.queryByRole('checkbox', { name: 'Chat completions' })).toBeNull();
    expect(screen.getByRole('button', { name: 'Submit services for review' })).toBeDisabled();
    await view.user.click(responses);
    await view.user.click(screen.getByRole('checkbox', { name: 'Embeddings' }));
    await view.user.click(screen.getByRole('checkbox', { name: 'Share this catalog' }));
    await view.user.click(screen.getByRole('button', { name: 'Submit services for review' }));
    await screen.findByText('Services submitted for review.');
    expect(writes).toHaveLength(2);
    expect(writes[0]).toEqual(writes[1]);
    expect(writes[0]?.key).toBeTruthy();
    expect(writes[0]?.body).toEqual({
      expected_revision: '2',
      donation_key_id: '4',
      credential_id: '8',
      credential_version: '3',
      grant_revision: '5',
      items: [
        { native_service: 'responses', expected_revision: '9' },
        { native_service: 'embeddings', expected_revision: null },
      ],
      discovery_source_id: '51',
      discovery_source_revision: '7',
    });
    expect(mocks.read).toHaveBeenCalledWith('11', '5', expect.any(AbortSignal));
    await screen.findByRole('heading', { name: 'Donation uses by service' });
    expect(screen.getByText('Chat completions', { exact: true })).toBeVisible();
    expect(
      fetchMock.mock.calls
        .filter(([url]) => String(url).includes('/api/donations/3/uses'))
        .every(
          ([url]) =>
            String(url).includes('donation_key_id=4') || String(url) === '/api/donations/3/uses',
        ),
    ).toBe(true);
  });
  it('keeps explicit service choices on a grant conflict and requires adoption before a new intent, clearing the old source share', async () => {
    let current = grant;
    const writes: { body: unknown; key: string | null }[] = [];
    mocks.read.mockImplementation(async () => current);
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>(async (input, init) => {
        const url = new URL(String(input), 'https://app.example');
        if (url.pathname === '/api/discovery-sources/51') return json(source);
        if (url.pathname === '/api/donations/3/uses' && init?.method === 'POST') {
          writes.push({
            body: JSON.parse(String(init.body)),
            key: new Headers(init.headers).get('Idempotency-Key'),
          });
          if (writes.length === 1) {
            current = { ...grant, credential_version: '4', grant_revision: '6' };
            return json({ error: { code: 'conflict', message: 'Credential changed' } }, 409);
          }
          return json(
            {
              donation_revision: '3',
              uses: [{ id: '6', revision: '10', native_service: 'responses' }],
            },
            201,
          );
        }
        if (url.pathname === '/api/donations/3/uses')
          return json(page([row('6', 'responses', 'rejected')]));
        throw Error('Unexpected ' + url.pathname);
      }),
    );
    const view = await renderWithProviders(
      <OwnerUseAddition accountId="1" donationId="3" donationRevision="2" donationKey={member} />,
      { station: 'user', role: 'user', route: '/charity' },
    );
    view.queryClient.setQueryData(['user', 'session'], {
      user: { id: '1', username: 'owner', effective_level: 1 },
    });
    await view.user.click(await screen.findByRole('checkbox', { name: 'Responses' }));
    await view.user.click(screen.getByRole('checkbox', { name: 'Share this catalog' }));
    await view.user.click(screen.getByRole('button', { name: 'Submit services for review' }));
    await screen.findByText(/Credential changed/);
    await waitFor(() => expect(mocks.read).toHaveBeenCalledTimes(2));
    expect(screen.getByRole('checkbox', { name: 'Responses' })).toBeChecked();
    expect(screen.getByRole('button', { name: 'Submit services for review' })).toBeDisabled();
    await view.user.click(screen.getByRole('button', { name: 'Continue with current revision' }));
    expect(screen.getByRole('checkbox', { name: 'Share this catalog' })).not.toBeChecked();
    await view.user.click(screen.getByRole('button', { name: 'Submit services for review' }));
    await screen.findByText('Services submitted for review.');
    expect(writes[1]?.key).not.toBe(writes[0]?.key);
    expect(writes[1]?.body).toMatchObject({
      credential_version: '4',
      grant_revision: '6',
      discovery_source_id: null,
      discovery_source_revision: null,
      items: [{ native_service: 'responses', expected_revision: '9' }],
    });
  });
});
