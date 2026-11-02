import { mkdirSync } from 'node:fs';
import { resolve } from 'node:path';
import { expect, test } from './test';
import {
  assertNoSensitiveBrowserPersistence,
  collectConsoleViolations,
  installLoopbackNetworkBoundary,
  installURLPersistenceObserver,
  mockPublicConfig,
  mockRoleSession,
  tabTo,
  useNarrowReducedMotion,
  userSession,
} from './support';
import { USER_ORIGIN } from './ports';
import { gamesSnapshotWire } from '../../src/user/games/common/testFixtures';
import {
  fishArtwork,
  junkArtwork,
  treasureArtwork,
} from '../../src/user/games/fishing/artRegistry';

const BATCH_ID = 'fb_AAAAAAAAAAAAAAAAAAAAAA';
const RESULT = {
  batch_id: BATCH_ID,
  bait: 'worm',
  count: 1,
  unit_price: '2.5',
  entry_total: '2.5',
  rules_version: 1,
  payment: { general: '2.5', game: '0' },
  outcomes: [
    {
      ordinal: 0,
      species_key: 'koi',
      tier: 'legend',
      size_cm: 180,
      reward: '12',
      net_reward: '12',
      rake: { platform: '0', welfare: '0', thursday: '0' },
    },
  ],
  payout_total: '12',
  net_payout_total: '12',
  rake: { platform: '0', welfare: '0', thursday: '0' },
  balance: '14.5',
  game_balance: '0',
  settled_at: 1_787_450_010,
  idempotent_replay: false,
};
const FISHING_PERSISTENCE_MARKERS = [BATCH_ID, String(RESULT.settled_at)] as const;
const AVATAR_URL = 'https://cdn.discordapp.com/avatars/a/b.png?size=64';

type FishingFixtureState = {
  settlement_pending: Record<string, unknown> | null;
  unrevealed: Record<string, unknown> | null;
  has_more_unrevealed: boolean;
};

interface FishingFixture {
  state: FishingFixtureState;
  balance: string;
  starts: number;
  settlements: number;
  acks: number;
  settlePending: boolean;
  failACK: boolean;
  preserveAfterACK: boolean;
  ackDelayMS: number;
}

function emptyState(): FishingFixtureState {
  return { settlement_pending: null, unrevealed: null, has_more_unrevealed: false };
}

function fixtureWith(state: FishingFixtureState = emptyState()): FishingFixture {
  return {
    state,
    balance: '5000000',
    starts: 0,
    settlements: 0,
    acks: 0,
    settlePending: false,
    failACK: false,
    preserveAfterACK: false,
    ackDelayMS: 0,
  };
}

function jsonResponse(body: unknown, status = 200) {
  return {
    status,
    headers: { 'content-type': 'application/json', 'cache-control': 'no-store' },
    body: JSON.stringify(body),
  };
}

