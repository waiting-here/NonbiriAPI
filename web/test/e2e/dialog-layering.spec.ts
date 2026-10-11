import { penaltyPage } from '../fixtures/penalties';
import { gamesSnapshotWire } from '../../src/user/games/common/testFixtures';
import { mkdirSync } from 'node:fs';
import { join } from 'node:path';
import { expect, test, type Page } from './test';
import { ADMIN_ORIGIN, USER_ORIGIN } from './ports';
import {
  collectConsoleViolations,
  mockPublicConfig,
  mockRoleSession,
  userSession,
} from './support';
import en from '../../src/shared/i18n/common/en.json' with { type: 'json' };
import zh from '../../src/shared/i18n/common/zh.json' with { type: 'json' };

async function expectUnobscured(dialog: ReturnType<Page['locator']>) {
  await expect(dialog).toBeVisible();
  expect(
    await dialog.evaluate((element) => {
      const bounds = element.getBoundingClientRect();
      return [0.1, 0.5, 0.9].every((x) =>
        [0.15, 0.5, 0.85].every((y) =>
          element.contains(
            document.elementFromPoint(
              bounds.left + bounds.width * x,
              bounds.top + bounds.height * y,
            ),
          ),
        ),
      );
    }),
  ).toBe(true);
}

for (const { station, design } of [
  { station: 'admin', design: 'default' },
  { station: 'user', design: 'default' },
  { station: 'user', design: 'celestial' },
  { station: 'user', design: 'wandering' },
] as const)
  for (const theme of ['light', 'dark'] as const)
    for (const width of [1440, 390]) {
      test(`user confirmations clear table headers and navigation: ${station} ${design} ${theme} ${width}`, async ({
        page,
      }) => {
        const language = theme === 'dark' ? 'zh' : 'en';
        const copy = (language === 'zh' ? zh : en).management.users;
        const origin = station === 'admin' ? ADMIN_ORIGIN : USER_ORIGIN;
        const api = station === 'admin' ? '/admin/api' : '/api/steward';
        const guard = collectConsoleViolations(page);
        await page.setViewportSize({ width, height: 900 });
        await page.emulateMedia({ reducedMotion: 'reduce' });
        await page.addInitScript(
          ({ language, theme }) => {
            localStorage.setItem('nb.lang', language);
            localStorage.setItem('nb.theme', theme);
          },
          { language, theme },
        );
        await mockPublicConfig(page, station);
        await mockRoleSession(page, station, station === 'admin' ? 'admin' : 'level6');
        if (station === 'user')
          await page.route('**/api/appearance', (route) =>
            route.fulfill({
              json: {
                selected: design,
                unlocked: ['default', design],
                visible: ['default', design],
                updated_at: null,
              },
            }),
          );
        await page.route('**/api/events', (route) =>
          route.fulfill({
            contentType: 'text/event-stream',
            body: ': connected\n\n',
          }),
        );
        const target = {
          ...Object.fromEntries(
            Object.entries(userSession('user').user).filter(
              ([key]) =>
                ![
                  'avatar',
                  'effective_level',
                  'level_display_name',
                  'charity_profile_public',
                  'automatic_restrictions',
                ].includes(key),
            ),
          ),
          id: '7',
          username: 'Managed member',
          discord_id: null,
          is_admin: false,
          discord_gate_policy: 'inherit',
          banned_reason: '',
          level: { manual: null, automatic: 1, effective: 1, display_name: 'Lv1' },
          revision: '1',
          game_balance: '0',
        };
        const pageBody = (data: unknown[]) => ({
          data,
          next_cursor: null,
          pagination: {
            page: '1',
            page_size: 20,
            total_items: String(data.length),
            total_pages: '1',
          },
        });
        let writes = 0;
        await page.route(`**${api}/users**`, async (route) => {
          const request = route.request(),
            path = new URL(request.url()).pathname;
          if (request.method() !== 'GET') {
            writes++;
            return route.fulfill({
              status: 500,
              json: { error: { code: 'internal_error', message: 'Unexpected write.' } },
            });
          }
          if (path === api + '/users')
            return route.fulfill({
              json: pageBody(
                Array.from({ length: 20 }, (_, i) => ({
                  ...target,
                  id: String(i + 7),
                  username: i ? `Member ${i}` : target.username,
                })),
              ),
            });
          if (path === api + '/users/7') return route.fulfill({ json: target });
          if (path.endsWith('/penalties'))
            return route.fulfill({
              json: { ...penaltyPage([]), legacy_details_unavailable: false },
            });
          return route.fallback();
        });
        await page.goto(
          origin + (station === 'admin' ? '/users?user=7' : '/steward?tab=users&user=7'),
        );
        await page.getByRole('tab', { name: copy.tabPenalties, exact: true }).click();
        await page.getByLabel(copy.banReason, { exact: true }).fill('Synthetic review');
        const trigger = page.getByRole('button', { name: copy.ban, exact: true });
        await expect(
          page.getByText(
            language === 'zh'
              ? '服务端返回了无效响应。'
              : 'The service returned an invalid response.',
            { exact: true },
          ),
        ).toHaveCount(0);
        await trigger.click();
        const dialog = page.getByRole('alertdialog', { name: copy.banTitle });
        await expect(dialog).toBeVisible();
        if (width === 1440) {
          // Keep the real sticky table header crossing the confirmation, as when
          // the page is scrolled independently from its user-detail column.
          await page.evaluate(() => {
            const header = document.querySelector('.user-management .nb-table th');
            if (header)
              window.scrollBy(0, header.getBoundingClientRect().top - (innerHeight / 2 - 35));
          });
        }
        await expectUnobscured(dialog);
        if (station === 'user') {
          await expect(page.locator('html')).toHaveAttribute('data-appearance', design);
          expect(
            await dialog.evaluate(
              (element) =>
                getComputedStyle(element).color === getComputedStyle(document.body).color,
            ),
          ).toBe(true);
        }
        const cancel = dialog.getByRole('button', {
          name: language === 'zh' ? '取消' : 'Cancel',
          exact: true,
        });
        await expect(cancel).toBeFocused();
        await page.keyboard.press('Shift+Tab');
        await expect(
          dialog.getByRole('button', { name: copy.banConfirm, exact: true }),
        ).toBeFocused();
        await page.keyboard.press('Tab');
        await expect(cancel).toBeFocused();
        const folder = process.env.NONBIRI_OVERLAY_SCREENSHOTS;
        if (folder) {
          mkdirSync(folder, { recursive: true });
          await page.screenshot({
            path: join(folder, `ban-${station}-${design}-${theme}-${width}.png`),
          });
        }
        await page.keyboard.press('Escape');
        await expect(dialog).toBeHidden();
        await expect(trigger).toBeFocused();
        await trigger.click();
        await dialog
          .getByRole('button', { name: language === 'zh' ? '取消' : 'Cancel', exact: true })
          .click();
        await expect(dialog).toBeHidden();
        await page.getByRole('tab', { name: copy.tabEconomy, exact: true }).click();
        await page.getByLabel(copy.economyPositiveAmount, { exact: true }).fill('1');
        await page.getByLabel(copy.economyReason, { exact: true }).fill('Synthetic review');
        await page.getByRole('button', { name: copy.economySubmit, exact: true }).click();
        const economy = page.getByRole('alertdialog', { name: copy.economyConfirmTitle });
        await expectUnobscured(economy);
        await economy
          .getByRole('button', { name: language === 'zh' ? '取消' : 'Cancel', exact: true })
          .click();
        if (station === 'admin') {
          await page.getByRole('button', { name: copy.delete, exact: true }).click();
          const deletion = page.getByRole('alertdialog', { name: copy.deleteTitle });
          await expectUnobscured(deletion);
          await expect(deletion.getByLabel(copy.elevatedPassword)).toBeVisible();
          await expect(
            deletion.getByRole('button', { name: copy.deleteConfirm, exact: true }),
          ).toBeDisabled();
          await deletion
            .getByRole('button', { name: language === 'zh' ? '取消' : 'Cancel', exact: true })
            .click();
        }
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
          true,
        );
        expect(writes).toBe(0);
        guard.assertNone();
      });
    }

