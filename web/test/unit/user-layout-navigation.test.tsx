import { screen, waitFor } from '@testing-library/react';
import { describe, expect, test } from 'vitest';
import { Route, Routes } from 'react-router';
import { UserLayout } from '../../src/user/layouts/UserLayout';
import { installJsonFetchFixtures, renderWithProviders } from './support';

const levelFiveSession = {
  user: {
    id: '5',
    username: 'fixture-user',
    avatar: null,
    avatar_url: null,
    guild_nick: null,
    guild_avatar_url: null,
    lang: 'en',
    is_banned: false,
    banned_until: null,
    charity_suspended_until: null,
    endpoint_limit: null,
    effective_endpoint_limit: '10',
    rpm_limit: null,
    effective_rpm_limit: '60',
    concurrency_limit: null,
    effective_concurrency_limit: '5',
    balance: '0',
    donation_credit: '0',
    effective_level: 5,
    level_display_name: 'Lv5',
    game_profile_public: false,
    created_at: 1_700_000_000,
    updated_at: 1_700_000_001,
    usage: {
      total_requests: '0',
      total_uncached_input_tokens: '0',
      total_cache_write_input_tokens: '0',
      total_cache_read_input_tokens: '0',
      total_output_tokens: '0',
      total_prompt_tokens: '0',
      total_completion_tokens: '0',
      total_unknown_usage_requests: '0',
    },
  },
};

const publicConfig = {
  site_name: 'Fixture Site',
  site_logo_url: '',
  charity_donation_notice_zh: '',
  charity_donation_notice_en: '',
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