async function installFishingRoutes(
  page: import('@playwright/test').Page,
  fixture: FishingFixture,
) {
  // Keep the public avatar path exercised while satisfying the suite's
  // no-external-network boundary with one exact, synthetic image response.
  await page.route(AVATAR_URL, (route) =>
    route.fulfill({
      contentType: 'image/svg+xml',
      body: '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1 1"><rect width="1" height="1" fill="#789"/></svg>',
    }),
  );
  await page.route('**/api/games**', async (route) => {
    const request = route.request();
    const requestURL = new URL(request.url());
    if (requestURL.origin !== USER_ORIGIN) {
      await route.fallback();
      return;
    }
    if (requestURL.pathname === '/api/games' && request.method() === 'GET') {
      const snapshot = gamesSnapshotWire();
      snapshot.balance = fixture.balance;
      snapshot.fishing.bait_prices = { worm: '2.5', lure: '5', premium: '7.5' };
      await route.fulfill(jsonResponse(snapshot));
      return;
    }
    if (requestURL.pathname === '/api/games/fishing/state' && request.method() === 'GET') {
      if (fixture.settlePending && fixture.state.settlement_pending) {
        fixture.settlePending = false;
        fixture.settlements += 1;
        fixture.balance = RESULT.balance;
        fixture.state = {
          settlement_pending: null,
          unrevealed: RESULT,
          has_more_unrevealed: false,
        };
      }
      await route.fulfill(jsonResponse(fixture.state));
      return;
    }
    if (requestURL.pathname === '/api/games/fishing/leaderboard' && request.method() === 'GET') {
      const board = requestURL.searchParams.get('board');
      if (board !== 'single' && board !== 'recent_single' && board !== 'total') {
        await route.fallback();
        return;
      }
      if (board === 'total') {
        await route.fulfill(
          jsonResponse({
            board: 'total',
            window_start: 1_787_000_000,
            entries: [],
            me: null,
          }),
        );
      } else {
        await route.fulfill(
          jsonResponse({
            board,
            window_start: board === 'single' ? null : 1_787_000_000,
            entries: [
              {
                rank: '1',
                species_key: 'taimen',
                size_cm: 190,
                identity: { kind: 'anonymous' },
                is_me: false,
              },
              {
                rank: '2',
                species_key: 'koi',
                size_cm: 180,
                identity: {
                  kind: 'public',
                  display_name: 'Public angler',
                  avatar_url: AVATAR_URL,
                },
                is_me: false,
              },
              {
                rank: '3',
                species_key: 'koi',
                size_cm: 170,
                identity: {
                  kind: 'public',
                  display_name: 'No avatar angler',
                  avatar_url: null,
                },
                is_me: false,
              },
            ],
            me: null,
          }),
        );
      }
      return;
    }
    if (requestURL.pathname === '/api/games/fishing/batches' && request.method() === 'POST') {
      const body = request.postDataJSON() as { bait?: unknown; count?: unknown };
      if (body.bait !== 'worm' || body.count !== 1) {
        await route.fulfill(
          jsonResponse(
            { error: { code: 'invalid_request', message: 'Unexpected test intent.' } },
            400,
          ),
        );
        return;
      }
      fixture.starts += 1;
      fixture.balance = '4999997.5';
      const pending = {
        batch_id: BATCH_ID,
        bait: 'worm',
        count: 1,
        entry_total: '2.5',
        rules_version: 1,
        payment: { general: '2.5', game: '0' },
        state: 'settlement_pending',
        next_attempt_at: 1_800_000_010,
        retry_exhausted: false,
      };
      fixture.state = {
        settlement_pending: pending,
        unrevealed: null,
        has_more_unrevealed: false,
      };
      await route.fulfill(jsonResponse(pending, 202));
      return;
    }
    if (
      requestURL.pathname === `/api/games/fishing/batches/${BATCH_ID}/ack` &&
      request.method() === 'POST'
    ) {
      fixture.acks += 1;
      if (fixture.ackDelayMS > 0) {
        await new Promise((resolve) => setTimeout(resolve, fixture.ackDelayMS));
      }
      if (fixture.failACK) {
        await route.fulfill(
          jsonResponse(
            { error: { code: 'temporarily_unavailable', message: 'Synthetic ACK failure.' } },
            503,
          ),
        );
        return;
      }
      if (!fixture.preserveAfterACK) fixture.state = emptyState();
      await route.fulfill({ status: 204, headers: { 'cache-control': 'no-store' } });
      return;
    }
    await route.fallback();
  });
}

async function installUserShell(
  page: import('@playwright/test').Page,
  language: 'en' | 'zh' = 'en',
) {
  await page.addInitScript((selectedLanguage) => {
    localStorage.setItem('nb.lang', selectedLanguage);
    localStorage.setItem('nb.theme', 'dark');
  }, language);
  await mockPublicConfig(page, 'user');
  await mockRoleSession(page, 'user', 'level4');
}

