import { expect, test, type Page } from './test';
import { collectConsoleViolations, mockPublicConfig, mockRoleSession } from './support';
import { USER_ORIGIN } from './ports';
import { gamesSnapshotWire } from '../../src/user/games/common/testFixtures';
import { catalogWire } from '../../src/user/games/likes/testCatalog';
import { tutorialData } from '../../src/user/games/likes/tutorial/data';
import { tutorialSteps } from '../../src/user/games/likes/tutorial/steps';
import { tutorialStorageKey } from '../../src/user/games/likes/tutorial/storage';

async function setup(page: Page) {
  await mockRoleSession(page, 'user', 'user');
  await mockPublicConfig(page, 'user');
  let queued = false;
  const writes: string[] = [];
  await page.route('**/api/games**', async (route) => {
    const request = route.request(),
      path = new URL(request.url()).pathname;
    if (request.method() !== 'GET') writes.push(path);
    if (path === '/api/games') return route.fulfill({ json: gamesSnapshotWire() });
    if (path.endsWith('/catalog'))
      return route.fulfill({
        json: catalogWire({ quick: tutorialData.contentHash, standard: 'b'.repeat(64) }),
      });
    if (path.endsWith('/state'))
      return route.fulfill({
        json: {
          server_now: 1800000000,
          current: null,
          latest_result: null,
          queue: queued
            ? {
                mode: 'quick',
                id: 'likq_AAAAAAAAAAAAAAAAAAAAAA',
                revision: '1',
                deadline: 1800000110,
                ticket: '1',
                payment: { general: '1', game: '0' },
                terms_hash: 'a'.repeat(64),
                rules_version: 1,
                loadout: tutorialData.selection,
              }
            : null,
        },
      });
    return route.fallback();
  });
  await page.goto(`${USER_ORIGIN}/games/likes`);
  return {
    writes,
    queue: () => {
      queued = true;
    },
  };
}

for (const mobile of [false, true])
  test(`local tutorial completes ten authoritative rounds ${mobile ? 'on mobile' : 'on desktop'}`, async ({
    page,
  }) => {
    test.setTimeout(120000);
    const errors = collectConsoleViolations(page);
    await page.setViewportSize({ width: mobile ? 390 : 1440, height: mobile ? 844 : 1000 });
    await page.emulateMedia({
      reducedMotion: mobile ? 'reduce' : 'no-preference',
      colorScheme: mobile ? 'dark' : 'light',
    });
    const { writes } = await setup(page);
    await page.getByRole('button', { name: 'Start tutorial', exact: true }).click();
    const dialog = page.locator('.likes-tutorial');
    await expect(dialog).toBeVisible();
    await page.clock.install();
    const steps = tutorialSteps((_zh, en) => en);
    for (const [index, step] of steps.entries()) {
      await expect(dialog.locator('.likes-tutorial-tip h3')).toHaveText(step.title);
      if (step.kind === 'finish') break;
      if (step.kind === 'resolution') {
        await page.clock.runFor(tutorialData.rounds[step.round - 1].seconds * 1000 + 200);
        continue;
      }
      const target =
        step.target === 'continue'
          ? dialog.locator('[data-tutorial-next]')
          : dialog.locator('[data-guide-active]');
      if (step.target !== 'continue') {
        await expect(target).toHaveCount(1);
        expect(
          await dialog
            .locator('.likes-tutorial-surface button:not([inert]):not([data-guide-active])')
            .count(),
        ).toBe(0);
      }
      if ([1, 7, 16, 45].includes(index))
        await page.screenshot({
          path: `../tmp/tutorial-${mobile ? 'mobile' : 'desktop'}-${index}.png`,
        });
      if (step.value) await target.selectOption(step.value);
      else await target.click();
    }
    await expect(dialog.locator('.likes-tutorial-tip h3')).toContainText('66–61');
    expect(await page.evaluate((key) => localStorage.getItem(key), tutorialStorageKey)).toBe(
      'completed',
    );
    await page.getByRole('button', { name: 'Use teaching loadout' }).click();
    await expect(dialog).toHaveCount(0);
    await expect(page.locator('.likes-skill-option input:checked')).toHaveCount(5);
    expect(writes).toEqual([]);
    errors.assertNone();
  });

test('skipping persists, replay and refresh restart, unavailable storage remains usable', async ({
  page,
}) => {
  const { writes } = await setup(page);
  await page.getByRole('button', { name: 'Start tutorial', exact: true }).click();
  await page.locator('[data-tutorial-next]').click();
  await page.getByRole('button', { name: 'Skip tutorial', exact: true }).click();
  expect(await page.evaluate((key) => localStorage.getItem(key), tutorialStorageKey)).toBe(
    'skipped',
  );
  await page.reload();
  await expect(page.locator('.likes-tutorial-invite')).toHaveCount(0);
  await page.getByRole('button', { name: 'Tutorial', exact: true }).click();
  await expect(page.locator('.likes-tutorial-tip h3')).toHaveText('Let’s play a practice match');
  await page.locator('[data-tutorial-next]').click();
  await page.reload();
  await page.getByRole('button', { name: 'Tutorial', exact: true }).click();
  await expect(page.locator('.likes-tutorial-tip h3')).toHaveText('Let’s play a practice match');
  await page.evaluate(() => {
    Storage.prototype.setItem = () => {
      throw new Error('storage unavailable');
    };
  });
  await page.getByRole('button', { name: 'Skip tutorial', exact: true }).click();
  await expect(page.locator('.likes-tutorial')).toHaveCount(0);
  expect(writes).toEqual([]);
});

test('an actual queue discovered while teaching takes precedence immediately', async ({ page }) => {
  const errors = collectConsoleViolations(page);
  const fixture = await setup(page);
  await page.getByRole('button', { name: 'Start tutorial', exact: true }).click();
  await expect(page.locator('.likes-tutorial')).toBeVisible();
  fixture.queue();
  await expect(page.locator('.likes-tutorial')).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Tutorial', exact: true })).toBeDisabled();
  expect(fixture.writes).toEqual([]);
  errors.assertNone();
});

test('Chinese rules support related reading and versions on a narrow dark screen', async ({
  page,
}) => {
  await page.addInitScript(() => {
    localStorage.setItem('nb.lang', 'zh');
    localStorage.setItem('nb.theme', 'dark');
  });
  await page.setViewportSize({ width: 390, height: 844 });
  await setup(page);
  await page
    .locator('.likes-skill-option')
    .filter({ has: page.locator('[data-guide="equip:GPT01"]') })
    .getByRole('button')
    .click();
  const reader = page.locator('.likes-reader');
  await expect(reader).toBeVisible();
  await reader.getByRole('button', { name: '蒸馏 II', exact: true }).click();
  await reader.locator('.likes-reader-main .likes-term').first().click();
  await reader.getByRole('button', { name: '← 返回上一词条' }).click();
  await expect(reader.getByRole('button', { name: '蒸馏 II', exact: true })).toHaveAttribute(
    'aria-pressed',
    'true',
  );
  await expect(reader.locator('.likes-flavor blockquote')).toBeVisible();
  await expect(reader).not.toContainText('Buff ID');
  await page.screenshot({ path: '../tmp/player-guide-zh-mobile.png' });
});
