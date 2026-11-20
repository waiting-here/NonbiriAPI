import { screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { AdminNavigation } from '../../src/admin/layouts/AdminNavigation';
import { renderWithProviders } from './support';

vi.mock('@shared/components/DonationPendingBadge', () => ({ DonationPendingBadge: () => null }));

describe('finding a management page', () => {
  it('finds links inside collapsed groups, accepts full-width pasted paths, and restores the group on clear', async () => {
    localStorage.setItem('nb.admin.nav.collapsed', '["records"]');
    const follow = vi.fn();
    const view = await renderWithProviders(
      <AdminNavigation accountId="admin" onNavigate={follow} />,
      { station: 'admin', route: '/settings' },
    );
    const menu = within(screen.getByRole('navigation'));
    const search = menu.getByRole('textbox', { name: 'Find a page' });
    expect(menu.queryByRole('link', { name: 'Logs & usage' })).toBeNull();
    await view.user.click(search);
    await view.user.paste('／ＬＯＧＳ');
    expect(menu.getByRole('link', { name: 'Logs & usage' })).toHaveAttribute('href', '/logs');
    await view.user.keyboard('{Escape}');
    expect(search).toHaveValue('');
    expect(search).toHaveFocus();
    expect(menu.queryByRole('link', { name: 'Logs & usage' })).toBeNull();
    await view.user.type(search, 'unknown-page');
    expect(menu.getByRole('status')).toHaveTextContent(/no matching page/i);
    await view.user.click(menu.getByRole('button', { name: 'Clear page search' }));
    await view.user.type(search, '/logs{Enter}');
    expect(follow).toHaveBeenCalledTimes(1);
    expect(search).toHaveValue('');
  });

  it.each([
    {
      locale: 'zh',
      query: '价格',
      label: '价格模板',
      href: '/charity?charity_section=templates',
    },
    {
      locale: 'en',
      query: '模型绑定',
      label: 'Charity models and bindings',
      href: '/charity?charity_section=models',
    },
    {
      locale: 'zh',
      query: 'binding preset',
      label: '渠道绑定预设',
      href: '/charity/channel-presets',
    },
    {
      locale: 'en',
      query: 'redeem code',
      label: 'Rewards and redemption codes',
      href: '/rewards',
    },
    {
      locale: 'zh',
      query: '请求日志',
      label: '日志与用量',
      href: '/logs',
    },
    {
      locale: 'en',
      query: 'activity logs',
      label: 'Discovery and activity',
      href: '/logs?log_view=activity',
    },
    {
      locale: 'zh',
      query: '抽卡预设',
      label: '抽卡配置模板',
      href: '/activities/gacha?gacha_view=presets',
    },
  ] as const)('finds $label for "$query" in $locale', async ({ locale, query, label, href }) => {
    const view = await renderWithProviders(
      <AdminNavigation accountId="admin" onNavigate={vi.fn()} />,
      { station: 'admin', locale, route: '/settings' },
    );
    const menu = within(screen.getByRole('navigation'));
    const search = menu.getByRole('textbox');
    if (href.includes('?') || href === '/charity/channel-presets')
      expect(menu.queryByRole('link', { name: label })).toBeNull();
    await view.user.click(search);
    await view.user.paste(query);
    expect(menu.getByRole('link', { name: label, exact: true })).toHaveAttribute('href', href);
  });
});
