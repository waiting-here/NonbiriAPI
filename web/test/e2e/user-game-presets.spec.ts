import { mkdir } from 'node:fs/promises';
import { resolve } from 'node:path';
import { test, expect } from './test';
import { mockPublicConfig, mockRoleSession } from './support';
import { USER_ORIGIN } from './ports';
import { gamesSnapshotWire } from '../../src/user/games/common/testFixtures';
import { catalogWire, testCatalog } from '../../src/user/games/likes/testCatalog';
import { initialSelection } from '../../src/user/games/likes/selection';

for (const locale of ['en', 'zh'] as const) {
  for (const theme of ['light', 'dark'] as const) {
    test(`preset workspace uses ordered summaries and direct names (${locale}, ${theme})`, async ({
      page,
    }) => {
      await page.addInitScript(
        ({ locale, theme }) => {
          localStorage.setItem('nb.lang', locale);
          localStorage.setItem('nb.theme', theme);
        },
        { locale, theme },
      );
      await mockRoleSession(page, 'user', 'user');
      await mockPublicConfig(page, 'user');
      const snapshot = gamesSnapshotWire();
      snapshot.likes.enabled = true;
      snapshot.likes.modes.quick.enabled = true;
      const loadout = { ...initialSelection(testCatalog.modes.quick), harness: 'H01' };
      const slots = Array.from({ length: 10 }, (_, index) => ({
        slot: index + 1,
        name: `LongPreset🐟${index + 1}星星星星星星`,
        revision: '1',
        mode: 'quick',
        loadout: structuredClone(loadout),
        updated_at: 1800000000,
      }));
      const renames: unknown[] = [];
      await page.route('**/api/games**', async (route) => {
        const request = route.request(),
          path = new URL(request.url()).pathname;
        if (path === '/api/games') return route.fulfill({ json: snapshot });
        if (path.endsWith('/catalog')) return route.fulfill({ json: catalogWire() });
        if (path.endsWith('/state'))
          return route.fulfill({
            json: { server_now: 1800000000, current: null, queue: null, latest_result: null },
          });
        if (path === '/api/games/likes/loadouts')
          return route.fulfill({ json: { capacity: 10, slots } });
        if (request.method() === 'PATCH') {
          const input = request.postDataJSON();
          renames.push(input);
          slots[0] = {
            ...slots[0],
            name: input.name,
            revision: String(Number(slots[0].revision) + 1),
          };
          return route.fulfill({ json: slots[0] });
        }
        return route.fallback();
      });
      const errors: string[] = [];
      page.on('pageerror', (error) => errors.push(error.message));
      await page.setViewportSize({ width: 1440, height: 1000 });
      await page.goto(`${USER_ORIGIN}/games/likes`);
      const workspace = page.locator('.likes-custom-presets');
      await expect(workspace.locator('article')).toHaveCount(10);
      const card = workspace.locator('article').first();
      await expect(card.locator('.likes-preset-skills li')).toHaveText(
        loadout.skills.map(
          (id) => testCatalog.modes.quick.skills.find((skill) => skill.id === id)!.name,
        ),
      );
      await expect(card).toContainText(
        testCatalog.modes.quick.harnesses.find((h) => h.id === 'H01')!.name,
      );
      const input = card.getByRole('textbox');
      await input.fill('🐟'.repeat(20));
      await input.press('Enter');
      await expect(card.locator('strong').first()).toHaveText('🐟'.repeat(20));
      expect(renames).toEqual([{ expected_revision: '1', name: '🐟'.repeat(20) }]);
      expect(slots[0].loadout).toEqual(loadout);
      await card
        .getByRole('button', { name: locale === 'zh' ? '清空短名' : 'Clear name', exact: true })
        .click();
      await expect(input).toHaveValue('');
      await expect(card.locator('strong').first()).toHaveText(
        locale === 'zh' ? '预设1' : 'Preset1',
      );
      expect(slots[0].loadout).toEqual(loadout);
      for (const [width, columns] of [
        [1440, 5],
        [900, 2],
        [390, 1],
      ] as const) {
        await page.setViewportSize({ width, height: 1000 });
        await workspace.scrollIntoViewIfNeeded();
        const layout = await workspace.evaluate((element) => {
          const grid = element.querySelector('.likes-custom-presets-grid')!;
          return {
            columns: getComputedStyle(grid).gridTemplateColumns.split(' ').length,
            documentFits: document.documentElement.scrollWidth <= innerWidth,
            cardsFit: [...grid.children].every((card) => card.scrollWidth <= card.clientWidth + 1),
          };
        });
        expect(layout).toEqual({ columns, documentFits: true, cardsFit: true });
        if (process.env.NONBIRI_VISUAL_DIR && width !== 900) {
          const directory = resolve(process.env.NONBIRI_VISUAL_DIR);
          await mkdir(directory, { recursive: true });
          const target = width === 1440 ? workspace : workspace.locator('article').nth(1);
          await target.screenshot({
            path: resolve(directory, 'presets-' + locale + '-' + theme + '-' + width + '.png'),
          });
        }
      }
      expect(errors).toEqual([]);
    });
  }
}
