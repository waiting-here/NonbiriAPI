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
} from './support';
import { USER_ORIGIN } from './ports';
import { gamesSnapshotWire } from '../../src/user/games/common/testFixtures';
import { fishArtwork, junkArtwork, treasureArtwork } from '../../src/user/games/fishing/artRegistry';

const BATCH_ID = 'fb_AAAAAAAAAAAAAAAAAAAAAA';
const RESULT = {
  batch_id: BATCH_ID,
  bait: 'worm',
  count: 1,
  unit_price: '2.5',
  entry_total: '2.5',
  outcomes: [
    { ordinal: 0, species_key: 'koi', tier: 'legend', size_cm: 180, reward: '12' },
  ],
  payout_total: '12',
  balance: '14.5',
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

async function installFishingRoutes(page: import('@playwright/test').Page, fixture: FishingFixture) {
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
    if (
      requestURL.pathname === '/api/games/fishing/leaderboard' &&
      request.method() === 'GET'
    ) {
      if (requestURL.searchParams.get('board') === 'total') {
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
            board: 'single',
            window_start: null,
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
          jsonResponse({ error: { code: 'invalid_request', message: 'Unexpected test intent.' } }, 400),
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
          jsonResponse({ error: { code: 'temporarily_unavailable', message: 'Synthetic ACK failure.' } }, 503),
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

async function installUserShell(page: import('@playwright/test').Page, language: 'en' | 'zh' = 'en') {
  await page.addInitScript((selectedLanguage) => {
    localStorage.setItem('nb.lang', selectedLanguage);
    localStorage.setItem('nb.theme', 'dark');
  }, language);
  await mockPublicConfig(page, 'user');
  await mockRoleSession(page, 'user', 'level4');
}

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
        },
      ],
      payout_total: '0',
    };
  }),
  ...junkArtwork.map((art) => ({
    ...RESULT,
    outcomes: [
      { ordinal: 0, species_key: art.key, tier: 'junk', size_cm: 0, reward: '0' },
    ],
    payout_total: '0',
  })),
  ...treasureArtwork.map((art) => ({
    ...RESULT,
    outcomes: [
      { ordinal: 0, species_key: art.key, tier: 'treasure', size_cm: 0, reward: '0' },
    ],
    payout_total: '0',
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

  await expect(
    page.getByRole('heading', { name: 'A quiet cast, an authoritative catch' }),
  ).toBeVisible();
  await page.getByRole('button', { name: 'Start this batch' }).click();
  await expect(page.locator('[data-phase="pending"]')).toBeVisible();
  await expect(page.getByRole('heading', { name: 'This batch is already accepted' })).toBeVisible();
  expect(fixture.starts).toBe(1);
  expect(await page.locator('.fishing-result').count()).toBe(0);

  fixture.settlePending = true;
  await page.reload();
  await expect(page.locator('.fishing-result [data-batch-id]')).toHaveAttribute(
    'data-batch-id',
    BATCH_ID,
  );
  await expect(page.getByRole('heading', { name: 'Total catch', exact: true })).toBeVisible();
  await expect(page.locator('.fishing-art')).toHaveAttribute('data-art-key', 'koi');
  await expect(page.locator('.fishing-result')).toContainText('14.5');
  expect(fixture.settlements).toBe(1);

  await expect.poll(() => fixture.acks).toBe(1);
  await expect(page.getByRole('heading', { name: 'Total catch', exact: true })).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Start this batch' })).toBeEnabled();
  await page.getByRole('button', { name: 'Start this batch' }).click();
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
    await expect(
      page.getByRole('heading', { name: 'A quiet cast, an authoritative catch' }),
    ).toBeVisible();
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
  await expect(page.locator('.fishing-board img')).toHaveCount(0);

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
    await expect(secondPage.getByRole('button', { name: 'Retry result ACK' })).toBeVisible();
    fixture.failACK = false;
    await secondPage.getByRole('button', { name: 'Retry result ACK' }).click();
    await expect(
      secondPage.getByRole('heading', { name: 'Total catch', exact: true }),
    ).toHaveCount(0);
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
  await expect(page.getByRole('heading', { name: '悠闲抛竿，权威收获' })).toBeVisible();
  await expect(page.getByRole('button', { name: /高级鱼饵/ })).toBeVisible();
  expect(await page.evaluate(() => matchMedia('(prefers-reduced-motion: reduce)').matches)).toBe(
    true,
  );
  await page.getByRole('button', { name: '打开导航' }).click();
  await expect(page.getByRole('link', { name: '账号' })).toBeVisible();
  await page.getByRole('combobox', { name: '主题' }).selectOption('light');
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'light');
  await page.getByRole('combobox', { name: '主题' }).selectOption('dark');
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
  await page.keyboard.press('Escape');
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
  const start = page.getByRole('button', { name: '开始本批' });
  await tabTo(page, start);
  await expect(start).toBeFocused();
  await page.keyboard.press('Enter');
  await expect.poll(() => fixture.starts).toBe(1);
  await expect(page.getByRole('heading', { name: '本批已经受理' })).toBeVisible();
  consoleGuard.assertNone();
});
