import { screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../test/unit/support';
import { limitedActivity, activityExchange } from '../../../../test/fixtures/limitedActivities';
import { PictureBookPage, LimitedActivitiesSection } from './LimitedActivities';
function reply(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}
const session = {
  user: {
    id: '1',
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
    game_balance: '0',
    donation_credit: '0',
    effective_level: 1,
    level_display_name: 'Lv1',
    game_profile_public: false,
    charity_profile_public: false,
    automatic_restrictions: [],
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
describe('limited activity user experience', () => {
  it('opens a hidden activity directly and reuses an uncertain exchange', async () => {
    const writes: { key: string | null; body: string }[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const path = String(input);
        if (path.endsWith('/session')) return reply(session);
        if (path.endsWith('/wallet'))
          return reply({ general: '10000', sketch_paper: '0', sketch_brush: '0' });
        if (path.endsWith('/exchange')) {
          writes.push({
            key: new Headers(init?.headers).get('Idempotency-Key'),
            body: String(init?.body),
          });
          if (writes.length === 1) throw new TypeError('connection lost');
          return reply(activityExchange());
        }
        return reply(limitedActivity());
      }),
    );
    const rendered = await renderWithProviders(<PictureBookPage />, {
      station: 'user',
      role: 'user',
    });
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Confirm exchange' })).toBeEnabled(),
    );
    await rendered.user.click(screen.getByRole('button', { name: 'Confirm exchange' }));
    await screen.findByText(/previous result is unconfirmed/i);
    expect(screen.getByLabelText('Quantity')).toBeDisabled();
    await rendered.user.click(screen.getByRole('button', { name: 'Retry the same exchange' }));
    await screen.findByText(/Exchanged 1 Sketch paper/);
    expect(writes).toHaveLength(2);
    expect(writes[0]).toEqual(writes[1]);
  });
  it('uses the public directory and shows no entry when it is hidden', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) =>
        String(input).endsWith('/session') ? reply(session) : reply([]),
      ),
    );
    await renderWithProviders(<LimitedActivitiesSection />, { station: 'user', role: 'user' });
    await screen.findByText('No limited-time activities are listed.');
    expect(screen.queryByRole('link', { name: 'View activity' })).toBeNull();
  });
});
