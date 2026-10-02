import { screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import { SettingsPage } from './SettingsPage';
import type { SiteConfigBundle, SiteConfigCatalogEntry } from '../features/operations/core';

const api = vi.hoisted(() => ({ getSiteConfigBundle: vi.fn(), patchSiteSettings: vi.fn() }));
vi.mock('../features/operations/core', async (original) => ({
  ...(await original<typeof import('../features/operations/core')>()),
  ...api,
}));
vi.mock('@shared/operations/MaintenancePanel', () => ({ MaintenancePanel: () => null }));
vi.mock('../features/operations/LegalHoldPanel', () => ({ LegalHoldPanel: () => null }));
vi.mock('../features/gateway/GatewayCapabilitiesSection', () => ({ default: () => null }));

const pair = (en: string, zh: string) => ({ en, zh });
function entry(
  key: string,
  group: string,
  title: SiteConfigCatalogEntry['title'],
): SiteConfigCatalogEntry {
  return {
    key,
    group,
    title,
    type: 'integer',
    description: pair('How long to wait.', '等待多长时间。'),
    unit: pair('seconds', '秒'),
    nullable: false,
    null_writable: false,
    raw_default: 120,
    effective_fallback: 120,
    minimum: 1,
    maximum: 600,
    step: 1,
    allowed_values: [],
    zero_semantics: pair('Not allowed.', '不可为零。'),
    null_semantics: pair('Use default.', '使用默认。'),
    empty_semantics: pair('Enter a duration.', '填写时长。'),
    independent_gates: [],
    write_endpoint: '/admin/api/site-config/settings',
  };
}
let bundle: SiteConfigBundle;
beforeEach(() => {
  bundle = {
    revision: '1',
    values: { response_wait_seconds: 130 },
    catalog: [entry('response_wait_seconds', 'limits', pair('Response wait', '响应等待'))],
  };
  api.getSiteConfigBundle.mockImplementation(async () => structuredClone(bundle));
  api.patchSiteSettings.mockImplementation(
    async (input: { values: SiteConfigBundle['values'] }) => {
      bundle = { ...bundle, revision: '2', values: { ...bundle.values, ...input.values } };
      return { revision: '2' };
    },
  );
  api.patchSiteSettings.mockClear();
});

describe('site settings discovery and saving', () => {
  it.each(['en', 'zh'] as const)(
    'reveals searched settings and keeps an edited field visible in %s',
    async (locale) => {
      const view = await renderWithProviders(<SettingsPage />, {
        station: 'admin',
        role: 'admin',
        locale,
      });
      view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'fixture' } });
      const label = locale === 'en' ? 'Response wait' : '响应等待';
      await screen.findByText(locale === 'en' ? '1 customized' : '1 项已调整', { exact: false });
      expect(screen.queryByLabelText(label)).toBeNull();
      const search = screen.getByRole('searchbox');
      await view.user.type(search, label);
      const field = screen.getByLabelText(label);
      expect(field).toHaveValue(130);
      await view.user.clear(field);
      await view.user.type(field, '150');
      await view.user.clear(search);
      expect(screen.getByLabelText(label)).toHaveValue(150);
      await view.user.click(
        screen.getByRole('button', { name: locale === 'en' ? 'Save all changes' : '保存所有修改' }),
      );
      await screen.findByText(locale === 'en' ? 'Settings saved.' : '设置已保存。');
      expect(api.patchSiteSettings).toHaveBeenCalledTimes(1);
      expect(api.patchSiteSettings.mock.calls[0][0]).toEqual({
        expected_revision: '1',
        values: { response_wait_seconds: 150 },
      });
      await view.user.type(search, label);
      await view.user.clear(screen.getByLabelText(label));
      expect(screen.queryByText(locale === 'en' ? 'Settings saved.' : '设置已保存。')).toBeNull();
      expect(screen.getByRole('alert')).toBeInTheDocument();
    },
  );

  it('keeps a rejected draft visible for correction', async () => {
    api.patchSiteSettings.mockRejectedValueOnce(new Error('Rejected save'));
    const view = await renderWithProviders(<SettingsPage />, { station: 'admin', role: 'admin' });
    view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'fixture' } });
    await view.user.type(await screen.findByRole('searchbox'), 'Response wait');
    const field = screen.getByLabelText('Response wait');
    await view.user.clear(field);
    await view.user.type(field, '151');
    await view.user.click(screen.getByRole('button', { name: 'Save all changes' }));
    await waitFor(() => expect(api.patchSiteSettings).toHaveBeenCalledTimes(1));
    await screen.findByRole('alert');
    expect(screen.getByLabelText('Response wait')).toHaveValue(151);
  });
});
