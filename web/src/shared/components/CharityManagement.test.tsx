import { screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import type { AdminDonation, CharityModel, ManagedDonationKey } from '@shared/operations/charity';
import { CharityManagement } from './CharityManagement';

function jsonResponse(value: unknown): Response {
  return new Response(JSON.stringify(value), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  });
}

const managedKey = (overrides: Partial<ManagedDonationKey> = {}): ManagedDonationKey => ({
  id: '11',
  endpoint_key_id: '21',
  display_head: 'head',
  display_tail: 'tail',
  safe_source: {
    kind: 'custom',
    base_url: 'https://example.test/v1',
    connector_type: 'openai-compatible',
  },
  physical_enabled: true,
  charity_state: 'available',
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
  streak: { generation: '1', count: '0', failure_disabled: false },
  ended_reason: null,
  safe_note: 'safe note',
  ...overrides,
});

function approvedDonation(key = managedKey()): AdminDonation {
  return {
    id: '1',
    status: 'approved',
    revision: '1',
    description: 'Donation description',
    review_result: { decision: 'approve', reason: 'accepted', reviewed_at: 10 },
    keys: [key],
    owner: { user_id: '7', discord_id: null, display_name: 'Donor' },
    reviewer: { user_id: '9', role: 'admin' },
    created_at: 1,
    updated_at: 10,
  };
}

function pendingDonation(): AdminDonation {
  return {
    ...approvedDonation(managedKey({ charity_state: 'pending' })),
    status: 'pending',
    review_result: null,
    reviewer: null,
    updated_at: 1,
  };
}

function installDonationFetch(initial: AdminDonation) {
  let current = initial;
  const keyBodies: Record<string, unknown>[] = [];
  const reviewBodies: Record<string, unknown>[] = [];
  const idempotencyKeys: string[] = [];
  const fetchMock = vi.fn<typeof fetch>(async (input, init) => {
    const path = String(input);
    const method = init?.method ?? 'GET';
    if (method === 'GET' && path.startsWith('/admin/api/donations?')) {
      return jsonResponse({ data: [current], next_cursor: null });
    }
    if (method === 'GET' && path === '/admin/api/donations/1') return jsonResponse(current);
    if (method === 'PATCH' && path === '/admin/api/donations/1/keys/11') {
      const body = JSON.parse(String(init?.body)) as Record<string, unknown>;
      keyBodies.push(body);
      idempotencyKeys.push(new Headers(init?.headers).get('Idempotency-Key') ?? '');
      current = { ...current, revision: String(Number(current.revision) + 1), updated_at: 11 };
      return jsonResponse(current);
    }
    if (method === 'POST' && path === '/admin/api/donations/1/review') {
      const body = JSON.parse(String(init?.body)) as Record<string, unknown>;
      reviewBodies.push(body);
      idempotencyKeys.push(new Headers(init?.headers).get('Idempotency-Key') ?? '');
      const approved = body.decision === 'approve';
      current = {
        ...current,
        status: approved ? 'approved' : 'rejected',
        revision: String(Number(current.revision) + 1),
        review_result: {
          decision: approved ? 'approve' : 'reject',
          reason: String(body.reason),
          reviewed_at: 11,
        },
        reviewer: { user_id: '9', role: 'admin' },
        keys: current.keys.map((key) => ({
          ...key,
          charity_state: approved ? 'available' : key.charity_state,
          expires_at: approved
            ? ((body.key_settings as { expires_at: number | null }[])[0]?.expires_at ?? null)
            : key.expires_at,
        })),
        updated_at: 11,
      };
      return jsonResponse(current);
    }
    throw new Error(`Unexpected request: ${method} ${path}`);
  });
  vi.stubGlobal('fetch', fetchMock);
  return { keyBodies, reviewBodies, idempotencyKeys };
}

const operationKeyPattern = /^[A-Za-z0-9_-]{22,128}$/;

