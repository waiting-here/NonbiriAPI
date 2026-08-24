import { Buffer } from 'node:buffer';
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
import { fishArtwork, junkArtwork, treasureArtwork } from '../../src/user/games/fishing/artRegistry';

const RESULT = {
  round_id: `grd_${'wxyz234567'.repeat(3)}`,
  game_id: 'fishing',
  game_version: 1,
  bait: 'worm',
  price: '2500000',
  species_key: 'koi',
  tier: 'legend',
  size_cm: 180,
  is_junk: false,
  is_treasure: false,
  meter: true,
  credits_won: '12000000',
  credits: '14500000',
  settled_at: 1_787_450_010,
};
const FISHING_PERSISTENCE_MARKERS = [RESULT.round_id, String(RESULT.settled_at)] as const;

const AVATAR_URL = 'https://cdn.discordapp.com/avatars/a/b.png?size=64';
const AVATAR_PNG = Buffer.from(
  'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=',
  'base64',
);

const CONFIG = {
  master_enabled: true,
  credits: '5000000',
  game_profile_public: false,
  games: [{
    id: 'fishing',
    version: 1,
    enabled: true,
    params: {
      baits: [
        { id: 'worm', price: '2500000' },
        { id: 'lure', price: '5000000' },
        { id: 'premium', price: '7500000' },
      ],
      rtp_percent: { standard: 90, premium: 88 },
      treasure_multipliers: { bottle: 2, clover: 3, shell: 5 },
    },
  }],
};

type FishingFixtureState = {
  pending_round: Record<string, unknown> | null;
  unrevealed_result: Record<string, unknown> | null;
  has_more_unrevealed: boolean;
};

function jsonResponse(body: unknown, status = 200) {
  return {
    status,
    headers: { 'content-type': 'application/json', 'cache-control': 'no-store' },
    body: JSON.stringify(body),
  };
}

async function installFishingRoutes(page: import('@playwright/test').Page, fixture: {
  state: FishingFixtureState;
  profilePublic: boolean;
  starts: number;
  settles: number;
  acks: number;
}) {
  await page.route('**/api/games**', async (route) => {
    const requestURL = new URL(route.request().url());
    if (requestURL.origin !== USER_ORIGIN) {
      await route.fallback();
      return;
    }
    if (requestURL.pathname === '/api/games' && route.request().method() === 'GET') {
      await route.fulfill(jsonResponse({ ...CONFIG, credits: fixture.state.pending_round ? '2500000' : CONFIG.credits, game_profile_public: fixture.profilePublic }));
      return;
    }
    if (requestURL.pathname === '/api/games/fishing/state' && route.request().method() === 'GET') {
      await route.fulfill(jsonResponse(fixture.state));
      return;
    }
    if (requestURL.pathname === '/api/games/fishing/leaderboard' && route.request().method() === 'GET') {
      if (requestURL.searchParams.get('board') === 'total') {
        await route.fulfill(jsonResponse({ board: 'total', window_start: 1_787_000_000, entries: [], me: null }));
      } else {
        await route.fulfill(jsonResponse({
          board: 'single',
          window_start: null,
          entries: [
            { rank: 1, species_key: 'taimen', size_cm: 190, is_me: false },
            { rank: 2, species_key: 'koi', size_cm: 180, display_name: 'Public angler', avatar_url: AVATAR_URL, level4_badge: true, is_me: false },
            { rank: 3, species_key: 'koi', size_cm: 170, display_name: 'No avatar angler', is_me: false },
          ],
          me: null,
        }));
      }
      return;
    }
    await route.fallback();
  });

  await page.route('**/api/games/fishing/rounds**', async (route) => {
    const requestURL = new URL(route.request().url());
    if (requestURL.origin !== USER_ORIGIN) {
      await route.fallback();
      return;
    }
    if (requestURL.pathname === '/api/games/fishing/rounds' && route.request().method() === 'POST') {
      fixture.starts += 1;
      fixture.state = {
        pending_round: { round_id: RESULT.round_id, bait: 'worm', price: '2500000', created_at: 1, auto_settle_at: 2 },
        unrevealed_result: null,
        has_more_unrevealed: false,
      };
      await route.fulfill(jsonResponse({
        round_id: RESULT.round_id,
        game_id: 'fishing',
        game_version: 1,
        bait: 'worm',
        price: '2500000',
        credits: '2500000',
        state: 'pending',
        created_at: 1,
        auto_settle_at: 2,
        idempotent_replay: false,
      }));
      return;
    }
    if (requestURL.pathname.endsWith('/settle') && route.request().method() === 'POST') {
      fixture.settles += 1;
      fixture.state = { pending_round: null, unrevealed_result: RESULT, has_more_unrevealed: false };
      await route.fulfill(jsonResponse({ ...RESULT, idempotent_replay: false }));
      return;
    }
    if (requestURL.pathname.endsWith('/ack') && route.request().method() === 'POST') {
      fixture.acks += 1;
      fixture.state = { pending_round: null, unrevealed_result: null, has_more_unrevealed: false };
      await route.fulfill({ status: 204, headers: { 'cache-control': 'no-store' } });
      return;
    }
    await route.fallback();
  });

  await page.route('**/api/me', async (route) => {
    const requestURL = new URL(route.request().url());
    if (requestURL.origin !== USER_ORIGIN || route.request().method() !== 'PATCH') {
      await route.fallback();
      return;
    }
    const body = route.request().postDataJSON() as { game_profile_public?: unknown };
    fixture.profilePublic = body.game_profile_public === true;
    await route.fulfill(jsonResponse({ user: { game_profile_public: fixture.profilePublic } }));
  });
}

