import { expect, test } from './test';
import { collectConsoleViolations, mockPublicConfig, mockRoleSession } from './support';
import { USER_ORIGIN } from './ports';
import { gamesSnapshotWire } from '../../src/user/games/common/testFixtures';
import { biddingViewWire } from '../../src/user/games/bidding/testFixtures';
import type { Match, Preferences } from '../../src/user/games/companion/api';

for (const width of [390, 1440])
  for (const theme of ['light', 'dark']) {
    test(
      'free companion restores a locked bid and keeps consent independent at ' +
        width +
        ' ' +
        theme,
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
        snapshot.games_enabled = true;
        snapshot.bidding.enabled = true;
        const prefs: Preferences = { memory: 'none', contribute: false };
        let active: Match | null = null,
          latest: Match | null = null;
        const path = '/api/games/bidding/companion';
        await page.route('**/api/games**', async (route) => {
          const req = route.request(),
            p = new URL(req.url()).pathname;
          if (p === '/api/games') return route.fulfill({ json: snapshot });
          if (p === '/api/games/bidding/state')
            return route.fulfill({
              json: { server_now: 11, queue: null, current: null, latest_result: null },
            });
          if (p === path) return route.fulfill({ json: { preferences: prefs, active, latest } });
          if (p === path + '/programs')
            return route.fulfill({
              json: {
                enabled: true,
                upload_available: true,
                official: { name: 'official', version: 0, difficulties: [1] },
                programs: [
                  {
                    name: 'Old private opponent',
                    version: 1,
                    versions: [{ version: 1, status: 'ready', created_at: 10 }],
                  },
                ],
              },
            });
          if (p === path + '/preferences') {
            Object.assign(prefs, req.postDataJSON());
            return route.fulfill({ json: prefs });
          }
          if (p === path + '/matches') {
            expect(req.headers()['idempotency-key']).toBeTruthy();
            expect(req.postDataJSON()).toEqual({
              mode: 'standard',
              program: 'official',
              difficulty: 1,
              constraints: {},
            });
            active = {
              id: 'match-1',
              game: 'bidding',
              mode: 'standard',
              status: 'active',
              program: 'official',
              version: 0,
              difficulty: 1,
              seat: 0,
              created_at: 11,
              ended_at: 0,
              winner: null,
              scores: [0, 0],
              reason: '',
              phase: 'bid',
              round: 1,
              ai_state: 'thinking',
              decision_id: 'move-1',
              space: { kind: 'enumerated', examples: [] },
              view: biddingViewWire(),
            };
            return route.fulfill({ json: active });
          }
          if (p.endsWith('/actions') && active) {
            expect(req.postDataJSON()).toEqual({
              decision_id: 'move-1',
              action: { kind: 'bid', card: 3 },
            });
            active.decision_id = '';
            active.view.own_selected_card = 3;
            return route.fulfill({ json: active });
          }
          if (p.endsWith('/abandon') && active) {
            latest = { ...active, status: 'abandoned', reason: 'abandoned' };
            active = null;
            return route.fulfill({ json: latest });
          }
          return route.fallback();
        });
        await page.goto(USER_ORIGIN + '/games/bidding');
        await page.getByRole('link', { name: 'Free AI companion' }).click();
        await expect(page.getByRole('heading', { name: 'Official companion' })).toBeVisible();
        await expect(page.getByRole('combobox', { name: 'Opponent', exact: true })).toHaveCount(0);
        await expect(
          page.getByText(/private programs|old private opponent|upload|compiler diagnostics/i),
        ).toHaveCount(0);
        await page.getByText('Memory and sharing', { exact: true }).click();
        await page.getByLabel('Personal match memory', { exact: true }).selectOption('opponent');
        await expect.poll(() => prefs.memory).toBe('opponent');
        expect(prefs.contribute).toBe(false);
        await page.getByRole('button', { name: 'Start free match' }).click();
        await expect(page.getByRole('status').filter({ hasText: 'AI is thinking' })).toBeVisible();
        const bidCard = page.getByRole('button', { name: /bid.*\(3\)/i });
        await expect(bidCard).toBeVisible();
        expect(
          await bidCard.evaluate((card) =>
            getComputedStyle(card).getPropertyValue('--bid-red').trim(),
          ),
        ).not.toBe('');
        await expect(page.getByText(/if time expires before you lock/i)).toHaveCount(0);
        await page.getByRole('button', { name: /bid.*\(3\)/i }).click();
        await page.getByRole('button', { name: /lock in bid/i }).click();
        await page.reload();
        await expect(page.getByRole('button', { name: /bid.*\(3\)/i })).toBeDisabled();
        if (process.env.NONBIRI_VISUAL_DIR)
          await page.screenshot({
            path: process.env.NONBIRI_VISUAL_DIR + '/companion-' + theme + '-' + width + '.png',
          });
        await page.getByRole('button', { name: 'Abandon match', exact: true }).click();
        await page
          .getByRole('alertdialog')
          .getByRole('button', { name: 'Abandon match', exact: true })
          .click();
        await expect(page.getByText('Match abandoned', { exact: true })).toBeVisible();
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
          true,
        );
        errors.assertNone();
      },
    );
  }