for (const theme of ['light', 'dark'] as const)
  for (const width of [1440, 390]) {
    test(`game overlays cover the site header: ${theme} ${width}`, async ({ page }) => {
      const guard = collectConsoleViolations(page);
      await page.setViewportSize({ width, height: 900 });
      await page.addInitScript(
        ({ theme }) => {
          localStorage.setItem('nb.lang', 'en');
          localStorage.setItem('nb.theme', theme);
        },
        { theme },
      );
      await mockPublicConfig(page, 'user');
      await mockRoleSession(page, 'user', 'user');
      await page.route('**/api/events', (route) =>
        route.fulfill({ contentType: 'text/event-stream', body: ': connected\n\n' }),
      );
      const snapshot = gamesSnapshotWire();
      const state = {
        session_id: `ll_${'A'.repeat(22)}`,
        spec: '6x8',
        state: 'active',
        price: '3',
        revision: '1',
        board: {
          rows: 6,
          cols: 8,
          tiles: Array.from({ length: 48 }, (_, i) => ({
            row: Math.floor(i / 8),
            col: i % 8,
            tile_key: `tile_${String(Math.floor(i / 4) + 1).padStart(2, '0')}`,
            removed: false,
          })),
        },
        pairs_removed: 0,
        total_pairs: 24,
        started_at: 1800000000,
        deadline: 1800000150,
        server_now: 1800000010,
      };
      await page.route('**/api/games**', (route) => {
        const url = new URL(route.request().url()),
          path = url.pathname;
        if (path === '/api/games') return route.fulfill({ json: snapshot });
        if (path === '/api/games/rps/state')
          return route.fulfill({
            json: { kind: 'idle', tutorial_seen: false, modes: snapshot.rps.modes },
          });
        if (path.endsWith('/leaderboard'))
          return route.fulfill({
            json: {
              mode: url.searchParams.get('mode'),
              board: url.searchParams.get('board'),
              window_days: 30,
              window_start: 1700000000,
              min_sessions: 10,
              rows: [],
              me: null,
            },
          });
        if (path === '/api/games/linklink/session') return route.fulfill({ json: state });
        if (path.endsWith('/lease')) return route.fulfill({ json: { expires_at: 1800000035 } });
        return route.fallback();
      });
      async function inspect(selector: string, name: string) {
        const overlay = page.locator(selector);
        await expectUnobscured(overlay);
        expect(
          await overlay.evaluate((element) => element.contains(document.elementFromPoint(20, 20))),
        ).toBe(true);
        const folder = process.env.NONBIRI_OVERLAY_SCREENSHOTS;
        if (folder) {
          mkdirSync(folder, { recursive: true });
          await page.screenshot({ path: join(folder, `${name}-${theme}-${width}.png`) });
        }
      }
      await page.goto(USER_ORIGIN + '/games/rps');
      await inspect('.rps-modal', 'rps-tutorial');
      await page.getByRole('button', { name: 'Skip for now', exact: true }).click();
      const rules = page.getByRole('button', { name: 'How to play', exact: true });
      await rules.click();
      await inspect('.game-rules-modal', 'game-rules');
      await page.keyboard.press('Escape');
      await expect(page.getByRole('dialog')).toHaveCount(0);
      await expect(rules).toBeFocused();
      await page.goto(USER_ORIGIN + '/games/linklink');
      await page.locator('.linklink-abandon').click();
      await inspect('.linklink-modal', 'linklink-abandon');
      await page.locator('.linklink-modal .nb-btn--secondary').click();
      await expect(page.locator('.linklink-modal')).toHaveCount(0);
      await expect(page.locator('.linklink-tile')).toHaveCount(48);
      guard.assertNone();
    });
  }
