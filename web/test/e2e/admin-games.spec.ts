import { expect, test } from './test';
import {
  assertNoSensitiveBrowserPersistence,
  collectConsoleViolations,
  installURLPersistenceObserver,
  mockJson,
  mockPublicConfig,
  mockRoleSession,
  useNarrowReducedMotion as configureNarrowReducedMotion,
} from './support';
import { ADMIN_ORIGIN } from './ports';

type BrowserContext = Parameters<typeof installURLPersistenceObserver>[0];
type Page = Parameters<typeof collectConsoleViolations>[0];
type RouteHandler = NonNullable<Parameters<Page['route']>[1]>;
type Route = Parameters<RouteHandler>[0];

const EPHEMERAL_MARKER = 'admin-games-ephemeral-marker';

interface GameConfig {
  master_enabled: boolean;
  fishing: {
    enabled: boolean;
    bait_prices: { worm: string; lure: string; premium: string };
    rtp_percent: { standard: number; premium: number };
    treasure_multipliers: { bottle: number; clover: number; shell: number };
  };
}

const INITIAL_CONFIG: GameConfig = {
  master_enabled: true,
  fishing: {
    enabled: true,
    bait_prices: { worm: '2500000', lure: '5000000', premium: '7500000' },
    rtp_percent: { standard: 90, premium: 88 },
    treasure_multipliers: { bottle: 2, clover: 3, shell: 5 },
  },
};

async function fulfillJSON(route: Route, value: unknown) {
  await route.fulfill({
    status: 200,
    headers: { 'cache-control': 'no-store', 'content-type': 'application/json' },
    body: JSON.stringify(value),
  });
}

function applyPatch(config: GameConfig, patch: Record<string, unknown>): GameConfig {
  const fishingPatch = (patch.fishing ?? {}) as Record<string, unknown>;
  const prices = (fishingPatch.bait_prices ?? {}) as Record<string, unknown>;
  const rtp = (fishingPatch.rtp_percent ?? {}) as Record<string, unknown>;
  const multipliers = (fishingPatch.treasure_multipliers ?? {}) as Record<string, unknown>;
  return {
    master_enabled:
      typeof patch.master_enabled === 'boolean' ? patch.master_enabled : config.master_enabled,
    fishing: {
      enabled:
        typeof fishingPatch.enabled === 'boolean' ? fishingPatch.enabled : config.fishing.enabled,
      bait_prices: {
        worm: typeof prices.worm === 'string' ? prices.worm : config.fishing.bait_prices.worm,
        lure: typeof prices.lure === 'string' ? prices.lure : config.fishing.bait_prices.lure,
        premium:
          typeof prices.premium === 'string' ? prices.premium : config.fishing.bait_prices.premium,
      },
      rtp_percent: {
        standard:
          typeof rtp.standard === 'number' ? rtp.standard : config.fishing.rtp_percent.standard,
        premium: typeof rtp.premium === 'number' ? rtp.premium : config.fishing.rtp_percent.premium,
      },
      treasure_multipliers: {
        bottle:
          typeof multipliers.bottle === 'number'
            ? multipliers.bottle
            : config.fishing.treasure_multipliers.bottle,
        clover:
          typeof multipliers.clover === 'number'
            ? multipliers.clover
            : config.fishing.treasure_multipliers.clover,
        shell:
          typeof multipliers.shell === 'number'
            ? multipliers.shell
            : config.fishing.treasure_multipliers.shell,
      },
    },
  };
}

async function prepare(
  context: BrowserContext,
  page: Page,
  config: { current: GameConfig; patches: Record<string, unknown>[] },
) {
  const consoleGuard = collectConsoleViolations(page);
  await installURLPersistenceObserver(context, [EPHEMERAL_MARKER]);
  await configureNarrowReducedMotion(page);
  await page.addInitScript(() => {
    localStorage.setItem('nb.lang', 'en');
    localStorage.setItem('nb.theme', 'dark');
  });
  await mockPublicConfig(page, 'admin');
  await mockRoleSession(page, 'admin', 'admin');
  await mockJson(page, {
    origin: ADMIN_ORIGIN,
    method: 'GET',
    path: '/admin/api/games/config',
    body: INITIAL_CONFIG,
  });
  await page.route('**/*', async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    if (
      url.origin !== ADMIN_ORIGIN ||
      request.method() !== 'PATCH' ||
      url.pathname !== '/admin/api/games/config'
    ) {
      await route.fallback();
      return;
    }
    const patch = request.postDataJSON() as Record<string, unknown>;
    config.patches.push(patch);
    config.current = applyPatch(config.current, patch);
    await fulfillJSON(route, config.current);
  });
  return consoleGuard;
}

test('admin games route performs authoritative PATCH with keyboard input at 390px and 200% zoom', async ({
  context,
  page,
}) => {
  const config = {
    current: structuredClone(INITIAL_CONFIG),
    patches: [] as Record<string, unknown>[],
  };
  const consoleGuard = await prepare(context, page, config);

  await page.goto(`${ADMIN_ORIGIN}/games`);
  await expect(page.getByRole('heading', { name: 'Game configuration' })).toBeVisible();
  expect(await page.locator('html').getAttribute('data-theme')).toBe('dark');
  await expect(page.getByLabel('Games master switch')).toBeChecked();
  await expect(page.getByLabel('Fishing enabled')).toBeChecked();

  const master = page.getByLabel('Games master switch');
  await master.focus();
  await page.keyboard.press('Space');
  await expect(master).not.toBeChecked();

  const worm = page.getByLabel('Worm bait');
  await worm.focus();
  await page.keyboard.press('ControlOrMeta+A');
  await page.keyboard.type('3000000');
  const save = page.getByRole('button', { name: 'Save game configuration' });
  await save.focus();
  await page.keyboard.press('Enter');
  await expect.poll(() => config.patches.length).toBe(1);
  expect(config.patches[0]).toEqual({
    master_enabled: false,
    fishing: { bait_prices: { worm: '3000000' } },
  });
  await expect(page.getByRole('status')).toContainText('Game configuration updated');

  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true,
  );
  await expect(save).toBeVisible();
  await page.setViewportSize({ width: 780, height: 844 });
  await page.evaluate(() => {
    document.documentElement.style.zoom = '200%';
  });
  await expect(save).toBeVisible();
  await expect(page.getByLabel('Worm bait')).toBeVisible();
  expect(await page.evaluate(() => matchMedia('(prefers-reduced-motion: reduce)').matches)).toBe(
    true,
  );
  await assertNoSensitiveBrowserPersistence(page, [EPHEMERAL_MARKER]);
  consoleGuard.assertNone();
});