for (const appearance of ['default', 'celestial'])
  for (const theme of ['light', 'dark'])
    for (const width of [390, 1440])
      test(`Fishing balance remains reachable ${appearance} ${theme} ${width}`, async ({
        page,
      }) => {
        const guard = collectConsoleViolations(page);
        const height = width === 390 ? 844 : 900;
        await page.setViewportSize({ width, height });
        await page.emulateMedia({ reducedMotion: 'reduce' });
        await installUserShell(page, 'zh');
        await page.addInitScript((theme) => localStorage.setItem('nb.theme', theme), theme);
        await page.route('**/api/appearance', (route) =>
          route.fulfill({
            json: { selected: appearance, unlocked: ['default', 'celestial'], updated_at: null },
          }),
        );
        await installFishingRoutes(page, fixtureWith());
        await page.goto(`${USER_ORIGIN}/games/fishing`);
        await expect(page.locator('html')).toHaveAttribute('data-appearance', appearance);
        const bar = page.locator('.game-actionbar');
        const start = bar.getByRole('button', { name: '开始垂钓', exact: true });
        const balance = page.locator('.fishing-controls .fishing-totals > div').last();
        await expect(balance).toContainText('5,000,000');
        const evidence = process.env.NONBIRI_FISHING_LAYOUT_EVIDENCE;
        const capture = async (state: string) => {
          if (!evidence) return;
          mkdirSync(evidence, { recursive: true });
          await page.screenshot({
            path: resolve(evidence, `${appearance}-${theme}-${width}-${state}.png`),
          });
        };
        const initialStart = (await start.boundingBox())!;
        await capture('top');
        await balance.evaluate((element) => element.scrollIntoView({ block: 'end' }));
        await expect(balance).toBeInViewport();
        const balanceBox = (await balance.boundingBox())!;
        const barBox = (await bar.boundingBox())!;
        await capture('balance');
        expect(balanceBox.y + balanceBox.height).toBeLessThanOrEqual(barBox.y);
        if (width === 390) {
          expect(barBox.x).toBe(0);
          expect(barBox.width).toBe(width);
          expect(barBox.y + barBox.height).toBeCloseTo(height, 0);
          expect(initialStart.y).toBeGreaterThanOrEqual(0);
          expect(initialStart.y + initialStart.height).toBeLessThanOrEqual(height);
        }
        const count = page.locator('.fishing-count button').last();
        await count.focus();
        await expect(count).toBeFocused();
        const countBox = (await count.boundingBox())!;
        expect(countBox.y + countBox.height).toBeLessThanOrEqual((await bar.boundingBox())!.y);
        await capture('focus');
        await page.evaluate(() => scrollTo(0, document.documentElement.scrollHeight));
        await expect(page.locator('#game-rankings')).toBeInViewport();
        if (width === 390) {
          const scrolledBar = (await bar.boundingBox())!;
          expect(scrolledBar.y + scrolledBar.height).toBeCloseTo(height, 0);
        }
        await capture('bottom');
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
          true,
        );
        if (appearance !== 'default') {
          await page.getByRole('button', { name: '欣赏背景', exact: true }).click();
          const view = page.getByRole('dialog', { name: '欣赏背景', exact: true });
          await expect(view).toBeVisible();
          await expect(bar).not.toBeVisible();
          await view.getByRole('button', { name: '返回页面', exact: true }).click();
          await expect(bar).toBeVisible();
        }
        guard.assertNone();
      });

test('fishing uses desktop space, fits mobile results, and retains the anonymity preference', async ({
  page,
}) => {
  const guard = collectConsoleViolations(page);
  await installUserShell(page);
  await installFishingRoutes(page, fixtureWith());
  const session = userSession('level4');
  let writes = 0;
  await page.route('**/api/{me,session}', async (route) => {
    const request = route.request();
    if (request.method() === 'PATCH') {
      expect(new URL(request.url()).pathname).toBe('/api/me');
      expect(request.postDataJSON()).toEqual({ game_profile_public: true });
      expect(request.headers()['idempotency-key']).toBeTruthy();
      session.user.game_profile_public = true;
      writes += 1;
    }
    await route.fulfill(jsonResponse(session));
  });
  await page.setViewportSize({ width: 1920, height: 1080 });
  await page.goto(`${USER_ORIGIN}/games/fishing`);
  await expect(page.getByRole('heading', { name: 'Largest single catch' })).toBeVisible();
  expect((await page.locator('.fishing-page').boundingBox())!.width).toBeGreaterThan(1500);
  for (const table of await page.locator('.fishing-table-wrap').all()) {
    expect(await table.evaluate((node) => node.scrollWidth <= node.clientWidth)).toBe(true);
  }
  await expect(page.getByRole('link', { name: 'API access', exact: true })).toBeVisible();
  const anonymity = page.getByRole('switch', { name: 'Stay anonymous on leaderboards' });
  await expect(anonymity).toHaveCount(0);
  await page.getByRole('link', { name: 'Manage game anonymity' }).click();
  await expect(page).toHaveURL(`${USER_ORIGIN}/games#game-privacy`);
  await expect(anonymity).toBeInViewport();
  await expect(anonymity).toBeChecked();
  await anonymity.click();
  await expect.poll(() => writes).toBe(1);
  await expect(anonymity).not.toBeChecked();
  await page.reload();
  await expect(anonymity).not.toBeChecked();
  await page.goto(`${USER_ORIGIN}/games/fishing`);
  await expect(anonymity).toHaveCount(0);
  await page.screenshot({ path: '../tmp/usability-fishing-desktop.png', fullPage: true });
  await page.setViewportSize({ width: 390, height: 844 });
  for (const table of await page.locator('.fishing-table-wrap').all()) {
    expect(await table.evaluate((node) => node.scrollWidth <= node.clientWidth)).toBe(true);
  }
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true,
  );
  await page.screenshot({ path: '../tmp/usability-fishing-mobile.png', fullPage: true });
  guard.assertNone();
});

