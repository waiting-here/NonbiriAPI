import { act, screen, waitFor, within } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import common from '@shared/i18n/common/en.json';
import type { AgentSettings, DriverSettings } from './api';
import { AdminSettings } from './AdminSettings';
vi.mock('./DriverPanel', () => ({ DriverPanel: () => null }));
const mocks = vi.hoisted(() => ({
  driver: vi.fn(),
  settings: vi.fn(),
  putDriver: vi.fn(),
  putSettings: vi.fn(),
  models: vi.fn(),
}));
vi.mock('./api', async (original) => ({
  ...(await original<typeof import('./api')>()),
  getDriver: mocks.driver,
  getSettings: mocks.settings,
  putDriver: mocks.putDriver,
  putSettings: mocks.putSettings,
  discoverModels: mocks.models,
}));
const driver: DriverSettings = {
  revision: '0',
  base_url: '',
  has_key: false,
  model: '',
  context_window: 32768,
  max_output_tokens: 4096,
  extra_parameters: {},
  concurrency: 1,
  timeout_seconds: 600,
  output_token_parameter: 'max_tokens',
};
const settings: AgentSettings = {
  chat_enabled: false,
  revision: '0',
  mode: 'off',
  auto_template: [
    { role: 'system', content: 'First {{memory}}' },
    { role: 'user', content: 'Second {{intelligence}}' },
  ],
  chat_template: [
    { role: 'system', content: '{{memory}}' },
    { role: 'user', content: '{{conversation}}' },
  ],
  auto_prefill: '',
  chat_prefill: '',
  tools: {
    donations: true,
    donation_models: true,
    review_history: true,
    charity_models: true,
    charity_binding_presets: true,
    users: true,
    site_status: true,
    web_search: true,
    propose_operations: true,
    edit_memory: true,
  },
  tool_descriptions: {
    donations: 'Read current donation details before proposing a review.',
    edit_memory: 'Read the current revision before proposing a memory change.',
  },
  search_url: '',
  search_has_key: false,
};
beforeEach(() => {
  vi.clearAllMocks();
  mocks.driver.mockResolvedValue(driver);
  mocks.settings.mockResolvedValue(settings);
  mocks.models.mockResolvedValue(['synthetic-model']);
  mocks.putSettings.mockImplementation(async (input) => ({ ...settings, ...input, revision: '1' }));
});
async function mount() {
  const view = await renderWithProviders(<AdminSettings account="synthetic" active />, {
    station: 'admin',
    role: 'admin',
  });
  view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'synthetic' } });
  return view;
}
describe('Agent administrator settings', () => {
  it('saves endpoint credentials before discovery and keeps the plaintext key out of query caches', async () => {
    const order: string[] = [];
    mocks.putDriver.mockImplementation(async (input) => {
      order.push('save');
      const saved = { ...driver, ...input, revision: '1', has_key: true };
      delete saved.api_key;
      return saved;
    });
    mocks.models.mockImplementation(async () => {
      order.push('discover');
      return ['synthetic-model'];
    });
    const view = await mount();
    await view.user.type(
      await screen.findByLabelText(common.common.connections.entryURL),
      'https://driver.example/v1',
    );
    await view.user.type(
      screen.getByLabelText(common.common.connections.secret),
      'synthetic-driver-secret',
    );
    await view.user.click(screen.getByRole('button', { name: 'Fetch models' }));
    await waitFor(() => expect(order).toEqual(['save', 'discover']));
    expect(document.querySelector('datalist option')).toHaveAttribute('value', 'synthetic-model');
    expect(mocks.putDriver.mock.calls[0]![0]).toMatchObject({
      api_key: 'synthetic-driver-secret',
      expected_revision: '0',
    });
    expect(
      JSON.stringify(view.queryClient.getQueryData(['admin', 'ai', 'synthetic', 'driver'])),
    ).not.toContain('synthetic-driver-secret');
  });
  it('keeps ordered template roles and contents when messages move', async () => {
    const view = await mount();
    await screen.findByLabelText('Automatic review mode');
    await view.user.click(screen.getByText('Message templates', { selector: 'strong' }));
    const list = screen.getAllByRole('list')[0]!;
    await view.user.click(
      within(within(list).getAllByRole('listitem')[0]!).getByRole('button', {
        name: common.common.connections.moveDown,
      }),
    );
    const save = screen
      .getAllByRole('button', { name: common.common.save })
      .find((button) => !button.hasAttribute('disabled'))!;
    await view.user.click(save);
    await waitFor(() => expect(mocks.putSettings).toHaveBeenCalledTimes(1));
    expect(mocks.putSettings.mock.calls[0]![0].auto_template).toEqual([
      { role: 'user', content: 'Second {{intelligence}}' },
      { role: 'system', content: 'First {{memory}}' },
    ]);
  });
  it('offers chat closed by default and saves its explicit switch', async () => {
    const view = await mount();
    const toggle = await screen.findByRole('switch', { name: 'Enable Agent chat' });
    expect(toggle).not.toBeChecked();
    await view.user.click(toggle);
    await view.user.click(
      screen
        .getAllByRole('button', { name: 'Save' })
        .find((button) => !button.hasAttribute('disabled'))!,
    );
    await waitFor(() => expect(mocks.putSettings).toHaveBeenCalledTimes(1));
    expect(mocks.putSettings.mock.calls[0]![0].chat_enabled).toBe(true);
  });
  it('saves custom tool instructions without changing other tools or message templates', async () => {
    const view = await mount();
    await screen.findByLabelText('Automatic review mode');
    await view.user.click(screen.getByText(common.common.agent.toolsTitle, { selector: 'strong' }));
    expect(
      screen.getByRole('switch', { name: common.common.agent.tools.charity_binding_presets }),
    ).toBeChecked();
    await view.user.click(
      screen.getAllByText(common.common.agent.toolDescription, { selector: 'strong' })[0]!,
    );
    const description = screen.getByLabelText(
      `${common.common.agent.tools.donations}: instructions`,
    );
    expect(description).toHaveValue(settings.tool_descriptions!.donations);
    await view.user.clear(description);
    await view.user.type(
      description,
      'Read the specified donation and report facts missing from its catalog.',
    );
    await view.user.click(
      screen
        .getAllByRole('button', { name: 'Save' })
        .find((button) => !button.hasAttribute('disabled'))!,
    );
    await waitFor(() => expect(mocks.putSettings).toHaveBeenCalledTimes(1));
    expect(mocks.putSettings.mock.calls[0]![0]).toMatchObject({
      expected_revision: '0',
      chat_enabled: false,
      tools: settings.tools,
      auto_template: settings.auto_template,
      chat_template: settings.chat_template,
      tool_descriptions: {
        ...settings.tool_descriptions,
        donations: 'Read the specified donation and report facts missing from its catalog.',
      },
    });
    await view.user.clear(description);
    await view.user.click(
      screen
        .getAllByRole('button', { name: 'Save' })
        .find((button) => !button.hasAttribute('disabled'))!,
    );
    await waitFor(() => expect(mocks.putSettings).toHaveBeenCalledTimes(2));
    expect(mocks.putSettings.mock.calls[1]![0].tool_descriptions.donations).toBe('');
  });
  it('does not publish a late settings save into the cache after the session changes', async () => {
    let finish!: (value: AgentSettings) => void;
    mocks.putSettings.mockReturnValue(
      new Promise((resolve) => {
        finish = resolve;
      }),
    );
    const view = await mount();
    await view.user.click(await screen.findByRole('switch', { name: 'Enable Agent chat' }));
    await view.user.click(
      screen
        .getAllByRole('button', { name: 'Save' })
        .find((button) => !button.hasAttribute('disabled'))!,
    );
    await waitFor(() => expect(mocks.putSettings).toHaveBeenCalledTimes(1));
    view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'different' } });
    finish({ ...settings, revision: '2', chat_enabled: true });
    await screen.findByRole('alert');
    expect(view.queryClient.getQueryData(['admin', 'ai', 'synthetic', 'settings'])).toEqual(
      settings,
    );
  });
  it('does not publish a late driver reload after leaving the page', async () => {
    let finish!: (value: DriverSettings) => void;
    const view = await mount();
    await screen.findByLabelText(common.common.connections.entryURL);
    mocks.driver.mockReturnValue(
      new Promise((resolve) => {
        finish = resolve;
      }),
    );
    await view.user.click(screen.getAllByRole('button', { name: 'Load current values' })[0]!);
    await waitFor(() => expect(mocks.driver).toHaveBeenCalledTimes(2));
    view.unmount();
    await act(async () => finish({ ...driver, revision: '9', model: 'late model' }));
    expect(view.queryClient.getQueryData(['admin', 'ai', 'synthetic', 'driver'])).toEqual(driver);
  });
});
