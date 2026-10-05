import { expect, test } from './test';
import { collectConsoleViolations, mockPublicConfig, mockRoleSession } from './support';
import { USER_ORIGIN } from './ports';
import { gamesSnapshotWire } from '../../src/user/games/common/testFixtures';
import { aiTerms } from '../fixtures/aiPlayers';

for (const width of [390, 1440]) {
  test('AI unpaid admission and memory preference at ' + width, async ({ page }) => {
    const errors = collectConsoleViolations(page);
    await mockRoleSession(page, 'user', 'user');
    await mockPublicConfig(page, 'user');
    await page.setViewportSize({ width, height: 900 });
    const snapshot = gamesSnapshotWire();
    snapshot.bidding.enabled = true;
    let memory = true,
      queued = false;
    const writes: unknown[] = [];
    const now = Math.floor(Date.now() / 1000),
      id = 'aiq_AAAAAAAAAAAAAAAAAAAAAA';
    await page.route('**/api/games**', async (route) => {
      const req = route.request(),
        path = new URL(req.url()).pathname;
      if (path === '/api/games') return route.fulfill({ json: snapshot });
      if (path === '/api/games/bidding/ai')
        return route.fulfill({
          json: {
            enabled: true,
            bots: [
              {
                terms: { ticket: '0', ai: aiTerms },
                terms_hash: 'a'.repeat(64),
                completed: false,
                memory_enabled: memory,
                memory_samples: 0,
              },
            ],
          },
        });
      if (path === '/api/games/bidding/ai/preference') {
        const body = req.postDataJSON();
        memory = body.memory_enabled;
        return route.fulfill({ json: body });
      }
      if (path === '/api/games/bidding/queue' && req.method() === 'POST') {
        writes.push(req.postDataJSON());
        queued = true;
        return route.fulfill({
          status: 202,
          json: { queue_id: id, revision: '1', deadline: now + 120 },
        });
      }
      if (path.endsWith('/' + id) && req.method() === 'DELETE') {
        queued = false;
        return route.fulfill({ status: 204 });
      }
      if (path === '/api/games/bidding/state')
        return route.fulfill({
          json: {
            server_now: now,
            current: null,
            latest_result: null,
            queue: queued
              ? {
                  id,
                  revision: '1',
                  mode: 'ai',
                  deadline: now + 120,
                  ticket: '0',
                  payment: { general: '0', game: '0' },
                  rules_version: 1,
                  terms_hash: 'a'.repeat(64),
                  economy: 'ai_challenge',
                  ai: aiTerms,
                  position: 1,
                }
              : null,
          },
        });
      return route.fallback();
    });
    await page.goto(USER_ORIGIN + '/games/bidding');
    await expect(page.getByRole('heading', { name: 'Patient', exact: true })).toBeVisible();
    const toggle = page.getByRole('switch', { name: 'Use match memory' });
    await toggle.click();
    await expect(toggle).not.toBeChecked();
    await page.getByRole('button', { name: 'Challenge', exact: true }).click();
    await expect(page.getByText('WAITING FOR AI MATCH')).toBeVisible();
    expect(writes).toEqual([
      { mode: 'ai', bot_id: aiTerms.bot_id, expected_terms_hash: 'a'.repeat(64) },
    ]);
    await expect(page.getByText(/No ticket is charged before admission/)).toBeVisible();
    await page.getByRole('button', { name: 'Leave queue', exact: true }).click();
    await expect(page.getByRole('button', { name: 'Challenge', exact: true })).toBeEnabled();
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
    ).toBe(true);
    errors.assertNone();
  });
}
