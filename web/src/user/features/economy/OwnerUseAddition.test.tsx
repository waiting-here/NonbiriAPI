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

describe('owner protocol resubmission', () => {
  async function setup(lost = false) {
    const writes: { body: unknown; key: string | null }[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>(async (input, init) => {
        const url = new URL(String(input), 'https://app.example');
        if (url.pathname === '/api/endpoint-create-options')
          return json({
            base_connector_types: ['openai-compatible'],
            mainstream_channels: [],
            native_service_options: [
              {
                connector_type: 'openai-compatible',
                configuration_type: 'openai-compatible',
                native_services: ['chat_completions', 'responses', 'embeddings'],
                default_native_services: ['chat_completions'],
                rerank_formats: [],
              },
            ],
          });
        if (url.pathname === '/api/donations/3/uses' && init?.method === 'POST') {
          writes.push({
            body: JSON.parse(String(init.body)),
            key: new Headers(init.headers).get('Idempotency-Key'),
          });
          if (lost && writes.length === 1) throw new TypeError('Response lost');
          return json(
            {
              donation_revision: '3',
              uses: [
                { id: '2', revision: '2', native_service: 'chat_completions' },
                { id: '6', revision: '10', native_service: 'responses' },
                { id: '7', revision: '1', native_service: 'embeddings' },
              ],
            },
            201,
          );
        }
        if (url.pathname === '/api/donations/3/uses')
          return json(
            page([row('2', 'chat_completions', 'approved'), row('6', 'responses', 'pending')]),
          );
        throw Error('Unexpected ' + url.pathname);
      }),
    );
    const view = await renderWithProviders(
      <OwnerUseAddition accountId="1" donationId="3" donationRevision="2" donationKey={member} />,
      { station: 'user', role: 'user' },
    );
    view.queryClient.setQueryData(['user', 'session'], {
      user: { id: '1', username: 'owner', effective_level: 1 },
    });
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Submit update' })).toBeEnabled(),
    );
    return { ...view, writes };
  }
  it('submits all protocol services including approved and already pending services without selection', async () => {
    const view = await setup();
    expect(screen.queryByRole('checkbox')).not.toBeInTheDocument();
    await view.user.click(screen.getByRole('button', { name: 'Submit update' }));
    await screen.findByText('Update submitted for review.');
    expect(view.writes).toHaveLength(1);
    expect(view.writes[0]?.body).toEqual({
      expected_revision: '2',
      donation_key_id: '4',
      credential_id: '8',
      credential_version: '3',
      grant_revision: '5',
      items: [
        { native_service: 'chat_completions', expected_revision: '1' },
        { native_service: 'responses', expected_revision: '9' },
        { native_service: 'embeddings', expected_revision: null },
      ],
      discovery_source_id: null,
      discovery_source_revision: null,
    });
  });
  it('replays an uncertain resubmission with its original complete body and operation key', async () => {
    const view = await setup(true);
    await view.user.click(screen.getByRole('button', { name: 'Submit update' }));
    await screen.findByText('Update submitted for review.');
    expect(view.writes).toHaveLength(2);
    expect(view.writes[0]).toEqual(view.writes[1]);
    expect(view.writes[0]?.key).toBeTruthy();
  });
});
