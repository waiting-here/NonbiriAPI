import type { Page } from './test';
import { appearanceFixtures } from './appearance-fixtures';
import { userSession } from './support';
import { gamesSnapshotWire } from '../../src/user/games/common/testFixtures';

const now = 1_800_000_000;

export async function creditDisplayFixtures(page: Page, design: string, balance = '0.001') {
  await appearanceFixtures(page, design);
  const session = userSession('level6');
  session.user.balance = balance;
  session.user.game_balance = '0.001';
  session.user.donation_credit = '0.001';
  await page.route('**/api/session', (route) => route.fulfill({ json: session }));
  await page.route('**/api/me', (route) => route.fulfill({ json: session }));
  await page.route('**/api/checkin*', (route) =>
    route.fulfill({
      json: {
        enabled: true,
        mutually_exclusive: true,
        blocked_by_other_checkin: false,
        asset_type: new URL(route.request().url()).pathname.endsWith('/game') ? 'game' : 'general',
        checked_in_today: false,
        balance: '0.001',
        award_min: '0.001',
        award_max: '0.001',
        balance_cap: '250000',
      },
    }),
  );
  await page.route('**/api/credits/history*', (route) =>
    route.fulfill({
      json: {
        data: [
          {
            operation_id: 'op_' + 'A'.repeat(22),
            line: 1,
            asset_type: 'general',
            kind: 'checkin_award',
            delta: '0.001',
            created_at: now,
            request_id: null,
          },
          {
            operation_id: 'op_' + 'A'.repeat(21) + 'Q',
            line: 1,
            asset_type: 'general',
            kind: 'fishing_reserve',
            delta: '-0.001',
            created_at: now - 1,
            request_id: null,
          },
        ],
        page: '1',
        page_size: 20,
        total: '2',
        total_pages: '1',
        anchor: 'op_' + 'A'.repeat(22),
        current_balance: balance,
        game_balance: '0.001',
        server_now: now,
      },
    }),
  );
  await page.route('**/api/games', (route) => {
    const snapshot = gamesSnapshotWire();
    snapshot.balance = balance;
    snapshot.game_balance = '0.001';
    snapshot.fishing.bait_prices = { worm: '0.001', lure: '0.002', premium: '0.003' };
    return route.fulfill({ json: snapshot });
  });
  await page.route('**/api/games/fishing/state', (route) =>
    route.fulfill({
      json: {
        settlement_pending: null,
        unrevealed: null,
        has_more_unrevealed: false,
      },
    }),
  );
  await page.route('**/api/games/fishing/leaderboard?*', (route) => {
    const board = new URL(route.request().url()).searchParams.get('board');
    return route.fulfill({
      json: {
        board,
        window_start: board === 'single' ? null : now - 30 * 86400,
        entries: [],
        me: null,
      },
    });
  });
}