const ART_FISH_SPECS: Readonly<Record<string, { readonly tier: string; readonly size: number }>> = {
  whitebait: { tier: 'small', size: 10 },
  gudgeon: { tier: 'small', size: 11 },
  horse_mouth: { tier: 'small', size: 12 },
  smelt: { tier: 'small', size: 13 },
  loach: { tier: 'small', size: 14 },
  crucian: { tier: 'regular', size: 20 },
  tilapia: { tier: 'regular', size: 21 },
  yellow_catfish: { tier: 'regular', size: 22 },
  ayu: { tier: 'regular', size: 23 },
  stream_carp: { tier: 'regular', size: 24 },
  common_carp: { tier: 'big', size: 40 },
  snakehead: { tier: 'big', size: 41 },
  catfish: { tier: 'big', size: 42 },
  mandarin_fish: { tier: 'big', size: 43 },
  rainbow_trout: { tier: 'big', size: 44 },
  grass_carp: { tier: 'giant', size: 80 },
  silver_carp: { tier: 'giant', size: 81 },
  bighead_carp: { tier: 'giant', size: 82 },
  black_carp: { tier: 'giant', size: 83 },
  japanese_eel: { tier: 'giant', size: 84 },
  yellowcheek: { tier: 'legend', size: 120 },
  taimen: { tier: 'legend', size: 121 },
  koi: { tier: 'legend', size: 122 },
};

const ARTWORK_RESULTS = [
  ...fishArtwork.map((art) => {
    const spec = ART_FISH_SPECS[art.key];
    if (!spec) throw new Error(`Missing frozen fish fixture for ${art.key}`);
    return {
      ...RESULT,
      outcomes: [
        {
          ordinal: 0,
          species_key: art.key,
          tier: spec.tier,
          size_cm: spec.size,
          reward: '0',
          net_reward: '0',
          rake: { platform: '0', welfare: '0', thursday: '0' },
        },
      ],
      payout_total: '0',
      net_payout_total: '0',
      rake: { platform: '0', welfare: '0', thursday: '0' },
    };
  }),
  ...junkArtwork.map((art) => ({
    ...RESULT,
    outcomes: [
      {
        ordinal: 0,
        species_key: art.key,
        tier: 'junk',
        size_cm: 0,
        reward: '0',
        net_reward: '0',
        rake: { platform: '0', welfare: '0', thursday: '0' },
      },
    ],
    payout_total: '0',
    net_payout_total: '0',
    rake: { platform: '0', welfare: '0', thursday: '0' },
  })),
  ...treasureArtwork.map((art) => ({
    ...RESULT,
    outcomes: [
      {
        ordinal: 0,
        species_key: art.key,
        tier: 'treasure',
        size_cm: 0,
        reward: '0',
        net_reward: '0',
        rake: { platform: '0', welfare: '0', thursday: '0' },
      },
    ],
    payout_total: '0',
    net_payout_total: '0',
    rake: { platform: '0', welfare: '0', thursday: '0' },
  })),
];

