import { mkdirSync } from 'node:fs';
import { resolve } from 'node:path';
import { expect, test } from './test';
import { ADMIN_ORIGIN, USER_ORIGIN } from './ports';
import { mockPublicConfig, mockRoleSession, userSession } from './support';

for (const station of ['admin', 'user'] as const)
  for (const mode of ['light', 'dark'])
    for (const width of [1440, 390])
      test(`user detail leading return target ${station} ${mode} ${width}`, async ({ page }) => {
        await page.setViewportSize({ width, height: 900 });
        await page.addInitScript((mode) => {
          localStorage.setItem('nb.lang', 'zh');
          localStorage.setItem('nb.theme', mode);
        }, mode);
        await mockPublicConfig(page, station);
        await mockRoleSession(page, station, station === 'admin' ? 'admin' : 'level6');
        const origin = station === 'admin' ? ADMIN_ORIGIN : USER_ORIGIN;
        const api = station === 'admin' ? '/admin/api' : '/api/steward';
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
          game_balance: '-2.5',
        };
        await page.route('**/*', async (route) => {
          const url = new URL(route.request().url());
          if (url.origin !== origin) return route.fallback();
          if (url.pathname === api + '/users')
            return route.fulfill({
              json: {
                data: [target],
                next_cursor: null,
                pagination: { page: '1', page_size: 20, total_items: '1', total_pages: '1' },
              },
            });
          if (url.pathname === api + '/users/7') return route.fulfill({ json: target });
          return route.fallback();
        });
        await page.goto(
          origin + (station === 'admin' ? '/users?user=7' : '/steward?tab=users&user=7'),
        );
        const back = page.locator('.user-authority__back');
        await expect(back).toBeVisible();
        const box = (await back.boundingBox())!;
        expect(box.width).toBeGreaterThanOrEqual(40);
        expect(box.height).toBeGreaterThanOrEqual(40);
        await page.keyboard.press('Tab');
        await back.evaluate((element) => element.scrollIntoView({ block: 'center' }));
        await back.focus();
        await expect(back).toBeFocused();
        expect(
          await back.evaluate((element) => {
            const rect = element.getBoundingClientRect();
            const header = document.querySelector('.site-header')?.getBoundingClientRect();
            const hit = document.elementFromPoint(
              rect.x + rect.width / 2,
              rect.y + rect.height / 2,
            );
            return (
              rect.top >= (header?.bottom ?? 0) &&
              rect.bottom <= innerHeight &&
              !!hit &&
              element.contains(hit)
            );
          }),
        ).toBe(true);
        expect(await back.evaluate((element) => getComputedStyle(element).boxShadow)).not.toBe(
          'none',
        );
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
          true,
        );
        if (process.env.NONBIRI_VISUAL_DIR) {
          mkdirSync(process.env.NONBIRI_VISUAL_DIR, { recursive: true });
          await page.screenshot({
            path: resolve(
              process.env.NONBIRI_VISUAL_DIR,
              `user-return-${station}-${mode}-${width}.png`,
            ),
          });
        }
        await page.keyboard.press('Enter');
        await expect(page.locator('.user-authority')).toHaveCount(0);
        await expect(
          page.getByRole('button', { name: 'Managed member', exact: true }),
        ).toBeVisible();
      });
