import { expect, test } from './test';
import { collectConsoleViolations, mockPublicConfig, mockRoleSession } from './support';
import { USER_ORIGIN } from './ports';
import { gamesSnapshotWire } from '../../src/user/games/common/testFixtures';
import { aiTerms } from '../fixtures/aiPlayers';

for (const { width, theme } of [390, 1440].flatMap((width) =>
  ['light', 'dark'].map((theme) => ({ width, theme })),
)) {
  test(
    'AI selection, unpaid admission and memory at ' + width + ' in ' + theme,
    async ({ page }) => {
      const errors = collectConsoleViolations(page);
      await page.addInitScript((theme) => {
        localStorage.setItem('nb.theme', theme);
        localStorage.setItem('nb.lang', 'en');
      }, theme);
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
              bots: ['Patient', 'Balanced', 'Aggressive', 'Comeback'].map((name, index) => ({
                terms: {
                  ticket: '0',
                  ai: {
                    ...aiTerms,
                    bot_id:
                      index === 0
                        ? aiTerms.bot_id
                        : 'bot_' + String.fromCharCode(66 + index).repeat(22),
                    bot_name: name,
                  },
                },
                terms_hash: 'a'.repeat(64),
                completed: false,
                memory_enabled: memory,
                memory_samples: 0,
              })),
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
      await expect(
        page.getByRole('button', { name: 'Player matches', exact: true }),
      ).toHaveAttribute('aria-pressed', 'true');
      await expect(page.locator('.bid-ai-card')).toHaveCount(0);
      await page.getByRole('button', { name: 'AI players', exact: true }).click();
      const choices = page.getByRole('group', { name: 'Choose an AI opponent', exact: true });
      await expect(choices.getByRole('button')).toHaveCount(4);
      await choices.getByRole('button', { name: /Comeback/ }).click();
      await expect(page.getByRole('heading', { name: 'Comeback', exact: true })).toBeVisible();
      await choices.getByRole('button', { name: /Patient/ }).click();
      await expect(page.getByRole('heading', { name: 'Patient', exact: true })).toBeVisible();
      await expect(page.locator('.bid-ai-card')).toHaveCount(1);
      await expect(page.locator('.bid-ai-card')).toContainText('First-generation AI player');
      const card = page.locator('.bid-ai-card');
      expect(
        await card.evaluate((node) => {
          const style = getComputedStyle(node);
          return style.color !== style.backgroundColor && node.scrollWidth <= node.clientWidth + 1;
        }),
      ).toBe(true);
      if (process.env.NONBIRI_VISUAL_DIR) {
        await card.scrollIntoViewIfNeeded();
        await page.screenshot({
          path: process.env.NONBIRI_VISUAL_DIR + '/ai-players-' + theme + '-' + width + '.png',
          fullPage: true,
        });
      }
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
      await page.getByRole('button', { name: 'AI players', exact: true }).click();
      await expect(page.getByRole('button', { name: 'Challenge', exact: true })).toBeEnabled();
      expect(
        await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
      ).toBe(true);
      errors.assertNone();
    },
  );
}