test('Fishing pending survives reload, settles from authority, auto-ACKs, and permits a new batch', async ({
  context,
  page,
}) => {
  const consoleGuard = collectConsoleViolations(page);
  await installURLPersistenceObserver(context, FISHING_PERSISTENCE_MARKERS);
  await installUserShell(page);
  const fixture = fixtureWith();
  fixture.ackDelayMS = 750;
  await installFishingRoutes(page, fixture);
  await page.emulateMedia({ reducedMotion: 'no-preference' });
  await page.goto(`${USER_ORIGIN}/games/fishing`);

  await expect(page.getByRole('heading', { name: 'Pond fishing' })).toBeVisible();
  await page.getByRole('button', { name: 'Start fishing' }).click();
  await expect(page.locator('[data-phase="pending"]')).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Your catch is on its way' })).toBeVisible();
  expect(fixture.starts).toBe(1);
  expect(await page.locator('.fishing-result').count()).toBe(0);

  fixture.settlePending = true;
  await page.reload();
  await expect(page.locator('.fishing-result [data-batch-id]')).toHaveAttribute(
    'data-batch-id',
    BATCH_ID,
  );
  await expect(page.getByRole('heading', { name: 'Total catch', exact: true })).toBeVisible();
  await expect(page.locator('.fishing-result .fishing-art')).toHaveAttribute('data-art-key', 'koi');
  await expect(page.locator('.fishing-result')).toContainText('14.5');
  expect(fixture.settlements).toBe(1);

  await expect.poll(() => fixture.acks).toBe(1);
  await expect(page.getByRole('heading', { name: 'Total catch', exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Start fishing' })).toBeEnabled();
  await page.getByRole('button', { name: 'Start fishing' }).click();
  expect(fixture.starts).toBe(2);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true,
  );
  await assertNoSensitiveBrowserPersistence(page, FISHING_PERSISTENCE_MARKERS);
  consoleGuard.assertNone();
});

test('Fishing renders every frozen outcome with non-zero local artwork on the real games route', async ({
  page,
}) => {
  const consoleGuard = collectConsoleViolations(page);
  await installUserShell(page);
  const fixture = fixtureWith();
  fixture.preserveAfterACK = true;
  await installFishingRoutes(page, fixture);
  await page.emulateMedia({ reducedMotion: 'reduce' });

  expect(ARTWORK_RESULTS).toHaveLength(34);
  for (const result of ARTWORK_RESULTS) {
    fixture.state = {
      settlement_pending: null,
      unrevealed: result,
      has_more_unrevealed: false,
    };
    await page.goto(`${USER_ORIGIN}/games/fishing`);
    await expect(page.getByRole('heading', { name: 'Pond fishing' })).toBeVisible();
    const artwork = page.locator('.fishing-result .fishing-art');
    await expect(artwork).toHaveAttribute('data-art-key', result.outcomes[0].species_key);
    const box = await artwork.boundingBox();
    expect(box?.width ?? 0).toBeGreaterThan(0);
    expect(box?.height ?? 0).toBeGreaterThan(0);
    await expect(artwork.locator('svg')).toHaveAttribute('aria-hidden', 'true');
    expect(await artwork.locator('svg image, svg use').count()).toBe(0);
  }
  consoleGuard.assertNone();
});

test('Fishing recovery is identical across a second page and leaderboard identity stays closed', async ({
  browser,
  context,
  page,
}) => {
  await installURLPersistenceObserver(context, FISHING_PERSISTENCE_MARKERS);
  const fixture = fixtureWith({
    settlement_pending: null,
    unrevealed: RESULT,
    has_more_unrevealed: false,
  });
  fixture.balance = RESULT.balance;
  fixture.failACK = true;
  await installUserShell(page);
  await installFishingRoutes(page, fixture);
  await page.goto(`${USER_ORIGIN}/games/fishing`);
  await expect(page.getByRole('heading', { name: 'Total catch', exact: true })).toBeVisible();
  const firstResult = page.locator('.fishing-result [data-batch-id]');
  await expect(firstResult).toHaveAttribute('data-batch-id', BATCH_ID);
  await expect(page.locator('.fishing-result .fishing-art')).toHaveAttribute('data-art-key', 'koi');
  await expect(page.getByText('Anonymous angler').first()).toBeVisible();
  await expect(page.getByText('Public angler')).toBeVisible();
  await expect(page.getByText('No avatar angler')).toBeVisible();
  const avatar = page.locator('.fishing-board img');
  await expect(avatar).toHaveCount(1);
  await expect(avatar).toHaveAttribute('src', AVATAR_URL);
  await expect(avatar).toHaveAttribute('alt', '');
  await expect
    .poll(() => avatar.evaluate((node) => (node as HTMLImageElement).naturalWidth))
    .toBeGreaterThan(0);

  const secondContext = await browser.newContext({ serviceWorkers: 'block' });
  try {
    await installLoopbackNetworkBoundary(secondContext);
    const secondPage = await secondContext.newPage();
    await installUserShell(secondPage);
    await installFishingRoutes(secondPage, fixture);
    await secondPage.goto(`${USER_ORIGIN}/games/fishing`);
    await expect(
      secondPage.getByRole('heading', { name: 'Total catch', exact: true }),
    ).toBeVisible();
    await expect(secondPage.locator('.fishing-result [data-batch-id]')).toHaveAttribute(
      'data-batch-id',
      BATCH_ID,
    );
    await expect(secondPage.locator('.fishing-result .fishing-art')).toHaveAttribute(
      'data-art-key',
      'koi',
    );
    await expect(secondPage.getByRole('button', { name: 'Retry marking as viewed' })).toBeVisible();
    fixture.failACK = false;
    await secondPage.getByRole('button', { name: 'Retry marking as viewed' }).click();
    await expect(
      secondPage.getByRole('heading', { name: 'Total catch', exact: true }),
    ).toBeVisible();
    await secondPage.close();
  } finally {
    await secondContext.close();
  }

  await page.reload();
  await expect(page.getByRole('heading', { name: 'Total catch', exact: true })).toHaveCount(0);
  await assertNoSensitiveBrowserPersistence(page, FISHING_PERSISTENCE_MARKERS);
});

test('Fishing remains keyboard usable in Chinese at 390px, 200% zoom, both themes, and reduced motion', async ({
  context,
  page,
}) => {
  const consoleGuard = collectConsoleViolations(page);
  await installURLPersistenceObserver(context, FISHING_PERSISTENCE_MARKERS);
  await useNarrowReducedMotion(page);
  await installUserShell(page, 'zh');
  const fixture = fixtureWith();
  await installFishingRoutes(page, fixture);
  await page.goto(`${USER_ORIGIN}/games/fishing`);
  await expect(page.getByRole('heading', { name: '池塘垂钓' })).toBeVisible();
  await expect(page.getByRole('button', { name: /高级鱼饵/ })).toBeVisible();
  expect(await page.evaluate(() => matchMedia('(prefers-reduced-motion: reduce)').matches)).toBe(
    true,
  );
  await page.evaluate(() => window.scrollTo(0, 600));
  const scrollBeforeMenu = await page.evaluate(() => window.scrollY);
  expect(scrollBeforeMenu).toBeGreaterThan(100);
  await page.getByRole('button', { name: '打开导航' }).click();
  await expect(page.getByRole('link', { name: '账号' })).toBeVisible();
  await expect(page.locator('.nb-menu-button')).toBeInViewport();
  expect(await page.evaluate(() => window.scrollY)).toBeCloseTo(scrollBeforeMenu, 0);
  const drawer = page.locator('.nb-user-header__nav');
  const drawerBox = await drawer.boundingBox();
  await page.screenshot({ path: '../tmp/usability-mobile-drawer.png' });
  expect(drawerBox!.height).toBeGreaterThan(500);
  expect(drawerBox!.y + drawerBox!.height).toBeCloseTo((await page.viewportSize())!.height, 0);
  expect(await drawer.evaluate((node) => getComputedStyle(node).backgroundColor)).not.toBe(
    'rgba(0, 0, 0, 0)',
  );
  await expect(page.getByRole('link', { name: 'API 接入', exact: true })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.style.overflow)).toBe('hidden');
  await page.getByRole('combobox', { name: '主题' }).selectOption('light');
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'light');
  await page.getByRole('combobox', { name: '主题' }).selectOption('dark');
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
  await page.keyboard.press('Escape');
  expect(await page.evaluate(() => document.documentElement.style.overflow)).not.toBe('hidden');
  expect(await page.evaluate(() => window.scrollY)).toBeCloseTo(scrollBeforeMenu, 0);
  await page.getByRole('button', { name: '打开导航' }).click();
  await expect(page.getByRole('link', { name: '首页', exact: true })).toBeInViewport();
  await page.locator('.nb-menu-button').click();
  await expect(drawer).not.toBeVisible();
  expect(await page.evaluate(() => window.scrollY)).toBeCloseTo(scrollBeforeMenu, 0);
  await page.evaluate(() => {
    document.documentElement.style.zoom = '2';
  });
  expect(await page.evaluate(() => getComputedStyle(document.documentElement).zoom)).toBe('2');
  const zoomLayout = await page.evaluate(() => ({
    viewport: window.innerWidth,
    body: document.body.scrollWidth,
    zoom: getComputedStyle(document.documentElement).zoom,
  }));
  expect(zoomLayout.zoom).toBe('2');
  expect(zoomLayout.body <= zoomLayout.viewport).toBe(true);
  const start = page.getByRole('button', { name: '开始垂钓' });
  await tabTo(page, start);
  await expect(start).toBeFocused();
  await page.keyboard.press('Enter');
  await expect.poll(() => fixture.starts).toBe(1);
  await expect(page.getByRole('heading', { name: '正在等待收获' })).toBeVisible();
  consoleGuard.assertNone();
});

