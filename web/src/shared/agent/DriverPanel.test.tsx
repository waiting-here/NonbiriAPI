import { act, screen, waitFor, within } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '@shared/query/http';
import { DisplayTimeContext } from '@shared/components/timeContextValue';
import { renderWithProviders } from '../../../test/unit/support';
import common from '@shared/i18n/common/en.json';
import { DriverPanel } from './DriverPanel';
import { ChatGPTConnection } from './ChatGPTConnection';
import { newProfile, type DriverConfiguration, type DriverProfile } from './driversApi';

const mocks = vi.hoisted(() => ({
  drivers: vi.fn(),
  save: vi.fn(),
  models: vi.fn(),
  binding: vi.fn(),
  status: vi.fn(),
  target: vi.fn(),
  import: vi.fn(),
  logout: vi.fn(),
  owners: vi.fn(),
}));
vi.mock('./driversApi', async (original) => ({
  ...(await original<typeof import('./driversApi')>()),
  getDrivers: mocks.drivers,
  saveProfile: mocks.save,
  discoverProfileModels: mocks.models,
  saveBinding: mocks.binding,
  getChatGPT: mocks.status,
  getChatGPTTarget: mocks.target,
  importChatGPT: mocks.import,
  logoutChatGPT: mocks.logout,
}));
vi.mock('./CodexConnection', () => ({ CodexConnection: () => null }));
vi.mock('./api', async (original) => ({
  ...(await original<typeof import('./api')>()),
  getMemoryOwners: mocks.owners,
}));
const text = common.common.agent.drivers;
const connected = {
  connected: true,
  revision: '2',
  host_id: 'synthetic-host',
  account_label: 'Synthetic account',
  expires_at: 1000000000,
  needs_login: false,
};
let config: DriverConfiguration;
const profile = (): DriverProfile => ({
  ...newProfile(),
  id: '1',
  name: 'Source one',
  settings: {
    ...newProfile().settings,
    driver_id: '1',
    revision: '2',
    base_url: 'https://synthetic.test/v1',
    model: 'synthetic-model',
    has_key: true,
  },
});
beforeEach(() => {
  vi.clearAllMocks();
  config = { profiles: [{ ...newProfile(), id: '0', name: 'Original' }, profile()], bindings: [] };
  mocks.drivers.mockImplementation(async () => config);
  mocks.status.mockResolvedValue(connected);
  mocks.models.mockResolvedValue(['new-model']);
  mocks.owners.mockResolvedValue({
    items: [
      { id: '7', role: 'level5', label: 'Synthetic steward' },
      { id: '8', role: 'admin', label: 'Synthetic admin' },
    ],
    total: '2',
    page: 1,
    page_size: 10,
    current_owner_id: '8',
  });
  mocks.binding.mockImplementation(async (input) => {
    const value = {
      scope: input.scope,
      actor_id: input.actor_id,
      driver_id: input.driver_id ?? '0',
      revision: input.driver_id === null ? '0' : '1',
    };
    config = { ...config, bindings: input.driver_id === null ? [] : [value] };
    return value;
  });
});
async function mount(connectionOnly = false) {
  const view = await renderWithProviders(
    <DisplayTimeContext.Provider value={{ mode: 'site', offset_minutes: 0 }}>
      {connectionOnly ? (
        <ChatGPTConnection account="synthetic" active />
      ) : (
        <DriverPanel account="synthetic" active />
      )}
    </DisplayTimeContext.Provider>,
    { station: 'admin', role: 'admin' },
  );
  view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'synthetic' } });
  await screen.findByText('Synthetic account', { exact: false });
  return view;
}
describe('Model source settings', () => {
  it('saves before fetching models and keeps API keys out of caches', async () => {
    const order: string[] = [];
    mocks.save.mockImplementation(async (_id, input) => {
      order.push('save');
      const value = {
        ...profile(),
        name: input.name,
        settings: { ...profile().settings, ...input.settings, revision: '3' },
      };
      config = { ...config, profiles: [config.profiles[0], value] };
      return value;
    });
    mocks.models.mockImplementation(async () => {
      order.push('models');
      return ['new-model'];
    });
    const view = await mount();
    await view.user.click(screen.getByRole('button', { name: common.common.edit }));
    const editor = screen.getByRole('form', { name: text.editSource });
    await view.user.type(
      within(editor).getByLabelText(common.common.connections.secret),
      'synthetic-key-value',
    );
    await view.user.click(
      within(editor).getByRole('button', { name: common.common.agent.discoverModels }),
    );
    await waitFor(() => expect(order).toEqual(['save', 'models']));
    expect(mocks.save.mock.calls[0][1].api_key).toBe('synthetic-key-value');
    expect(
      JSON.stringify(
        view.queryClient
          .getQueryCache()
          .getAll()
          .map((item) => item.state.data),
      ),
    ).not.toContain('synthetic-key-value');
    expect(
      JSON.stringify(
        view.queryClient
          .getMutationCache()
          .getAll()
          .map((item) => item.state.variables),
      ),
    ).not.toContain('synthetic-key-value');
    expect(within(editor).getByLabelText(common.common.connections.model)).toHaveValue(
      'synthetic-model',
    );
    expect(editor.querySelector('datalist option[value="new-model"]')).toBeInTheDocument();
  });
  it('keeps a conflicting draft until the administrator adopts the current revision', async () => {
    mocks.save.mockImplementation(async () => {
      config = {
        ...config,
        profiles: [
          config.profiles[0],
          { ...profile(), settings: { ...profile().settings, revision: '3' } },
        ],
      };
      throw new ApiError('conflict', 'Changed elsewhere', 409);
    });
    const view = await mount();
    await view.user.click(screen.getByRole('button', { name: common.common.edit }));
    const editor = screen.getByRole('form', { name: text.editSource });
    const name = within(editor).getByLabelText(text.name);
    await view.user.clear(name);
    await view.user.type(name, 'My draft');
    await view.user.click(within(editor).getByRole('button', { name: common.common.save }));
    await screen.findByText(text.currentRevision.replace('{{revision}}', '3'));
    expect(name).toHaveValue('My draft');
    await view.user.click(
      within(editor).getByRole('button', { name: common.common.agent.keepDraft }),
    );
    mocks.save.mockImplementation(async (_id, input) => ({
      ...profile(),
      name: input.name,
      settings: { ...profile().settings, revision: '4' },
    }));
    await view.user.click(within(editor).getByRole('button', { name: common.common.save }));
    await waitFor(() => expect(mocks.save.mock.calls[1][1].expected_revision).toBe('3'));
  });
  it('shows possible unknown creations without repeating POST or claiming ownership', async () => {
    mocks.save.mockImplementation(async (_id, input) => {
      config = {
        ...config,
        profiles: [
          ...config.profiles,
          {
            ...profile(),
            id: '3',
            name: input.name,
            settings: { ...profile().settings, provider: input.settings.provider },
          },
        ],
      };
      throw new ApiError('network_error', 'Response lost', 0);
    });
    const view = await mount();
    await view.user.click(screen.getByRole('button', { name: text.addSource }));
    const editor = screen.getByRole('form', { name: text.editSource });
    await view.user.clear(within(editor).getByLabelText(text.name));
    await view.user.type(within(editor).getByLabelText(text.name), 'Candidate');
    await view.user.selectOptions(within(editor).getByLabelText(text.provider), 'chatgpt');
    await view.user.click(within(editor).getByRole('button', { name: common.common.save }));
    await screen.findByText(text.writeUncertain);
    expect(screen.getByText(text.creationCandidates)).toBeInTheDocument();
    await view.user.click(within(editor).getByRole('button', { name: text.checkStatus }));
    expect(mocks.save).toHaveBeenCalledTimes(1);
    expect(within(editor).getByRole('button', { name: common.common.save })).toBeDisabled();
    expect(within(editor).getByLabelText(text.name)).toHaveValue('Candidate');
  });
  it('selects a steward by name and restores inherited source with null', async () => {
    const view = await mount();
    await view.user.click(screen.getByText(text.individual));
    const picker = await screen.findByLabelText(text.steward);
    expect(
      within(picker).queryByRole('option', { name: /Synthetic admin/ }),
    ).not.toBeInTheDocument();
    await view.user.selectOptions(picker, '7');
    const selection = screen.getByLabelText(
      text.forSteward.replace('{{name}}', 'Synthetic steward'),
    );
    const form = selection.closest('form')!;
    await view.user.selectOptions(selection, '1');
    await view.user.click(within(form).getByRole('button', { name: common.common.save }));
    await waitFor(() =>
      expect(mocks.binding).toHaveBeenCalledWith(
        { scope: 'steward_chat', actor_id: '7', driver_id: '1', expected_revision: '0' },
        expect.anything(),
      ),
    );
    await view.user.selectOptions(selection, '');
    await view.user.click(within(form).getByRole('button', { name: common.common.save }));
    await waitFor(() =>
      expect(mocks.binding).toHaveBeenLastCalledWith(
        { scope: 'steward_chat', actor_id: '7', driver_id: null, expected_revision: '1' },
        expect.anything(),
      ),
    );
  });
});
describe('ChatGPT connection transfer', () => {
  it('distinguishes official account quota from retained local request usage', async () => {
    mocks.status.mockResolvedValue({
      ...connected,
      updated_at: Date.parse('2026-10-27T12:30:00Z'),
      expires_at: Date.parse('2026-10-27T13:45:00Z') / 1000,
      official_usage: {
        availability: 'unavailable',
        reason: 'not_exposed_by_public_api',
        usage_url: 'https://chatgpt.com/settings/usage',
      },
      local_usage: {
        scope: 'retained_completed_chatgpt_requests',
        window_start: Date.parse('2026-10-26T15:00:00Z'),
        window_end: Date.parse('2026-10-27T15:00:00Z'),
        requests: '2',
        input_tokens: '9007199254740993',
        cached_input_tokens: '0',
        cache_write_input_tokens: '0',
        output_tokens: '3',
        reasoning_tokens: '0',
        unknown_count: '1',
      },
      last_limit_error: {
        code: 'subscription_sharing_usage_limit_exceeded',
        occurred_at: Date.parse('2026-10-27T14:10:00Z'),
      },
    });
    await mount(true);
    expect(screen.getByText(text.officialUnavailable)).toBeInTheDocument();
    expect(screen.getByText(text.localUsageHelp)).toBeInTheDocument();
    expect(screen.getByText('9007199254740993')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: text.openOfficialUsage })).toHaveAttribute(
      'href',
      'https://chatgpt.com/settings/usage',
    );
    expect(screen.getByText(text.lastLimit)).toBeInTheDocument();
    for (const date of [
      '10/27/2026, 12:30 PM',
      '10/27/2026, 01:45 PM',
      '10/26/2026, 03:00 PM',
      '10/27/2026, 03:00 PM',
      '10/27/2026, 02:10 PM',
    ]) {
      expect(screen.getByText(date)).toBeInTheDocument();
    }
    expect(screen.queryByText(/1970/)).not.toBeInTheDocument();
  });
  it('allows another account after logout while keeping the first login on the same-account default', async () => {
    mocks.status.mockResolvedValue({
      ...connected,
      connected: false,
      revision: '3',
      needs_login: true,
    });
    mocks.target.mockResolvedValue({
      version: 1,
      host_id: 'synthetic-host',
      revision: '3',
      public_key: 'public',
      new_account: true,
    });
    vi.stubGlobal(
      'URL',
      class extends URL {
        static createObjectURL = vi.fn(() => 'blob:synthetic-target');
        static revokeObjectURL = vi.fn();
      },
    );
    vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => undefined);
    const view = await mount(true);
    await view.user.click(screen.getByRole('button', { name: text.signIn }));
    const choice = screen.getByLabelText(text.loginAccount);
    expect(choice).toHaveValue('same');
    await view.user.selectOptions(choice, 'new');
    await view.user.click(screen.getByRole('button', { name: text.downloadTarget }));
    await waitFor(() => expect(mocks.target).toHaveBeenCalledWith(true, expect.anything()));
  });
  it('imports account-switch JSON unchanged and clears the file without caching it', async () => {
    mocks.import.mockResolvedValue({ ...connected, revision: '3' });
    const view = await mount(true);
    await view.user.click(screen.getByRole('button', { name: text.signIn }));
    const envelope = {
      version: 1,
      revision: '2',
      host_id: 'synthetic-host',
      ciphertext: 'synthetic-encrypted-transfer',
      ephemeral_key: 'ephemeral',
      nonce: 'nonce',
      new_account: true,
    };
    const upload = new File([JSON.stringify(envelope)], 'connection.json', {
      type: 'application/json',
    });
    Object.defineProperty(upload, 'text', { value: async () => JSON.stringify(envelope) });
    const input = screen.getByLabelText(text.connectionFile);
    await view.user.upload(input, upload);
    await view.user.click(screen.getByRole('button', { name: text.importConnection }));
    await waitFor(() => expect(mocks.import).toHaveBeenCalledWith(envelope, expect.anything()));
    await waitFor(() => expect((input as HTMLInputElement).files).toHaveLength(0));
    expect(
      JSON.stringify(
        view.queryClient
          .getQueryCache()
          .getAll()
          .map((item) => item.state.data),
      ),
    ).not.toContain('synthetic-encrypted-transfer');
    expect(
      JSON.stringify(
        view.queryClient
          .getMutationCache()
          .getAll()
          .map((item) => item.state.variables),
      ),
    ).not.toContain('synthetic-encrypted-transfer');
  });
  it('checks an unknown import without submitting the file twice', async () => {
    mocks.import.mockRejectedValue(new ApiError('network_error', 'Response lost', 0));
    const view = await mount(true);
    await view.user.click(screen.getByRole('button', { name: text.signIn }));
    const upload = new File(['{}'], 'connection.json', { type: 'application/json' });
    Object.defineProperty(upload, 'text', { value: async () => '{"ciphertext":"encrypted"}' });
    await view.user.upload(screen.getByLabelText(text.connectionFile), upload);
    await view.user.click(screen.getByRole('button', { name: text.importConnection }));
    await screen.findByText(text.connectionUncertain);
    await view.user.click(screen.getByRole('button', { name: text.checkStatus }));
    expect(mocks.import).toHaveBeenCalledTimes(1);
    expect(screen.getByRole('button', { name: text.importConnection })).toBeDisabled();
  });
  it('ignores a late import response after the station account changes', async () => {
    let resolve!: (value: typeof connected) => void;
    mocks.import.mockReturnValue(
      new Promise((done) => {
        resolve = done;
      }),
    );
    const view = await mount(true);
    await view.user.click(screen.getByRole('button', { name: text.signIn }));
    const upload = new File(['{}'], 'connection.json', { type: 'application/json' });
    Object.defineProperty(upload, 'text', { value: async () => '{"ciphertext":"encrypted"}' });
    await view.user.upload(screen.getByLabelText(text.connectionFile), upload);
    await view.user.click(screen.getByRole('button', { name: text.importConnection }));
    await waitFor(() => expect(mocks.import).toHaveBeenCalledTimes(1));
    act(() =>
      view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'another' } }),
    );
    await act(async () => resolve({ ...connected, account_label: 'Late account' }));
    expect(view.queryClient.getQueryData(['admin', 'ai', 'synthetic', 'chatgpt'])).toMatchObject({
      account_label: 'Synthetic account',
    });
    expect(screen.queryByText('Late account', { exact: false })).not.toBeInTheDocument();
  });
});
