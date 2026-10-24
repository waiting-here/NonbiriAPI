import { screen } from '@testing-library/react';
import { Link, Route, Routes, useLocation } from 'react-router';
import { expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../test/unit/support';
import type { CharityPickerKeyManagerProps } from '../CharityBindingPicker';
import { useCharityModelScope } from '../charityModelScopeContext';
import { ModelsPanel } from './CharityModels';

const model = {
  recurring_discount: { enabled: false, time_zone: 'UTC', default_percent: 100, periods: [] },
  effective_discount: { source: 'none', percent: 100, next_change_at: null },
  server_now: 1800000000,

  model_types: ['chat_completions'],
  route_strategy: 'expiry_weighted',
  id: '31',
  provider: 'Provider',
  model: 'model',
  full_name: '[公益]Provider/model',
  enabled: true,
  allowed_levels: [1, 2, 3, 4, 5],
  public_description: '',
  pricing: { mode: 'per_request', user_price: '1', donor_reward: '0' },
  discount: { enabled: false, percent: 0, start_at: null, end_at: null },
  transport_rule: 'passthrough',
  cloudflare_proxy_enabled: false,
  cloudflare_proxy_mode: 'direct',
  flatten_tool_calls: false,
  revision: '1',
  binding_revision: '0',
  binding_count: '0',
  rolling_success: { sample_count: '0', success_count: '0', percent: null },
  created_at: 1,
  updated_at: 1,
};
const returnTo =
  '/steward?tab=logs&charity_model=historical&page=2&page_size=20&request_id=req_AAAAAAAAAAAAAAAAAAAAAQ&attempt_page=2&attempt_page_size=10';
const target = '/charity?charity_section=models&charity_model=31&managed_donation=7&managed_key=11';

function KeyManager({ donationID, keyID, onBack, backLabel }: CharityPickerKeyManagerProps) {
  const scope = useCharityModelScope();
  return (
    <>
      <output>
        model={scope};donation={donationID};key={keyID}
      </output>
      <button onClick={onBack}>{backLabel}</button>
    </>
  );
}
function Probe() {
  return <output data-testid="location">{useLocation().pathname + useLocation().search}</output>;
}

it('opens a direct trainee key in model scope and returns to the full log window', async () => {
  const fetchMock = vi.fn(
    async () =>
      new Response(JSON.stringify(model), { headers: { 'Content-Type': 'application/json' } }),
  );
  vi.stubGlobal('fetch', fetchMock);
  const view = await renderWithProviders(
    <>
      <Probe />
      <Routes>
        <Route
          path="/steward"
          element={
            <Link to={target} state={{ logReturnTo: returnTo }}>
              Open key
            </Link>
          }
        />
        <Route
          path="/charity"
          element={
            <ModelsPanel
              role="steward"
              accountId="trainee"
              renderKeyManager={(props) => <KeyManager {...props} />}
              renderScopedKeys={() => null}
            />
          }
        />
      </Routes>
    </>,
    { station: 'user', role: 'level5', route: returnTo },
  );
  await view.user.click(screen.getByRole('link', { name: 'Open key' }));
  expect(await screen.findByText('model=31;donation=7;key=11')).toBeVisible();
  expect(fetchMock).toHaveBeenCalledWith('/api/steward/charity-models/31', expect.anything());
  await view.user.click(screen.getByRole('button', { name: 'Return to request logs' }));
  expect(screen.getByTestId('location')).toHaveTextContent(returnTo);
});

it.each([403, 404])(
  'retains log return and retry when the direct model returns %s',
  async (status) => {
    const loss = vi.fn();
    vi.stubGlobal(
      'fetch',
      vi.fn(
        async () =>
          new Response(
            JSON.stringify({
              error: { code: status === 403 ? 'forbidden' : 'not_found', message: 'Unavailable.' },
            }),
            { status, headers: { 'Content-Type': 'application/json' } },
          ),
      ),
    );
    const view = await renderWithProviders(
      <Routes>
        <Route
          path="/steward"
          element={
            <Link to={target} state={{ logReturnTo: returnTo }}>
              Open key
            </Link>
          }
        />
        <Route
          path="/charity"
          element={
            <ModelsPanel
              role="steward"
              accountId="trainee"
              onCapabilityLoss={loss}
              renderKeyManager={() => null}
              renderScopedKeys={() => null}
            />
          }
        />
      </Routes>,
      { station: 'user', role: 'level5', route: returnTo },
    );
    await view.user.click(screen.getByRole('link', { name: 'Open key' }));
    expect(await screen.findByRole('alert')).toBeVisible();
    expect(screen.getByRole('link', { name: 'Return to request logs' })).toHaveAttribute(
      'href',
      returnTo,
    );
    expect(screen.getByRole('button', { name: 'Retry' })).toBeVisible();
    expect(loss).not.toHaveBeenCalled();
  },
);
