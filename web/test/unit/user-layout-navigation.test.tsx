import { screen, waitFor } from '@testing-library/react';
import { describe, expect, test } from 'vitest';
import { Route, Routes } from 'react-router';
import { UserLayout } from '../../src/user/layouts/UserLayout';
import { installJsonFetchFixtures, renderWithProviders } from './support';

const levelFiveSession = {
  user: {
    id: '5',
    username: 'fixture-user',
    lang: 'en',
    is_banned: false,
    endpoint_limit: null,
    effective_endpoint_limit: 10,
    rpm_limit: null,
    effective_rpm_limit: 60,
    concurrency_limit: null,
    effective_concurrency_limit: 5,
    credits: '0',
    donation_credit: '0',
    effective_level: 5,
    created_at: '2026-08-23T00:00:00Z',
  },
};

const publicConfig = {
  site_name: 'Fixture Site',
  site_logo_url: '',
  legal_privacy_override_zh: '',
  legal_privacy_override_en: '',
  legal_terms_override_zh: '',
  legal_terms_override_en: '',
  legal_authoritative_locale: '',
  maintenance_mode: false,
  registration_open: true,
  announcement_epoch: 'b1e_AAAAAAAAAAAAAAAAAAAAAA',
};

describe('level-five user shell navigation', () => {
  test('keeps steward out of primary nav and exposes one drawer entry', async () => {
    installJsonFetchFixtures([
      { method: 'GET', path: '/api/session', body: levelFiveSession },
      { method: 'GET', path: '/api/config', body: publicConfig },
    ]);
    const rendered = await renderWithProviders(
      <Routes>
        <Route element={<UserLayout />}>
          <Route index element={<div>home</div>} />
        </Route>
      </Routes>,
      { station: 'user', role: 'level5' },
    );

    await waitFor(() => {
      const state = rendered.queryClient.getQueryState(['user', 'session']);
      if (state?.error) throw new Error(`session fixture failed: ${String(state.error)}`);
      expect(state?.data).toBeDefined();
    });
    await screen.findByRole('button', { name: 'fixture-user' });
    expect(screen.queryAllByRole('link', { name: 'Steward' })).toHaveLength(0);

    await rendered.user.click(screen.getByRole('button', { name: 'Open navigation' }));
    await waitFor(() => expect(screen.getAllByRole('link', { name: 'Steward' })).toHaveLength(1));
    const closeMenuButton = screen
      .getAllByRole('button', { name: 'Close navigation' })
      .find((button) => button.classList.contains('nb-menu-button'));
    expect(closeMenuButton).toBeDefined();
    await rendered.user.click(closeMenuButton!);
    expect(screen.queryAllByRole('link', { name: 'Steward' })).toHaveLength(0);

    await rendered.user.click(screen.getByRole('button', { name: 'fixture-user' }));
    expect(screen.getAllByRole('link', { name: 'Steward' })).toHaveLength(1);
  });
});