async function installAvatarFixture(page: import('@playwright/test').Page) {
  await page.route(AVATAR_URL, async (route) => {
    await route.fulfill({
      status: 200,
      headers: { 'content-type': 'image/png', 'cache-control': 'no-store' },
      body: AVATAR_PNG,
    });
  });
}

async function installAvatarFailureFixture(page: import('@playwright/test').Page) {
  await page.route(AVATAR_URL, async (route) => {
    await route.fulfill({ status: 204, headers: { 'cache-control': 'no-store' } });
  });
}

async function installUserShell(page: import('@playwright/test').Page) {
  await page.addInitScript(() => {
    localStorage.setItem('nb.lang', 'en');
    localStorage.setItem('nb.theme', 'dark');
  });
  await mockPublicConfig(page, 'user');
  await mockRoleSession(page, 'user', 'level4');
}

const ART_FISH_SPECS: Readonly<Record<string, { readonly tier: string; readonly size: number }>> = {
  whitebait: { tier: 'small', size: 10 }, gudgeon: { tier: 'small', size: 11 }, horse_mouth: { tier: 'small', size: 12 }, smelt: { tier: 'small', size: 13 }, loach: { tier: 'small', size: 14 },
  crucian: { tier: 'regular', size: 20 }, tilapia: { tier: 'regular', size: 21 }, yellow_catfish: { tier: 'regular', size: 22 }, ayu: { tier: 'regular', size: 23 }, stream_carp: { tier: 'regular', size: 24 },
  common_carp: { tier: 'big', size: 40 }, snakehead: { tier: 'big', size: 41 }, catfish: { tier: 'big', size: 42 }, mandarin_fish: { tier: 'big', size: 43 }, rainbow_trout: { tier: 'big', size: 44 },
  grass_carp: { tier: 'giant', size: 80 }, silver_carp: { tier: 'giant', size: 81 }, bighead_carp: { tier: 'giant', size: 82 }, black_carp: { tier: 'giant', size: 83 }, japanese_eel: { tier: 'giant', size: 84 },
  yellowcheek: { tier: 'legend', size: 120 }, taimen: { tier: 'legend', size: 121 }, koi: { tier: 'legend', size: 122 },
};

function artworkRoundId(index: number): string {
  const alphabet = 'abcdefghijklmnopqrstuv234567';
  let remainder = index;
  let suffix = '';
  do {
    suffix = alphabet[remainder % alphabet.length] + suffix;
    remainder = Math.floor(remainder / alphabet.length);
  } while (remainder > 0);
  return `grd_${'a'.repeat(26 - suffix.length)}${suffix}`;
}

const ARTWORK_RESULTS = [
  ...fishArtwork.map((art, index) => {
    const spec = ART_FISH_SPECS[art.key];
    if (!spec) throw new Error(`Missing frozen fish fixture for ${art.key}`);
    return {
      ...RESULT,
      round_id: artworkRoundId(index),
      species_key: art.key,
      tier: spec.tier,
      size_cm: spec.size,
      is_junk: false,
      is_treasure: false,
      meter: spec.size >= 100,
      credits_won: '0',
    };
  }),
  ...junkArtwork.map((art, index) => ({
    ...RESULT,
    round_id: artworkRoundId(fishArtwork.length + index),
    species_key: art.key,
    tier: 'junk',
    size_cm: 0,
    is_junk: true,
    is_treasure: false,
    meter: false,
    credits_won: '0',
  })),
  ...treasureArtwork.map((art, index) => ({
    ...RESULT,
    round_id: artworkRoundId(fishArtwork.length + junkArtwork.length + index),
    species_key: art.key,
    tier: 'treasure',
    size_cm: 0,
    is_junk: false,
    is_treasure: true,
    meter: false,
    credits_won: '0',
  })),
];

