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
  blackjack: {
    enabled: false,
    min_stake: '1000',
    max_stake: '50000',
    stake_step: '1000',
    default_stake: '5000',
    rake_bp: { platform: 100, welfare: 100, thursday: 100 },
    quick_stakes: ['1000', '5000', '10000', '50000'],
  },
  bidding: duelConfigFixture('bidding'),
  likes: duelConfigFixture('likes'),
  revision: '7',
  master_enabled: true,
  fishing: {
    blue_fish_chance_bps: 1000,
    enabled: true,
    bait_prices: { worm: '2.5', lure: '5', premium: '7.5' },
    rtp_percent: { standard: 90, premium: 88 },
    rake_bp: { platform: 100, welfare: 100, thursday: 100 },
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
  blackjack: GamesConfig['blackjack'];
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
    blackjack: structuredClone(patch.blackjack),
    bidding: structuredClone(config.bidding),
    likes: structuredClone(config.likes),
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
  preferences = { locale: 'en', theme: 'dark' },
) {
  const consoleGuard = collectConsoleViolations(page);
  await installURLPersistenceObserver(context, [EPHEMERAL_MARKER]);
  await configureNarrowReducedMotion(page);
  await page.addInitScript(({ locale, theme }) => {
    localStorage.setItem('nb.lang', locale);
    localStorage.setItem('nb.theme', theme);
  }, preferences);
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
      url.origin === ADMIN_ORIGIN &&
      request.method() === 'GET' &&
      url.pathname === '/admin/api/games/config'
    ) {
      await fulfillJSON(route, config.current);
      return;
    }
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
  await expect(page.getByRole('switch', { name: 'Enable Fishing', exact: true })).toBeChecked();

  const master = page.getByLabel('Games master switch');
  await master.focus();
  await page.keyboard.press('Space');
  await expect(master).not.toBeChecked();
  await expect(page.getByRole('button', { name: 'Save game configuration' })).toBeDisabled();
  await master.press('Space');
  await expect(master).toBeChecked();

  await page.getByRole('button', { name: 'Fishing Game settings', exact: true }).click();
  const worm = page.getByLabel('Worm bait', { exact: true });
  await worm.focus();
  await page.keyboard.press('ControlOrMeta+A');
  await page.keyboard.type('3');
  const chance = page.getByLabel('Blue-fish probability', { exact: true });
  await expect(chance).toHaveValue('10');
  await chance.fill('37.5');
  const save = page.getByRole('button', { name: 'Save Fishing settings', exact: true });
  await save.focus();
  await page.keyboard.press('Enter');
  await expect.poll(() => config.patches.length).toBe(1);
  expect(config.patches[0]).toEqual({
    expected_revision: '7',
    master_enabled: true,
    blackjack: INITIAL_CONFIG.blackjack,
    bidding: INITIAL_CONFIG.bidding,
    likes: INITIAL_CONFIG.likes,
    fishing: {
      ...INITIAL_CONFIG.fishing,
      blue_fish_chance_bps: 3750,
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
  await expect(page.getByRole('status').filter({ hasText: 'Game settings saved' })).toBeVisible();

  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true,
  );
  await page.getByRole('button', { name: 'Fishing Game settings', exact: true }).click();
  await expect(save).toBeVisible();
  await page.setViewportSize({ width: 780, height: 844 });
  await page.evaluate(() => {
    document.documentElement.style.zoom = '200%';
  });
  await expect(save).toBeVisible();
  await expect(page.getByLabel('Worm bait', { exact: true })).toBeVisible();
  expect(await page.evaluate(() => matchMedia('(prefers-reduced-motion: reduce)').matches)).toBe(
    true,
  );
  await assertNoSensitiveBrowserPersistence(page, [EPHEMERAL_MARKER]);
  consoleGuard.assertNone();
});
import { duelConfigFixture } from '../duelConfigFixture';

