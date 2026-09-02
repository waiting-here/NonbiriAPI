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
import type { GamesConfig } from '../../src/admin/features/operations/economy';

type BrowserContext = Parameters<typeof installURLPersistenceObserver>[0];
type Page = Parameters<typeof collectConsoleViolations>[0];
type RouteHandler = NonNullable<Parameters<Page['route']>[1]>;
type Route = Parameters<RouteHandler>[0];

const EPHEMERAL_MARKER = 'admin-games-ephemeral-marker';

const INITIAL_CONFIG: GamesConfig = {
  revision: '7',
  master_enabled: true,
  fishing: {
    enabled: true,
    bait_prices: { worm: '2.5', lure: '5', premium: '7.5' },
    rtp_percent: { standard: 90, premium: 88 },
    treasure_multipliers: { bottle: 2, clover: 3, shell: 5 },
  },
  linklink: {
    enabled: true,
    specs: {
      '6x8': { enabled: true, price: '1' },
      '8x8': { enabled: true, price: '2' },
      '10x10': { enabled: false, price: '3.125' },
    },
  },
  rps: {
    enabled: true,
    modes: {
      quick: {
        enabled: true,
        base: '1',
        pumps_bp: { platform: 100, welfare: 200, thursday: 300 },
        queue_seconds: 60,
        gesture_seconds: 10,
        dealer_seconds: 10,
        follower_seconds: 10,
        queue_capacity: 1_024,
      },
      standard: {
        enabled: true,
        base: '2',
        pumps_bp: { platform: 200, welfare: 300, thursday: 400 },
        queue_seconds: 90,
        gesture_seconds: 15,
        dealer_seconds: 12,
        follower_seconds: 12,
        queue_capacity: 2_048,
      },
      deathmatch: {
        enabled: false,
        base: '3',
        pumps_bp: { platform: 300, welfare: 400, thursday: 500 },
        queue_seconds: 120,
        gesture_seconds: 20,
        dealer_seconds: 15,
        follower_seconds: 15,
        queue_capacity: 4_096,
      },
    },
  },
};

async function fulfillJSON(route: Route, value: unknown) {
  await route.fulfill({
    status: 200,
    headers: { 'cache-control': 'no-store', 'content-type': 'application/json' },
    body: JSON.stringify(value),
  });
}

type MutableRPSMode = Omit<GamesConfig['rps']['modes']['quick'], 'queue_capacity'>;
type GamesPatch = {
  expected_revision: string;
  master_enabled: boolean;
  fishing: GamesConfig['fishing'];
  linklink: GamesConfig['linklink'];
  rps: {
    enabled: boolean;
    modes: Record<'quick' | 'standard' | 'deathmatch', MutableRPSMode>;
  };
};

function applyPatch(config: GamesConfig, rawPatch: Record<string, unknown>): GamesConfig {
  const patch = rawPatch as GamesPatch;
  return {
    revision: String(BigInt(config.revision) + 1n),
    master_enabled: patch.master_enabled,
    fishing: structuredClone(patch.fishing),
    linklink: structuredClone(patch.linklink),
    rps: {
      enabled: patch.rps.enabled,
      modes: {
        quick: {
          ...structuredClone(patch.rps.modes.quick),
          queue_capacity: config.rps.modes.quick.queue_capacity,
        },
        standard: {
          ...structuredClone(patch.rps.modes.standard),
          queue_capacity: config.rps.modes.standard.queue_capacity,
        },
        deathmatch: {
          ...structuredClone(patch.rps.modes.deathmatch),
          queue_capacity: config.rps.modes.deathmatch.queue_capacity,
        },
      },
    },
  };
}

async function prepare(
  context: BrowserContext,
  page: Page,
  config: { current: GamesConfig; patches: Record<string, unknown>[] },
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
  await mockJson(page, {
    origin: ADMIN_ORIGIN,
    method: 'GET',
    path: '/admin/api/games/active-counts',
    body: { games: [], queues: [] },
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

  const worm = page.getByLabel('Worm bait price (credits)');
  await worm.focus();
  await page.keyboard.press('ControlOrMeta+A');
  await page.keyboard.type('3');
  const save = page.getByRole('button', { name: 'Save game configuration' });
  await save.focus();
  await page.keyboard.press('Enter');
  await expect.poll(() => config.patches.length).toBe(1);
  expect(config.patches[0]).toEqual({
    expected_revision: '7',
    master_enabled: false,
    fishing: {
      ...INITIAL_CONFIG.fishing,
      bait_prices: { ...INITIAL_CONFIG.fishing.bait_prices, worm: '3' },
    },
    linklink: INITIAL_CONFIG.linklink,
    rps: {
      enabled: INITIAL_CONFIG.rps.enabled,
      modes: {
        quick: {
          enabled: true,
          base: '1',
          pumps_bp: { platform: 100, welfare: 200, thursday: 300 },
          queue_seconds: 60,
          gesture_seconds: 10,
          dealer_seconds: 10,
          follower_seconds: 10,
        },
        standard: {
          enabled: true,
          base: '2',
          pumps_bp: { platform: 200, welfare: 300, thursday: 400 },
          queue_seconds: 90,
          gesture_seconds: 15,
          dealer_seconds: 12,
          follower_seconds: 12,
        },
        deathmatch: {
          enabled: false,
          base: '3',
          pumps_bp: { platform: 300, welfare: 400, thursday: 500 },
          queue_seconds: 120,
          gesture_seconds: 20,
          dealer_seconds: 15,
          follower_seconds: 15,
        },
      },
    },
  });
  expect(JSON.stringify(config.patches[0])).not.toContain('queue_capacity');
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
  await expect(page.getByLabel('Worm bait price (credits)')).toBeVisible();
  expect(await page.evaluate(() => matchMedia('(prefers-reduced-motion: reduce)').matches)).toBe(
    true,
  );
  await assertNoSensitiveBrowserPersistence(page, [EPHEMERAL_MARKER]);
  consoleGuard.assertNone();
});