test('Fishing pending survives reload, settles from the server, ACKs, and permits a new round', async ({ context, page }) => {
  const consoleGuard = collectConsoleViolations(page);
  await installURLPersistenceObserver(context, FISHING_PERSISTENCE_MARKERS);
  await installUserShell(page);
  const fixture: {
    state: FishingFixtureState;
    profilePublic: boolean;
    starts: number;
    settles: number;
    acks: number;
  } = {
    state: { pending_round: null, unrevealed_result: null, has_more_unrevealed: false } satisfies FishingFixtureState,
    profilePublic: false,
    starts: 0,
    settles: 0,
    acks: 0,
  };
  await installFishingRoutes(page, fixture);
  await installAvatarFailureFixture(page);
  await page.emulateMedia({ reducedMotion: 'no-preference' });
  await page.goto(`${USER_ORIGIN}/games`);

  await expect(page.getByRole('heading', { name: 'Pond fishing' })).toBeVisible();
  await page.getByRole('button', { name: 'Cast line' }).click();
  await expect(page.locator('[data-phase="waiting"]')).toBeVisible();
  expect(fixture.starts).toBe(1);
  expect(await page.locator('.fishing-result').count()).toBe(0);

  await page.reload();
  await expect(page.locator('[data-phase="waiting"], [data-phase="reeling"], [data-phase="settling"]')).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Catch confirmed' })).toBeVisible({ timeout: 7_000 });
  expect(fixture.settles).toBe(1);
  await expect(page.locator('[title="14,500,000 milli-credits"]').first()).toBeVisible();
  await expect(page.locator('tr').filter({ hasText: 'Public angler' }).getByRole('img', { name: 'Avatar unavailable' })).toBeVisible();
  await expect(page.locator('tr').filter({ hasText: 'No avatar angler' }).getByRole('img', { name: 'Avatar unavailable' })).toBeVisible();

  await page.getByRole('button', { name: 'Mark as viewed' }).click();
  await expect.poll(() => fixture.acks).toBe(1);
  await expect(page.getByRole('heading', { name: 'Catch confirmed' })).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Cast line' })).toBeEnabled();
  await page.getByRole('button', { name: 'Cast line' }).click();
  expect(fixture.starts).toBe(2);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await assertNoSensitiveBrowserPersistence(page, FISHING_PERSISTENCE_MARKERS);
  consoleGuard.assertNone();
});

test('Fishing renders every frozen outcome with non-zero local artwork on the real games route', async ({ page }) => {
  const consoleGuard = collectConsoleViolations(page);
  await installUserShell(page);
  const fixture: {
    state: FishingFixtureState;
    profilePublic: boolean;
    starts: number;
    settles: number;
    acks: number;
  } = {
    state: { pending_round: null, unrevealed_result: null, has_more_unrevealed: false } satisfies FishingFixtureState,
    profilePublic: false,
    starts: 0,
    settles: 0,
    acks: 0,
  };
  await installFishingRoutes(page, fixture);
  await installAvatarFailureFixture(page);

  expect(ARTWORK_RESULTS).toHaveLength(34);
  for (const outcome of ARTWORK_RESULTS) {
    fixture.state = { pending_round: null, unrevealed_result: outcome, has_more_unrevealed: false };
    await page.goto(`${USER_ORIGIN}/games`);
    await expect(page.getByRole('heading', { name: 'Pond fishing' })).toBeVisible();
    const artwork = page.locator('.fishing-result .fishing-art');
    await expect(artwork).toHaveAttribute('data-art-key', outcome.species_key);
    const box = await artwork.boundingBox();
    expect(box?.width ?? 0).toBeGreaterThan(0);
    expect(box?.height ?? 0).toBeGreaterThan(0);
    await expect(artwork.locator('svg')).toHaveAttribute('aria-hidden', 'true');
    expect(await artwork.locator('svg image, svg use').count()).toBe(0);
  }
  consoleGuard.assertNone();
});