test('admin validates quick amount count, duplicates and limits before saving the complete list', async ({
  context,
  page,
}) => {
  const config = {
    current: structuredClone(INITIAL_CONFIG),
    patches: [] as Record<string, unknown>[],
  };
  const errors = await prepare(context, page, config);
  await page.goto(`${ADMIN_ORIGIN}/games`);
  await page.getByRole('button', { name: 'Blackjack Game settings', exact: true }).click();
  const group = page.getByRole('group', { name: 'Quick stake amounts (0–8)' });
  const save = page.getByRole('button', { name: 'Save Blackjack settings', exact: true });
  await expect(group.getByRole('textbox')).toHaveCount(4);
  for (let i = 0; i < 4; i++)
    await group
      .getByRole('button', { name: /^Remove quick amount/ })
      .first()
      .click();
  for (let i = 0; i < 8; i++) {
    await group.getByRole('button', { name: 'Add quick amount' }).click();
    await group
      .getByRole('textbox')
      .nth(i)
      .fill(String((i + 1) * 1000));
  }
  await expect(group.getByRole('button', { name: 'Add quick amount' })).toBeDisabled();
  await group.getByRole('textbox').nth(1).fill('1000');
  await expect(save).toBeDisabled();
  expect(config.patches).toHaveLength(0);
  await group.getByRole('textbox').nth(1).fill('2000');
  await page.getByLabel('Minimum base stake', { exact: true }).fill('2000');
  await expect(save).toBeDisabled();
  await group.getByRole('button', { name: 'Remove quick amount 1', exact: true }).click();
  await save.click();
  await expect.poll(() => config.patches.length).toBe(1);
  expect(config.current.blackjack.min_stake).toBe('2000');
  expect(config.current.blackjack.quick_stakes).toEqual([
    '2000',
    '3000',
    '4000',
    '5000',
    '6000',
    '7000',
    '8000',
  ]);
  await page.getByRole('button', { name: 'Blackjack Game settings', exact: true }).click();
  for (let i = 0; i < 7; i++)
    await group
      .getByRole('button', { name: /^Remove quick amount/ })
      .first()
      .click();
  await save.click();
  await expect.poll(() => config.patches.length).toBe(2);
  expect(config.current.blackjack.quick_stakes).toEqual([]);
  errors.assertNone();
});

for (const scenario of [
  { width: 1440, height: 900, locale: 'en', theme: 'light' },
  { width: 768, height: 1024, locale: 'en', theme: 'dark' },
  { width: 390, height: 844, locale: 'zh', theme: 'dark' },
]) {
  test(`game drawers retain labels, keyboard focus and cancelled drafts at ${scenario.width}`, async ({
    context,
    page,
  }) => {
    const config = {
      current: structuredClone(INITIAL_CONFIG),
      patches: [] as Record<string, unknown>[],
    };
    const guard = await prepare(context, page, config, scenario);
    await page.setViewportSize(scenario);
    await page.goto(`${ADMIN_ORIGIN}/games`);
    const rows = page.locator('.admin-game-row');
    await expect(rows).toHaveCount(6);
    const capture = async (name: string) => {
      if (process.env.NONBIRI_VISUAL_DIR)
        await page.screenshot({
          path: `${process.env.NONBIRI_VISUAL_DIR}/games-${scenario.locale}-${scenario.width}-${name}.png`,
          fullPage: true,
        });
    };
    await capture('overview');
    for (let index = 0; index < 6; index++) {
      const trigger = rows.nth(index).getByRole('button');
      await trigger.click();
      const dialog = page.getByRole('dialog');
      await expect(dialog).toBeVisible();
      for (const disclosure of await dialog.locator('details:not([open]) > summary').all())
        await disclosure.click();
      const unnamed = await dialog
        .locator('input:not([type="checkbox"])')
        .evaluateAll(
          (inputs) =>
            inputs.filter(
              (node) =>
                !(node as HTMLInputElement).labels?.length ||
                !Array.from((node as HTMLInputElement).labels!).some(
                  (label) => label.textContent?.trim() && label.getBoundingClientRect().width > 0,
                ),
            ).length,
        );
      expect(unnamed).toBe(0);
      expect(await dialog.evaluate((node) => node.scrollWidth <= node.clientWidth + 1)).toBe(true);
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
        true,
      );
      await capture(`drawer-${index}`);
      if (index === 0) {
        const amount = dialog.locator('input[name="fishing.bait_prices.worm"]');
        await amount.fill('3.25');
        await page.keyboard.press('Escape');
        await expect(trigger).toBeFocused();
        await trigger.click();
        await expect(amount).toHaveValue('2.5');
      }
      await page.keyboard.press('Escape');
      await expect(page.getByRole('dialog')).toHaveCount(0);
      await expect(trigger).toBeFocused();
    }
    expect(config.patches).toEqual([]);
    guard.assertNone();
  });
}