for (const theme of ['light', 'dark'] as const)
  for (const width of [390, 1440]) {
    test(`Pond gifts, return recovery and boards ${theme} ${width}`, async ({ page }) => {
      const faults: string[] = [];
      page.on('pageerror', (error) => faults.push(error.message));
      await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
      await page.emulateMedia({ reducedMotion: 'reduce' });
      await installUserShell(page, 'zh');
      await page.addInitScript((value) => localStorage.setItem('nb.theme', value), theme);
      const session = userSession('level4');
      session.user.game_profile_public = true;
      await page.route('**/api/session', (route) => route.fulfill(jsonResponse(session)));
      const donor = '快乐的池塘朋友'.repeat(16);
      const outcomes = Array.from({ length: 10 }, (_, ordinal) => ({
        ordinal,
        species_key: 'branch',
        tier: 'junk',
        size_cm: 0,
        reward: '0',
        net_reward: '0',
        rake: { platform: '0', welfare: '0', thursday: '0' },
        ...(ordinal === 0 || ordinal === 1
          ? { pond: { return_state: 'available', gift: null } }
          : {}),
        ...(ordinal === 0 ? { species_key: 'boot' } : {}),
        ...(ordinal === 1 ? { species_key: 'fry' } : {}),
        ...(ordinal === 2
          ? {
              species_key: 'old_tire',
              pond: {
                return_state: 'gift',
                gift: { kind: 'public', display_name: donor, avatar_url: AVATAR_URL },
              },
            }
          : {}),
      }));
      const result = {
        ...RESULT,
        count: 10,
        entry_total: '25',
        payment: { general: '25', game: '0' },
        outcomes,
        payout_total: '0',
        net_payout_total: '0',
      };
      const fixture = fixtureWith({
        settlement_pending: null,
        unrevealed: result,
        has_more_unrevealed: false,
      });
      fixture.preserveAfterACK = true;
      await installFishingRoutes(page, fixture);
      let returns = 0;
      const resources: string[] = [];
      await page.route('**/api/games/fishing/batches/*/outcomes/*/return', async (route) => {
        const request = route.request();
        expect(request.method()).toBe('POST');
        expect(request.postData()).toBeNull();
        expect(request.headers()['idempotency-key']).toBeUndefined();
        resources.push(new URL(request.url()).pathname);
        const ordinal = Number(resources.at(-1)!.split('/').at(-2));
        returns += 1;
        if (returns === 1) {
          await route.abort('failed');
          return;
        }
        const state = ordinal === 1 ? 'released' : 'returned';
        Object.assign(outcomes[ordinal], { pond: { return_state: state, gift: null } });
        await route.fulfill(
          jsonResponse({
            batch_id: BATCH_ID,
            ordinal,
            return_state: state,
            returned_at: 1_800_000_000,
          }),
        );
      });
      const boardRequests: string[] = [];
      await page.route('**/api/games/fishing/pond/leaderboard?*', async (route) => {
        const url = new URL(route.request().url());
        const board = url.searchParams.get('board')!;
        const period = url.searchParams.get('period')!;
        boardRequests.push(board + ':' + period);
        await route.fulfill(
          jsonResponse({
            board,
            period,
            window_start: period === 'all' ? null : 1_700_000_000,
            entries:
              period === '30d'
                ? []
                : [
                    {
                      rank: '1',
                      item_count: '12000',
                      identity: { kind: 'public', display_name: donor, avatar_url: AVATAR_URL },
                      is_me: false,
                    },
                  ],
            me:
              period === '30d'
                ? null
                : { rank: '21', item_count: '2', identity: { kind: 'anonymous' }, is_me: true },
          }),
        );
      });
      const capture = async (name: string) => {
        const directory = process.env.NONBIRI_POND_LAYOUT_EVIDENCE;
        if (!directory) return;
        mkdirSync(directory, { recursive: true });
        await page.screenshot({ path: resolve(directory, `${theme}-${width}-${name}.png`) });
      };
      await page.goto(`${USER_ORIGIN}/games/fishing`);
      const caught = page.locator('.fishing-outcome');
      await expect(caught).toHaveCount(10);
      await expect(page.getByText(/之后开启游戏匿名.*保留署名/)).toBeVisible();
      const gift = caught.filter({ has: page.locator('[data-art-key="old_tire"]') });
      await expect(gift).toContainText(donor);
      await expect(gift).toContainText('来自');
      await expect(gift).toContainText('净到账 0 通用积分');
      await expect(gift.locator('img[src="' + AVATAR_URL + '"]')).toBeVisible();
      await expect(gift.getByRole('button', { name: '投回池塘', exact: true })).toHaveCount(0);
      await expect(gift).not.toContainText('原本');
      await gift.scrollIntoViewIfNeeded();
      await capture('gift');
      const junk = page.locator('.fishing-outcome[data-ordinal="0"]');
      await junk.getByRole('button', { name: '投回池塘', exact: true }).click();
      await expect(junk.getByRole('alert')).toContainText('尚未确认结果');
      await junk.scrollIntoViewIfNeeded();
      await capture('retry');
      await junk.getByRole('button', { name: '重试这件收获' }).click();
      await expect(junk).toContainText('已投回池塘');
      expect(resources[0]).toBe(resources[1]);
      const fry = page.locator('.fishing-outcome[data-ordinal="1"]');
      await fry.getByRole('button', { name: '放生', exact: true }).click();
      await expect(fry).toContainText('已放生');
      expect(boardRequests).toEqual([]);
      await page.getByRole('tab', { name: '环保榜', exact: true }).click();
      const board = page.locator('.fishing-pond-board');
      await expect(board.getByRole('radio', { name: '过去 7 天' })).toBeChecked();
      await expect(board).toContainText('匿名垂钓者');
      expect(boardRequests).toEqual(['cleanup:7d']);
      await board.scrollIntoViewIfNeeded();
      await capture('board');
      await board.getByRole('radiogroup').getByText('过去 30 天', { exact: true }).click();
      await expect(board).toContainText('还没有记录');
      await capture('empty');
      await board.getByRole('radiogroup').getByText('历史', { exact: true }).click();
      await expect(board).toContainText(donor);
      await page.getByRole('tab', { name: '馈赠榜', exact: true }).click();
      await expect(board.getByRole('radio', { name: '过去 7 天' })).toBeChecked();
      await expect(board).toContainText(donor);
      expect(boardRequests).toEqual(['cleanup:7d', 'cleanup:30d', 'cleanup:all', 'gifts:7d']);
      for (const table of await page.locator('.nb-table-wrap').all())
        expect(await table.evaluate((node) => node.scrollWidth <= node.clientWidth)).toBe(true);
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
        true,
      );
      session.user.game_profile_public = false;
      Object.assign(outcomes[3], { pond: { return_state: 'available', gift: null } });
      fixture.preserveAfterACK = false;
      await page.reload();
      await expect(page.locator('.fishing-gift-source')).toContainText(donor);
      await expect(page.getByRole('button', { name: '投回池塘', exact: true })).toBeDisabled();
      await page.locator('.fishing-outcome[data-ordinal="3"]').scrollIntoViewIfNeeded();
      await capture('anonymous');
      await expect.poll(() => fixture.state.unrevealed).toBeNull();
      await expect(page.getByRole('button', { name: '放生', exact: true })).toHaveCount(0);
      let privacyWrites = 0;
      await page.route('**/api/me', async (route) => {
        if (route.request().method() === 'PATCH') {
          privacyWrites += 1;
          expect(route.request().postDataJSON()).toEqual({ game_profile_public: true });
          if (privacyWrites === 1) {
            await route.fulfill(
              jsonResponse(
                { error: { code: 'invalid_request', message: 'Synthetic retryable profile save' } },
                400,
              ),
            );
            return;
          }
          session.user.game_profile_public = true;
        }
        await route.fulfill(jsonResponse(session));
      });
      const privacy = page.locator('.fishing-pond-disclosure');
      const anonymity = privacy.getByRole('switch', { name: '在排行榜中匿名' });
      await expect(anonymity).toBeChecked();
      await privacy.evaluate((node) => node.scrollIntoView({ block: 'center' }));
      await capture('privacy');
      expect(await privacy.evaluate((node) => node.scrollWidth <= node.clientWidth)).toBe(true);
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
        true,
      );
      await anonymity.click();
      await expect(privacy.getByRole('alert')).toContainText('Synthetic retryable profile save');
      await expect(page.getByRole('button', { name: '投回池塘', exact: true })).toBeDisabled();
      await anonymity.click();
      await expect(page.getByRole('button', { name: '投回池塘', exact: true })).toBeEnabled();
      await expect(page.getByText(/之后开启游戏匿名.*保留署名/)).toHaveCount(1);
      await expect(page).toHaveURL(`${USER_ORIGIN}/games/fishing`);
      await expect(page.locator('[data-batch-id]')).toHaveAttribute('data-batch-id', BATCH_ID);
      await page.getByRole('button', { name: '投回池塘', exact: true }).click();
      await expect(page.locator('.fishing-outcome[data-ordinal="3"]')).toContainText('已投回池塘');
      expect(privacyWrites).toBe(2);
      expect(faults).toEqual([]);
    });
  }
