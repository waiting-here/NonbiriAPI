import { expect, test, type Page } from './test';
import { collectConsoleViolations, mockPublicConfig, mockRoleSession } from './support';
import { USER_ORIGIN } from './ports';
import { gamesSnapshotWire } from '../../src/user/games/common/testFixtures';
import { catalogWire, legacyCatalogWire } from '../../src/user/games/likes/testCatalog';
import wire from '../../src/user/games/likes/testdata/passives.json' with { type: 'json' };

const now = 1_800_000_000,
  id = 'lik_AAAAAAAAAAAAAAAAAAAAAA';
async function setup(page: Page, name: keyof typeof wire, offset = 0) {
  await mockRoleSession(page, 'user', 'user');
  await mockPublicConfig(page, 'user');
  const source = wire[name];
  const catalog = {
    ...catalogWire({ quick: wire.partial.content_hash, standard: wire.chain.content_hash }),
    compatible_modes: legacyCatalogWire(),
  };
  await page.route('**/api/games**', async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path === '/api/games') return route.fulfill({ json: gamesSnapshotWire() });
    if (path.endsWith('/catalog')) return route.fulfill({ json: catalog });
    if (path.endsWith('/rounds'))
      return route.fulfill({
        json: {
          items: [
            {
              round: 1,
              before: source.before,
              after: source.after,
              facts: source.facts,
              start_events: null,
              timeouts: [false, false],
            },
          ],
          next_cursor: null,
          server_now: now + offset,
        },
      });
    if (path.endsWith('/state'))
      return route.fulfill({
        json: {
          server_now: now + offset,
          queue: null,
          latest_result: null,
          current: {
            id,
            game: 'likes',
            mode: source.mode,
            rules_version: 1,
            content_hash: source.content_hash,
            revision: '2',
            phase_seq: '2',
            phase: 'settlement',
            round: 1,
            deadline: now + source.seconds,
            server_now: now + offset,
            you: 0,
            locked: [true, true],
            ticket: '1',
            rake_bp: { platform: 0, welfare: 0, thursday: 0 },
            own_payment: { general: '1', game: '0' },
            profiles: [{ kind: 'anonymous' }, { kind: 'anonymous' }],
            view: source.after,
            resolution: {
              round: 1,
              started_at: now,
              ends_at: now + source.seconds,
              summary: source.summary,
            },
            round_start: null,
          },
        },
      });
    return route.fallback();
  });
  await page.goto(`${USER_ORIGIN}/games/likes`);
  await expect(page.locator('.likes-arena')).toBeVisible();
}

for (const mobile of [false, true]) {
  test(`partial resistance stays readable ${mobile ? 'on a reduced-motion phone' : 'on desktop'}`, async ({
    page,
  }) => {
    const errors = collectConsoleViolations(page);
    await page.setViewportSize({ width: mobile ? 390 : 1440, height: 1000 });
    await page.emulateMedia({
      reducedMotion: mobile ? 'reduce' : 'no-preference',
      colorScheme: mobile ? 'dark' : 'light',
    });
    const steps = wire.partial.summary.timeline,
      at = steps.findIndex((s) => s.stage === 'score');
    const offset = Math.ceil(steps.slice(0, at).reduce((n, s) => n + s.duration_ms, 0) / 1000);
    await setup(page, 'partial', offset);
    await page.clock.install();
    await page.clock.pauseAt(new Date(Date.now() + 1000));
    await expect(page.locator('.likes-application-result').first()).toContainText(
      'Applied 1 / Resisted 1',
    );
    await expect(page.locator('.likes-application-result').last()).toContainText(
      'Applied 0 / Resisted 1',
    );
    await expect(page.locator('.likes-character-passive')).toHaveCount(2);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
      true,
    );
    await page.locator('.likes-application-result').first().scrollIntoViewIfNeeded();
    await page.screenshot({
      path: `../tmp/passive-partial-${mobile ? 'mobile' : 'desktop'}.png`,
      fullPage: true,
    });
    await page.clock.resume();
    await page.getByRole('button', { name: 'Round log', exact: true }).click();
    await page.locator('.duel-round > summary').click();
    const attempts = page
      .locator('.likes-event')
      .filter({ has: page.locator('summary', { hasText: 'Debuff hit and resistance' }) });
    await expect(attempts).toHaveCount(3);
    await attempts.first().locator('summary').click();
    await expect(attempts.first()).toContainText('Candidate count');
    await expect(attempts.first()).toContainText('125');
    errors.assertNone();
  });
}

test('long Flash chain reveals each authoritative cast pair once', async ({ page }) => {
  const errors = collectConsoleViolations(page);
  await page.setViewportSize({ width: 390, height: 1000 });
  await setup(page, 'chain');
  await page.clock.install();
  await page.clock.pauseAt(new Date(Date.now() + 1000));
  const steps = wire.chain.summary.timeline;
  let elapsed = 0,
    seen = 1000;
  for (const step of steps) {
    const pair = wire.chain.summary.events.filter(
      (e) => step.event_ids.includes(e.id) && e.kind === 'cast' && e.data.derived,
    );
    if (pair.length) {
      await page.clock.runFor(Math.max(0, elapsed + 500 - seen));
      seen = elapsed + 500;
      await expect(
        page.locator('.likes-cast-facts strong').filter({ hasText: 'Follow-up' }),
      ).toHaveCount(2);
      await expect(page.locator('.likes-character-passive').first()).toContainText(
        'World knowledge',
      );
    }
    elapsed += step.duration_ms;
  }
  await page.screenshot({ path: '../tmp/passive-flash-mobile.png', fullPage: true });
  errors.assertNone();
});

test('saved legacy catalog displays its original passives and round facts', async ({ page }) => {
  const errors = collectConsoleViolations(page);
  await setup(page, 'legacy');
  await expect(page.locator('.likes-character-passive')).toHaveCount(0);
  await page.getByRole('button', { name: 'Round log', exact: true }).click();
  await page.locator('.duel-round > summary').click();
  await expect(
    page.locator('.likes-event').filter({ hasText: 'Debuff hit and resistance' }),
  ).toHaveCount(0);
  await expect(page.locator('.likes-round-log')).toContainText('Successful cast');
  errors.assertNone();
});
