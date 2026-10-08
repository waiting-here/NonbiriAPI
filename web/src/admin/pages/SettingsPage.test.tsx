import { screen, waitFor, within } from '@testing-library/react';
import { useState } from 'react';
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
vi.mock('../features/operations/LegalHoldPanel', () => ({
  LegalHoldPanel: () => (
    <input type="password" autoComplete="current-password" aria-label="Hold password" />
  ),
}));
vi.mock('../features/gateway/GatewayCapabilitiesSection', () => ({
  default: function GatewayDraft() {
    const [value, setValue] = useState('');
    return (
      <input
        aria-label="Gateway draft"
        value={value}
        onChange={(event) => setValue(event.target.value)}
      />
    );
  },
}));

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
  it('restores a default in the draft and saves only through the save bar', async () => {
    const view = await renderWithProviders(<SettingsPage />, { station: 'admin', role: 'admin' });
    view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'fixture' } });
    expect(await screen.findByLabelText('Response wait')).toHaveValue(130);
    expect(screen.getByText('Modified')).toBeVisible();
    await view.user.click(screen.getByRole('button', { name: 'Restore default' }));
    expect(screen.getByLabelText('Response wait')).toHaveValue(120);
    expect(screen.queryByText('Modified')).toBeNull();
    expect(api.patchSiteSettings).not.toHaveBeenCalled();
    await view.user.click(screen.getByRole('button', { name: 'Save all changes' }));
    await waitFor(() => expect(api.patchSiteSettings).toHaveBeenCalledTimes(1));
    expect(api.patchSiteSettings.mock.calls[0][0]).toEqual({
      expected_revision: '1',
      values: { response_wait_seconds: 120 },
    });
  });
  it('opens legal text before mounting credential forms and isolates search autofill', async () => {
    bundle.catalog.push({
      ...entry('legal_terms_override_zh', 'legal', pair('Terms in Chinese', '中文服务条款')),
      type: 'text',
      unit: null,
      minimum: null,
      maximum: 100000,
      step: null,
      raw_default: '',
      effective_fallback: '',
    });
    bundle.values.legal_terms_override_zh = 'Reviewable terms';
    const view = await renderWithProviders(<SettingsPage />, { station: 'admin', role: 'admin' });
    await screen.findByLabelText('Response wait');
    expect(screen.queryByLabelText('Hold password')).toBeNull();
    const search = screen.getByRole('searchbox');
    expect(search).toHaveAttribute('autocomplete', 'off');
    expect(search.closest('form')).toBe(screen.getByRole('search'));
    await view.user.type(search, 'unmatched prior search');
    await view.user.click(screen.getByRole('button', { name: 'Legal text' }));
    expect(search).toHaveValue('');
    expect(screen.getByLabelText('Terms in Chinese')).toHaveValue('Reviewable terms');
    expect(screen.queryByLabelText('Hold password')).toBeNull();
    await view.user.click(screen.getByRole('button', { name: /^Legal holds$/ }));
    expect(screen.getByLabelText('Hold password')).toBeVisible();
    expect(search.closest('form')!.contains(screen.getByLabelText('Hold password'))).toBe(false);
  });
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
      expect(await screen.findByLabelText(label)).toHaveValue(130);
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
  it('retains edits across groups and confirms settings requiring care', async () => {
    bundle.catalog.push(entry('access_wait', 'access', pair('Access wait', '访问等待')));
    bundle.values.access_wait = 120;
    const view = await renderWithProviders(<SettingsPage />, {
      station: 'admin',
      role: 'admin',
      route: '/settings?group=limits',
    });
    view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'fixture' } });
    const field = await screen.findByLabelText('Response wait');
    await view.user.clear(field);
    await view.user.type(field, '150');
    await view.user.click(screen.getByRole('button', { name: 'Access' }));
    expect(screen.queryByLabelText('Response wait')).toBeNull();
    await view.user.clear(screen.getByLabelText('Access wait'));
    await view.user.type(screen.getByLabelText('Access wait'), '140');
    await view.user.type(screen.getByRole('searchbox'), 'response_wait');
    expect(screen.getByLabelText('Response wait')).toHaveValue(150);
    await view.user.clear(screen.getByRole('searchbox'));
    expect(screen.getByLabelText('Access wait')).toHaveValue(140);
    await view.user.click(screen.getByRole('button', { name: 'Save all changes' }));
    expect(api.patchSiteSettings).not.toHaveBeenCalled();
    const dialog = screen.getByRole('alertdialog');
    await view.user.click(within(dialog).getByRole('button', { name: 'Save all changes' }));
    await waitFor(() => expect(api.patchSiteSettings).toHaveBeenCalledTimes(1));
    expect(api.patchSiteSettings.mock.calls[0][0]).toEqual({
      expected_revision: '1',
      values: { response_wait_seconds: 150, access_wait: 140 },
    });
  });

  it('allows discarding invalid edits while saving is disabled', async () => {
    const view = await renderWithProviders(<SettingsPage />, { station: 'admin', role: 'admin' });
    await view.user.clear(await screen.findByLabelText('Response wait'));
    expect(screen.getByRole('button', { name: 'Save all changes' })).toBeDisabled();
    await view.user.click(screen.getByRole('button', { name: 'Discard changes' }));
    expect(screen.getByLabelText('Response wait')).toHaveValue(130);
    expect(screen.queryByRole('button', { name: 'Save all changes' })).toBeNull();
    expect(api.patchSiteSettings).not.toHaveBeenCalled();
  });

  it('keeps independent form drafts when switching groups or searching', async () => {
    const view = await renderWithProviders(<SettingsPage />, { station: 'admin', role: 'admin' });
    await screen.findByLabelText('Response wait');
    await view.user.click(screen.getByRole('button', { name: 'Gateway model capabilities' }));
    await view.user.type(screen.getByRole('textbox', { name: 'Gateway draft' }), 'saved locally');
    await view.user.click(screen.getByRole('button', { name: 'Limits' }));
    expect(screen.getByLabelText('Gateway draft')).not.toBeVisible();
    await view.user.click(screen.getByRole('button', { name: 'Gateway model capabilities' }));
    await view.user.type(screen.getByRole('searchbox'), 'Response wait');
    expect(screen.getByLabelText('Gateway draft')).not.toBeVisible();
    await view.user.clear(screen.getByRole('searchbox'));
    expect(screen.getByRole('textbox', { name: 'Gateway draft' })).toHaveValue('saved locally');
    expect(api.patchSiteSettings).not.toHaveBeenCalled();
  });
});
