import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { expect, test } from './test';
import { collectConsoleViolations, mockPublicConfig, mockRoleSession } from './support';
import { USER_ORIGIN } from './ports';
import { gamesSnapshotWire } from '../../src/user/games/common/testFixtures';
import wire from '../../src/user/games/likes/testdata/authority.json' with { type: 'json' };
import timings from '../../src/user/games/likes/testdata/timelines.json' with { type: 'json' };

function catalogFixture() {
  const modes = Object.fromEntries(
    ['quick', 'standard'].map((mode) => {
      const source = readFileSync(
        resolve('../internal/game/likes/catalog', `${mode}.json`),
        'utf8',
      );
      const config = JSON.parse(source);
      delete config.parameters.POINT_TICKET;
      delete config.parameters.FOLLOWUP_CAP;
      config.paramMeta = config.paramMeta.filter(
        (item: { id: string }) => !['POINT_TICKET', 'FOLLOWUP_CAP'].includes(item.id),
      );
      for (const buff of config.buffs) {
        if (buff.kind === 'OVERLOAD')
          buff.target = '自身；共享电能不足时仅本轮报价大于零的席位过载';
      }
      return [
        mode,
        {
          rules_version: 1,
          design_version: '0.17.0',
          schema_version: 15,
          content_hash: createHash('sha256')
            .update('likes@1;positive-energy-overload;separate-round-start\n' + source)
            .digest('hex'),
          config,
        },
      ];
    }),
  );
  return {
    rules_version: 1,
    design_version: '0.17.0',
    schema_version: 15,
    content_hash: 'a'.repeat(64),
    modes,
  };
}

test('desktop battle uses both screen halves while narrow screens retain compact art', async ({
  page,
}) => {
  const errors = collectConsoleViolations(page);
  await mockRoleSession(page, 'user', 'user');
  await mockPublicConfig(page, 'user');
  const catalog = catalogFixture();
  const start = 1_800_000_000;
  const summary = {
    ...wire.rounds[0].summary,
    timeline: timings.rounds[0].timeline.map((step) => ({
      ...step,
      duration_ms: step.duration_ms * 4,
    })),
  };
  const state = {
    server_now: start + 15,
    queue: null,
    latest_result: null,
    current: {
      id: 'lik_AAAAAAAAAAAAAAAAAAAAAA',
      game: 'likes',
      mode: 'quick',
      rules_version: 1,
      content_hash: catalog.modes.quick.content_hash,
      revision: '2',
      phase_seq: '2',
      phase: 'settlement',
      round: 1,
      deadline: start + 36,
      server_now: start + 15,
      you: 0,
      locked: [true, true],
      ticket: '1',
      rake_bp: { platform: 0, welfare: 0, thursday: 0 },
      own_payment: { general: '1', game: '0' },
      profiles: [{ kind: 'anonymous' }, { kind: 'anonymous' }],
      view: wire.rounds[0].after,
      resolution: { round: 1, started_at: start, ends_at: start + 36, summary },
      round_start: null,
    },
  };
  await page.route('**/api/games**', async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path === '/api/games') return route.fulfill({ json: gamesSnapshotWire() });
    if (path === '/api/games/likes/catalog') return route.fulfill({ json: catalog });
    if (path === '/api/games/likes/state') return route.fulfill({ json: state });
    return route.fallback();
  });
  await page.setViewportSize({ width: 2560, height: 1440 });
  await page.goto(`${USER_ORIGIN}/games/likes`);
  await expect(page.locator('.likes-cast')).toHaveCount(2);
  await expect(page.locator('.likes-cast img').first()).toBeVisible();
  for (const width of [2560, 1440, 1024]) {
    await page.setViewportSize({ width, height: 1000 });
    const game = await page.locator('.likes-game').boundingBox();
    const cast = await page.locator('.likes-cast').first().boundingBox();
    expect(game!.width).toBeGreaterThan(width - 100);
    expect(cast!.width).toBeGreaterThan(width * 0.35);
    expect(cast!.height).toBeGreaterThan(350);
    await expect(page.locator('.likes-compact-scores')).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
      true,
    );
  }
  await page.setViewportSize({ width: 1920, height: 1080 });
  await page.locator('.likes-arena').scrollIntoViewIfNeeded();
  await page.screenshot({ path: '../tmp/battle-desktop.png' });
  for (const width of [390, 320]) {
    await page.setViewportSize({ width, height: 844 });
    expect((await page.locator('.likes-cast').first().boundingBox())!.height).toBeLessThan(150);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
      true,
    );
  }
  await page.emulateMedia({ reducedMotion: 'reduce' });
  expect(
    await page
      .locator('.likes-cast')
      .first()
      .evaluate((node) => getComputedStyle(node).animationName),
  ).toBe('none');
  errors.assertNone();
});