const charityModel = (start: number, end: number): CharityModel => ({
  id: '1',
  provider: 'provider',
  model: 'model',
  full_name: '[公益]provider/model',
  enabled: true,
  pricing: { mode: 'per_request', user_price: '1', donor_reward: '0' },
  discount: { enabled: true, percent: 10, start_at: start, end_at: end },
  flatten_tool_calls: false,
  revision: '1',
  binding_revision: '0',
  binding_count: '0',
  rolling_success: { sample_count: '0', success_count: '0', percent: null },
  created_at: 1,
  updated_at: 1,
});

const datePart = (value: number) => String(value).padStart(2, '0');
function localDateTime(epoch: number): string {
  const date = new Date(epoch * 1_000);
  return `${date.getFullYear()}-${datePart(date.getMonth() + 1)}-${datePart(date.getDate())}T${datePart(date.getHours())}:${datePart(date.getMinutes())}:${datePart(date.getSeconds())}`;
}

describe('CharityManagement corrective controls', () => {
  it('omits enabled when reset is the only switch-related operator action', async () => {
    const fixture = approvedDonation(
      managedKey({
        charity_state: 'disabled',
        streak: { generation: '2', count: '10', failure_disabled: true },
      }),
    );
    const requests = installDonationFetch(fixture);
    const view = await renderWithProviders(<CharityManagement frame="admin" />, {
      station: 'admin',
      role: 'admin',
    });

    await view.user.click(await screen.findByRole('button', { name: 'Review' }));
    await screen.findByRole('heading', { name: 'Donation #1' });
    expect(screen.getByLabelText('Charity switch change')).toHaveValue('');
    await view.user.click(screen.getByRole('checkbox', { name: 'Reset failure streak' }));
    await view.user.click(screen.getByRole('button', { name: 'Save key limits' }));

    await waitFor(() => expect(requests.keyBodies).toHaveLength(1));
    expect(requests.keyBodies[0]).toEqual({
      expected_revision: '1',
      price_limit: null,
      calls_limit: null,
      tokens_limit: null,
      token_reserve: 32,
      safe_note: 'safe note',
      expires_at: null,
      reset_failure_streak: true,
    });
    expect(requests.keyBodies[0]).not.toHaveProperty('enabled');
    expect(requests.idempotencyKeys).toEqual([expect.stringMatching(operationKeyPattern)]);
  });

  it('sends enabled after an explicit operator switch choice', async () => {
    const requests = installDonationFetch(approvedDonation());
    const view = await renderWithProviders(<CharityManagement frame="admin" />, {
      station: 'admin',
      role: 'admin',
    });

    await view.user.click(await screen.findByRole('button', { name: 'Review' }));
    await screen.findByRole('heading', { name: 'Donation #1' });
    await view.user.selectOptions(screen.getByLabelText('Charity switch change'), 'false');
    await view.user.click(screen.getByRole('button', { name: 'Save key limits' }));

    await waitFor(() => expect(requests.keyBodies).toHaveLength(1));
    expect(requests.keyBodies[0]).toEqual({
      expected_revision: '1',
      enabled: false,
      price_limit: null,
      calls_limit: null,
      tokens_limit: null,
      token_reserve: 32,
      safe_note: 'safe note',
      expires_at: null,
    });
    expect(requests.idempotencyKeys).toEqual([expect.stringMatching(operationKeyPattern)]);
  });

  it('submits an explicit per-key null expiry for a pending donation', async () => {
    const requests = installDonationFetch(pendingDonation());
    const view = await renderWithProviders(<CharityManagement frame="admin" />, {
      station: 'admin',
      role: 'admin',
    });

    await view.user.click(await screen.findByRole('button', { name: 'Review' }));
    await screen.findByRole('heading', { name: 'Review pending submission' });
    expect(screen.getByRole('checkbox', { name: 'No expiry' })).toBeChecked();
    expect(screen.getByLabelText('Effective expiry')).toBeDisabled();
    await view.user.type(screen.getByLabelText('Reason'), 'approved without expiry');
    await view.user.click(
      screen.getByRole('checkbox', {
        name: 'I confirm this review result and its per-key consequences.',
      }),
    );
    await view.user.click(screen.getByRole('button', { name: 'Approve donation' }));

    await waitFor(() => expect(requests.reviewBodies).toHaveLength(1));
    expect(requests.reviewBodies[0]).toEqual({
      decision: 'approve',
      expected_revision: '1',
      reason: 'approved without expiry',
      key_settings: [
        {
          donation_key_id: '11',
          price_limit: null,
          calls_limit: null,
          tokens_limit: null,
          token_reserve: 32,
          enabled: true,
          safe_note: 'safe note',
          expires_at: null,
        },
      ],
    });
    expect(requests.idempotencyKeys).toEqual([expect.stringMatching(operationKeyPattern)]);
  });

  it('renders local discount seconds and preserves untouched epochs', async () => {
    const start = 1_735_689_845;
    const end = 1_735_693_507;
    let current = charityModel(start, end);
    const patchBodies: Record<string, unknown>[] = [];
    const idempotencyKeys: string[] = [];
    const fetchMock = vi.fn<typeof fetch>(async (input, init) => {
      const path = String(input);
      const method = init?.method ?? 'GET';
      if (method === 'GET' && path.startsWith('/admin/api/donations?')) {
        return jsonResponse({ data: [], next_cursor: null });
      }
      if (method === 'GET' && path.startsWith('/admin/api/charity-models?')) {
        return jsonResponse({ data: [current], next_cursor: null });
      }
      if (method === 'GET' && path === '/admin/api/charity-models/1/bindings') {
        return jsonResponse({ bindings: [], binding_revision: '0' });
      }
      if (
        method === 'GET' &&
        path.startsWith('/admin/api/charity-models/1/binding-candidates?')
      ) {
        return jsonResponse({ data: [], next_cursor: null });
      }
      if (method === 'PATCH' && path === '/admin/api/charity-models/1') {
        const body = JSON.parse(String(init?.body)) as Record<string, unknown>;
        patchBodies.push(body);
        idempotencyKeys.push(new Headers(init?.headers).get('Idempotency-Key') ?? '');
        current = {
          ...current,
          revision: '2',
          discount: body.discount as CharityModel['discount'],
          updated_at: 2,
        };
        return jsonResponse(current);
      }
      throw new Error(`Unexpected request: ${method} ${path}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const view = await renderWithProviders(<CharityManagement frame="admin" />, {
      station: 'admin',
      role: 'admin',
    });

    await view.user.click(screen.getByRole('tab', { name: 'Charity models and bindings' }));
    await view.user.click(await screen.findByRole('button', { name: 'Manage' }));
    const heading = await screen.findByRole('heading', { name: '[公益]provider/model' });
    const card = heading.closest('.card');
    if (!(card instanceof HTMLElement)) throw new Error('Expected model editor card.');
    const editor = within(card);
    expect(editor.getByLabelText('Start (optional)')).toHaveValue(`${localDateTime(start)}.000`);
    expect(editor.getByLabelText('End (optional)')).toHaveValue(`${localDateTime(end)}.000`);

    await view.user.click(editor.getByRole('button', { name: 'Save model' }));

    await waitFor(() => expect(patchBodies).toHaveLength(1));
    expect(patchBodies[0]).toEqual({
      expected_revision: '1',
      provider: 'provider',
      model: 'model',
      enabled: true,
      pricing: { mode: 'per_request', user_price: '1', donor_reward: '0' },
      discount: { enabled: true, percent: 10, start_at: start, end_at: end },
      flatten_tool_calls: false,
    });
    expect(patchBodies[0]).not.toHaveProperty('pricing_mode');
    expect(patchBodies[0]).not.toHaveProperty('prices');
    expect(idempotencyKeys).toEqual([expect.stringMatching(operationKeyPattern)]);
  });
});
