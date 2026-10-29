import { screen, waitFor, within } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import common from '@shared/i18n/common/en.json';
import { DisplayTimeContext } from '@shared/components/timeContextValue';
import { CodexConnection } from './CodexConnection';
import { BindingEditor } from './DriverBindings';
import { newProfile, type DriverConfiguration } from './driversApi';
import type { CodexStatus } from './codexApi';

const mocks = vi.hoisted(() => ({
  get: vi.fn(),
  begin: vi.fn(),
  cancel: vi.fn(),
  disconnect: vi.fn(),
}));
vi.mock('./codexApi', () => ({
  getCodex: mocks.get,
  beginCodex: mocks.begin,
  cancelCodex: mocks.cancel,
  disconnectCodex: mocks.disconnect,
}));
const text = common.common.agent.codex;
const disconnected: CodexStatus = {
  connected: false,
  revision: '0',
  account_id: '',
  account_label: '',
  expires_at: 0,
};
beforeEach(() => {
  vi.clearAllMocks();
  mocks.get.mockResolvedValue(disconnected);
});
async function mount() {
  const view = await renderWithProviders(
    <DisplayTimeContext.Provider value={{ mode: 'site', offset_minutes: 0 }}>
      <CodexConnection account="synthetic" active />
    </DisplayTimeContext.Provider>,
    { station: 'admin', role: 'admin' },
  );
  view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'synthetic' } });
  await screen.findByText(text.disconnected);
  return view;
}
describe('Codex connection', () => {
  it('starts device login on the official page, then removes the code on cancellation', async () => {
    const pending: CodexStatus = {
      ...disconnected,
      login: { id: 'attempt', state: 'pending', user_code: 'CODE-FIXTURE', expires_at: 1800000000 },
    };
    mocks.begin.mockImplementation(async () => {
      mocks.get.mockResolvedValue(pending);
      return pending;
    });
    mocks.cancel.mockImplementation(async () => {
      mocks.get.mockResolvedValue(disconnected);
      return disconnected;
    });
    const view = await mount();
    await view.user.click(screen.getByRole('button', { name: text.connect }));
    expect(mocks.begin).toHaveBeenCalledWith('0', expect.any(AbortSignal));
    expect(await screen.findByLabelText(text.code)).toHaveValue('CODE-FIXTURE');
    expect(screen.getByRole('link', { name: text.openOfficial })).toHaveAttribute(
      'href',
      'https://auth.openai.com/codex/device',
    );
    expect(JSON.stringify(localStorage)).not.toContain('CODE-FIXTURE');
    await view.user.click(screen.getByRole('button', { name: common.common.cancel }));
    await waitFor(() => expect(screen.queryByLabelText(text.code)).not.toBeInTheDocument());
    expect(mocks.cancel).toHaveBeenCalledWith('attempt', expect.any(AbortSignal));
  });
  it('explains expired login and allows another attempt', async () => {
    mocks.get.mockResolvedValue({
      ...disconnected,
      login: { id: 'old', state: 'expired', expires_at: 1800000000, error_code: 'expired' },
    });
    await mount();
    expect(screen.getByText(text.expired)).toBeVisible();
    expect(screen.getByRole('button', { name: text.connect })).toBeEnabled();
  });
  it('offers Codex only for administrator and individually selected steward bindings', async () => {
    const source = newProfile();
    source.id = '1';
    source.name = 'Codex source';
    source.settings.provider = 'codex';
    const config: DriverConfiguration = { profiles: [source], bindings: [] };
    await renderWithProviders(
      <>
        <BindingEditor
          config={config}
          scope="auto_review"
          label="Automatic"
          account="synthetic"
          onConfiguration={() => undefined}
        />
        <BindingEditor
          config={config}
          scope="steward_chat"
          label="All stewards"
          account="synthetic"
          onConfiguration={() => undefined}
        />
        <BindingEditor
          config={config}
          scope="steward_chat"
          actor="7"
          label="Selected steward"
          account="synthetic"
          onConfiguration={() => undefined}
        />
        <BindingEditor
          config={config}
          scope="admin_chat"
          label="Administrator"
          account="synthetic"
          onConfiguration={() => undefined}
        />
      </>,
      { station: 'admin', role: 'admin' },
    );
    for (const label of ['Automatic', 'All stewards'])
      expect(
        within(screen.getByLabelText(label)).queryByRole('option', { name: 'Codex source' }),
      ).not.toBeInTheDocument();
    for (const label of ['Selected steward', 'Administrator'])
      expect(
        within(screen.getByLabelText(label)).getByRole('option', { name: 'Codex source' }),
      ).toBeInTheDocument();
  });
});
