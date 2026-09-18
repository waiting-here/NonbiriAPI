import { expect, test } from './test';
import { mockPublicConfig, mockRoleSession } from './support';
import { USER_ORIGIN } from './ports';
import { gamesSnapshotWire } from '../../src/user/games/common/testFixtures';
import { catalogWire } from '../../src/user/games/likes/testCatalog';

for (const game of ['bidding', 'likes'] as const)
  test(`${game} respects direct access, partial opening and dynamic rejection`, async ({
    page,
  }) => {
    const errors: string[] = [];
    page.on('pageerror', (error) => errors.push(error.message));
    page.on('console', (message) => {
      if (
        message.type() === 'error' &&
        !/^Failed to load resource: the server responded with a status of 409 /.test(message.text())
      )
        errors.push(message.text());
    });
    await mockRoleSession(page, 'user', 'user');
    await mockPublicConfig(page, 'user');
    const snapshot = gamesSnapshotWire();
    const modes = Object.keys(snapshot[game].modes);
    const writes: string[] = [];
    await page.route('**/api/games**', async (route) => {
      const request = route.request(),
        path = new URL(request.url()).pathname;
      if (request.method() !== 'GET') {
        writes.push(path);
        snapshot[game].enabled = false;
        return route.fulfill({
          status: 409,
          json: { error: { code: 'conflict', message: 'changed' } },
        });
      }
      if (path === '/api/games') return route.fulfill({ json: snapshot });
      if (path.endsWith('/catalog')) return route.fulfill({ json: catalogWire() });
      if (path.endsWith('/state'))
        return route.fulfill({
          json: { server_now: 1800000000, current: null, queue: null, latest_result: null },
        });
      return route.fallback();
    });
    await page.goto(`${USER_ORIGIN}/games/${game}`);
    await expect(
      page.getByRole('button', { name: 'This game is not open', exact: true }),
    ).toBeDisabled();
    snapshot[game].enabled = true;
    snapshot[game].modes[modes[0]].enabled = true;
    await page.reload();
    const buttons = page.locator(game === 'likes' ? '.likes-modes button' : '.bid-modes button');
    await expect(buttons.first()).toBeEnabled();
    await expect(buttons.nth(1)).toBeDisabled();
    const match = page.getByRole('button', { name: 'Pay entry and find a match', exact: true });
    await expect(match).toBeEnabled();
    await match.click();
    await expect(page.locator('.duel-feedback')).toContainText('This game is not open');
    await expect(
      page.getByRole('button', { name: 'This game is not open', exact: true }),
    ).toBeDisabled();
    expect(writes).toHaveLength(1);
    expect(errors).toEqual([]);
  });