test('Fishing recovery is identical across a second page, and public identity is opt-in', async ({ browser, context, page }) => {
  await installURLPersistenceObserver(context, FISHING_PERSISTENCE_MARKERS);
  const fixture = {
    state: { pending_round: null, unrevealed_result: RESULT, has_more_unrevealed: false } satisfies FishingFixtureState,
    profilePublic: false,
    starts: 0,
    settles: 0,
    acks: 0,
  };
  await installUserShell(page);
  await installFishingRoutes(page, fixture);
  await installAvatarFixture(page);
  await page.goto(`${USER_ORIGIN}/games`);
  await expect(page.getByRole('heading', { name: 'Catch confirmed' })).toBeVisible();
  const firstResult = page.locator('.fishing-result');
  const firstOutcome = {
    roundId: await firstResult.getAttribute('data-round-id'),
    speciesKey: await firstResult.getAttribute('data-species-key'),
    creditsWon: await firstResult.getAttribute('data-credits-won'),
  };
  expect(firstOutcome).toEqual({ roundId: RESULT.round_id, speciesKey: RESULT.species_key, creditsWon: RESULT.credits_won });
  await expect(page.getByText('Anonymous angler').first()).toBeVisible();
  await expect(page.locator('.fishing-leaderboard tbody tr').first().locator('img')).toHaveCount(0);
  await expect(page.locator('.fishing-leaderboard tbody tr').nth(1).locator('img')).toHaveAttribute('src', AVATAR_URL);
  await expect(page.locator('.fishing-leaderboard tbody tr').nth(1).locator('img')).toHaveAttribute('referrerpolicy', 'no-referrer');
  await expect(page.locator('tr').filter({ hasText: 'No avatar angler' }).getByRole('img', { name: 'Avatar unavailable' })).toBeVisible();
  await expect(page.getByText('L4', { exact: true })).toBeVisible();

  const secondContext = await browser.newContext({ serviceWorkers: 'block' });
  try {
    await installLoopbackNetworkBoundary(secondContext);
    const secondPage = await secondContext.newPage();
    await installUserShell(secondPage);
    await installFishingRoutes(secondPage, fixture);
    await installAvatarFixture(secondPage);
    await secondPage.goto(`${USER_ORIGIN}/games`);
    await expect(secondPage.getByRole('heading', { name: 'Catch confirmed' })).toBeVisible();
    const secondResult = secondPage.locator('.fishing-result');
    await expect(secondResult).toHaveAttribute('data-round-id', firstOutcome.roundId ?? '');
    await expect(secondResult).toHaveAttribute('data-species-key', firstOutcome.speciesKey ?? '');
    await expect(secondResult).toHaveAttribute('data-credits-won', firstOutcome.creditsWon ?? '');

    await secondPage.getByRole('checkbox', { name: 'Show my profile' }).click();
    await expect.poll(() => secondPage.getByRole('checkbox', { name: 'Show my profile' }).isChecked()).toBe(true);
    await expect(secondPage.getByRole('button', { name: 'Mark as viewed' })).toBeVisible();
    await secondPage.getByRole('button', { name: 'Mark as viewed' }).click();
    await expect.poll(() => fixture.acks).toBe(1);
    await expect(secondPage.getByRole('heading', { name: 'Catch confirmed' })).toHaveCount(0);
    await secondPage.close();
  } finally {
    await secondContext.close();
  }

  await page.reload();
  await expect(page.getByRole('heading', { name: 'Catch confirmed' })).toHaveCount(0);
  await assertNoSensitiveBrowserPersistence(page, FISHING_PERSISTENCE_MARKERS);
});

test('Fishing remains keyboard usable at 390px, 200% zoom, both themes, and zh with reduced motion', async ({ context, page }) => {
  const consoleGuard = collectConsoleViolations(page);
  await installURLPersistenceObserver(context, FISHING_PERSISTENCE_MARKERS);
  await useNarrowReducedMotion(page);
  await installUserShell(page);
  const fixture = {
    state: { pending_round: null, unrevealed_result: null, has_more_unrevealed: false } satisfies FishingFixtureState,
    profilePublic: false,
    starts: 0,
    settles: 0,
    acks: 0,
  };
  await installFishingRoutes(page, fixture);
  await installAvatarFailureFixture(page);
  await page.goto(`${USER_ORIGIN}/games`);
  await expect(page.getByRole('heading', { name: 'Pond fishing' })).toBeVisible();
  await expect(page.getByRole('button', { name: /Premium lure/ })).toBeVisible();
  expect(await page.evaluate(() => matchMedia('(prefers-reduced-motion: reduce)').matches)).toBe(true);
  await page.getByRole('combobox', { name: 'Theme' }).selectOption('light');
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'light');
  await page.getByRole('combobox', { name: 'Theme' }).selectOption('dark');
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
  await page.getByRole('button', { name: '中文' }).click();
  await expect(page.locator('html')).toHaveAttribute('lang', 'zh-CN');
  await expect(page.getByRole('heading', { name: '池塘垂钓' })).toBeVisible();
  await page.getByRole('button', { name: 'EN', exact: true }).click();
  await expect(page.locator('html')).toHaveAttribute('lang', 'en');
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
  const cast = page.getByRole('button', { name: 'Cast line' });
  await tabTo(page, cast);
  await expect(cast).toBeFocused();
  await page.keyboard.press('Enter');
  await expect.poll(() => fixture.starts).toBe(1);
  await expect(page.getByRole('heading', { name: 'Catch confirmed' })).toBeVisible();
  consoleGuard.assertNone();
});
