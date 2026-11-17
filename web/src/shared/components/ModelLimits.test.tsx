import { fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { useAdminSession } from '../../admin/data';
import { renderWithProviders } from '../../../test/unit/support';
import { ModelLimits } from './ModelLimits';

const model = 'upstream/model';
const legacyLimits = { tokens: '90', input_tokens: null, output_tokens: '50' };
const legacyUsage = { tokens: '9', input_tokens: '4', output_tokens: '5' };
const amountLimit = '340282366920938463463374607431768211.455';

function modelView(legacy: boolean) {
  const costs = { credits: '12.345', calls: '2' };
  return {
    upstream_model_id: model,
    limits: { ...legacyLimits, ...(legacy ? {} : { credits: amountLimit, calls: null }) },
    remaining: { ...legacyLimits, ...(legacy ? {} : { credits: '0.001', calls: null }) },
    used: { ...legacyUsage, ...(legacy ? {} : costs) },
    reserved: {
      tokens: '0',
      input_tokens: '0',
      output_tokens: '0',
      ...(legacy ? {} : { credits: '0', calls: '0' }),
    },
    ...(legacy ? {} : { historical_costs_partial: true }),
    unattributed_total_tokens: '0',
    tracking_started_at: 1_800_000_000,
  };
}

function AdminQuotaEditor() {
  const session = useAdminSession();
  return session.data ? (
    <ModelLimits role="admin" donationId="7" keyId="8" accountId="fixture-admin" />
  ) : null;
}

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { 'content-type': 'application/json' } });

async function editor(
  legacy: boolean,
  write: (body: Record<string, unknown>, key: string) => Response,
) {
  let revision = 1;
  const view = modelView(legacy);
  const requests: { body: Record<string, unknown>; key: string }[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn<typeof fetch>(async (input, init) => {
      const url = new URL(String(input), 'https://example.test');
      if (url.pathname === '/admin/api/session')
        return json({ admin: { username: 'fixture-admin' } });
      if (url.pathname !== '/admin/api/donations/7/keys/8/model-limits')
        throw new Error('Unexpected request ' + url.pathname);
      if (init?.method === 'PUT') {
        const body = JSON.parse(String(init.body)) as Record<string, unknown>;
        const key = new Headers(init.headers).get('Idempotency-Key')!;
        requests.push({ body, key });
        const response = write(body, key);
        if (response.status === 409) view.limits.tokens = '80';
        revision++;
        return response;
      }
      return json({
        donation_id: '7',
        key_id: '8',
        donation_revision: String(revision),
        data: [view],
        next_cursor: null,
        pagination: { page: '1', page_size: 10, total_items: '1', total_pages: '1' },
      });
    }),
  );
  const { user } = await renderWithProviders(<AdminQuotaEditor />, {
    station: 'admin',
    role: 'admin',
  });
  await user.click(await screen.findByText('Per-model quotas', { exact: true }));
  await user.click(await screen.findByRole('button', { name: 'Configure' }));
  const save = screen.getByRole('button', { name: 'Save model quotas' });
  await waitFor(() => expect(screen.getByLabelText('Total tokens')).toBeEnabled());
  return { user, requests, save };
}

afterEach(() => vi.unstubAllGlobals());

it('edits a legacy token budget without synthesizing credit or call limits and replays an unknown save', async () => {
  let attempt = 0;
  const { user, requests, save } = await editor(true, () =>
    ++attempt === 1
      ? json({}, 503)
      : json({
          donation_id: '7',
          key_id: '8',
          donation_revision: '2',
        }),
  );
  expect(screen.queryByLabelText('Credits')).not.toBeInTheDocument();
  const total = screen.getByLabelText('Total tokens');
  fireEvent.change(total, { target: { value: '0' } });
  await user.click(save);
  await user.click(await screen.findByRole('button', { name: 'Retry the same save' }));
  await screen.findByText('Model quotas saved.');
  expect(requests).toHaveLength(2);
  expect(requests[1]).toEqual(requests[0]);
  expect(requests[0]?.key).toBeTruthy();
  expect(requests[0]?.body).toEqual({
    ...legacyLimits,
    tokens: '0',
    expected_revision: '1',
    upstream_model_id: model,
  });
});

it('preserves the edited field, adopts unrelated concurrent edits, and clears a credit limit explicitly', async () => {
  let attempt = 0;
  const { user, requests, save } = await editor(false, () =>
    ++attempt === 1
      ? json({ error: { code: 'conflict' } }, 409)
      : json({
          donation_id: '7',
          key_id: '8',
          donation_revision: '3',
        }),
  );
  const credit = screen.getByLabelText('Credits');
  expect(credit).toHaveValue(amountLimit);
  expect(screen.getByText(/records still available/)).toBeVisible();
  await user.clear(credit);
  await user.click(save);
  const reapply = await screen.findByRole('button', {
    name: 'Apply my edits to the latest settings',
  });
  await waitFor(() => expect(reapply).toBeEnabled());
  await user.click(reapply);
  await waitFor(() => expect(save).toBeEnabled());
  expect(screen.getByLabelText('Total tokens')).toHaveValue('80');
  expect(credit).toHaveValue('');
  await user.click(save);
  await screen.findByText('Model quotas saved.');
  expect(requests[1]?.body).toEqual({
    ...legacyLimits,
    tokens: '80',
    credits: null,
    calls: null,
    upstream_model_id: model,
    expected_revision: '2',
  });
});
