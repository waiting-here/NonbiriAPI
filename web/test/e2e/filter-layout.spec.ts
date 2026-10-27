import { resolve } from 'node:path';
import { expect, test } from './test';
import { USER_ORIGIN } from './ports';
import { collectConsoleViolations, mockPublicConfig, mockRoleSession } from './support';
import { gachaSnapshot } from '../fixtures/gacha';

for (const theme of ['light', 'dark'] as const) {
  test(`collection filters stay compact and preserve selections ${theme}`, async ({ page }) => {
    const guard = collectConsoleViolations(page);
    await page.emulateMedia({ reducedMotion: 'reduce' });
    await page.addInitScript((value) => {
      localStorage.setItem('nb.lang', 'en');
      localStorage.setItem('nb.theme', value);
      localStorage.setItem(
        'nb.gacha.preferences',
        JSON.stringify({ music: false, sound: false, reduced: true }),
      );
    }, theme);
    await mockRoleSession(page, 'user', 'user');
    await mockPublicConfig(page, 'user');
    const snapshot = gachaSnapshot();
    snapshot.visible_themes = ['a', 'b', 'c', 'g'];
    await page.route('**/api/events', (route) =>
      route.fulfill({ contentType: 'text/event-stream', body: ': connected\n\n' }),
    );
    await page.route('**/api/appearance', (route) =>
      route.fulfill({
        json: {
          selected: 'default',
          visible: ['default'],
          unlocked: ['default'],
          updated_at: null,
          moment: 'cover:permanent',
          moments: ['cover:permanent'],
        },
      }),
    );
    await page.route('**/api/gacha/state', (route) => route.fulfill({ json: snapshot }));
    await page.route('**/api/gacha/actions', (route) => {
      expect(route.request().postDataJSON().action).toBe('enter');
      return route.fulfill({ json: { snapshot } });
    });
    await page.route('**/api/gacha/entry', (route) =>
      route.fulfill({ json: { enabled: true, entered: true, pools: [] } }),
    );
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.goto(USER_ORIGIN + '/activities/gacha');
    await page.getByRole('tab', { name: 'Collection', exact: true }).click();
    const filters = page.getByRole('form', { name: 'Find cards' });
    const character = filters.getByRole('combobox', { name: 'Character', exact: true });
    await expect(character).toBeVisible();
    const search = filters.getByRole('textbox', { name: 'Search', exact: true });
    await search.fill('ChatGPT');
    await character.selectOption('ChatGPT');
    for (const width of [1440, 900, 390]) {
      await page.setViewportSize({ width, height: 900 });
      const more = filters.locator('summary');
      if (width === 390) {
        await expect(character).toBeHidden();
        await more.click();
      }
      await expect(character).toBeVisible();
      await expect(character).toHaveValue('ChatGPT');
      await expect(search).toHaveValue('ChatGPT');
      const boxes = await filters.evaluate((element) => ({
        filter: element.getBoundingClientRect().toJSON(),
        search: element.querySelector('.nb-filter__search')!.getBoundingClientRect().toJSON(),
        fields: [...element.querySelectorAll('.nb-filter__fields .nb-field')].map((field) =>
          field.getBoundingClientRect().toJSON(),
        ),
      }));
      expect(boxes.search.y).toBeLessThan(boxes.fields[0].y);
      expect(Math.abs(boxes.fields[0].x - boxes.search.x)).toBeLessThan(2);
      if (width > 768) {
        expect(Math.max(...boxes.fields.map((box) => box.y)) - boxes.fields[0].y).toBeLessThan(2);
        expect(boxes.filter.height).toBeLessThan(230);
      }
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
        true,
      );
      if (process.env.NONBIRI_VISUAL_DIR)
        await filters.screenshot({
          path: resolve(process.env.NONBIRI_VISUAL_DIR, `collection-filters-${theme}-${width}.png`),
        });
      if (width === 390) {
        await more.click();
        await expect(character).toBeHidden();
        await more.click();
        await expect(character).toHaveValue('ChatGPT');
      }
    }
    guard.assertNone();
  });
}
