import { mkdir } from 'node:fs/promises';
import { join } from 'node:path';
import { expect, test } from './test';
import { collectConsoleViolations, mockPublicConfig, mockRoleSession } from './support';
import { USER_ORIGIN } from './ports';
import { gamesSnapshotWire } from '../../src/user/games/common/testFixtures';

const BATCH_ID = 'fb_AAAAAAAAAAAAAAAAAAAAAA';
const LONG_BLUE_LENGTH = `1${'9'.repeat(127)}`;
const UNSAFE_BLUE_LENGTH = '90071992547409931234567890';
const BLUE_IMAGE_PATH = 'blue-fat-fish';

type Page = import('@playwright/test').Page;

type Board = 'single' | 'recent_single' | 'total';

interface FishingState {
  readonly settlement_pending: null;
  readonly unrevealed: Record<string, unknown> | null;
  readonly has_more_unrevealed: false;
}

interface FishingFixture {
  state: FishingState;
  readonly boardRequests: Record<Board, number>;
  ackRequests: number;
}

function jsonResponse(body: unknown, status = 200) {
  return {
    status,
    headers: { 'content-type': 'application/json', 'cache-control': 'no-store' },
    body: JSON.stringify(body),
  };
}

const BLUE_RESULT = {
  ordinal: 1,
  species_key: 'koi',
  tier: 'legend',
  size_cm: 122,
  reward: '2',
  blue_fat_fish_length_cm: LONG_BLUE_LENGTH,
};

const OLD_RESULT = {
  ordinal: 0,
  species_key: 'taimen',
  tier: 'legend',
  size_cm: 121,
  reward: '1',
};

const FILLER_RESULTS = Array.from({ length: 8 }, (_, index) => ({
  ordinal: index + 2,
  species_key: 'whitebait',
  tier: 'small',
  size_cm: 10,
  reward: '0',
}));

const RESULT = {
  batch_id: BATCH_ID,
  bait: 'worm',
  count: 10,
  unit_price: '2.5',
  entry_total: '25',
  rules_version: 1,
  payment: { general: '25', game: '0' },
  outcomes: [OLD_RESULT, BLUE_RESULT, ...FILLER_RESULTS],
  payout_total: '3',
  balance: '4999995',
  game_balance: '0',
  settled_at: 1_800_000_111,
  idempotent_replay: false,
};

function fixtureWithResult(): FishingFixture {
  return {
    state: {
      settlement_pending: null,
      unrevealed: RESULT,
      has_more_unrevealed: false,
    },
    boardRequests: { single: 0, recent_single: 0, total: 0 },
    ackRequests: 0,
  };
}

function boardBody(board: Board) {
  if (board === 'single') {
    return {
      board,
      window_start: null,
      entries: [
        {
          rank: '1',
          species_key: 'koi',
          size_cm: 122,
          blue_fat_fish_length_cm: '262',
          identity: { kind: 'anonymous' },
          is_me: false,
        },
        {
          rank: '2',
          species_key: 'taimen',
          size_cm: 121,
          identity: { kind: 'anonymous' },
          is_me: false,
        },
      ],
      me: null,
    };
  }
  if (board === 'recent_single') {
    return {
      board,
      window_start: 1_797_408_000,
      entries: [
        {
          rank: '1',
          species_key: 'koi',
          size_cm: 122,
          blue_fat_fish_length_cm: LONG_BLUE_LENGTH,
          identity: { kind: 'anonymous' },
          is_me: false,
        },
        {
          rank: '2',
          species_key: 'koi',
          size_cm: 122,
          blue_fat_fish_length_cm: UNSAFE_BLUE_LENGTH,
          identity: { kind: 'public', display_name: 'Fixture angler', avatar_url: null },
          is_me: false,
        },
      ],
      me: null,
    };
  }
  return {
    board,
    window_start: 1_797_408_000,
    entries: [
      {
        rank: '1',
        total_credits: '12345678901234567890.125',
        identity: { kind: 'anonymous' },
        is_me: false,
      },
    ],
    me: null,
  };
}

async function installFishingRoutes(page: Page, fixture: FishingFixture): Promise<void> {
  await page.route('**/api/games**', async (route) => {
    const request = route.request();
    const requestURL = new URL(request.url());
    if (requestURL.origin !== USER_ORIGIN) {
      await route.fallback();
      return;
    }
    if (requestURL.pathname === '/api/games' && request.method() === 'GET') {
      const snapshot = gamesSnapshotWire();
      snapshot.balance = '5000000';
      snapshot.fishing.bait_prices = { worm: '2.5', lure: '5', premium: '7.5' };
      await route.fulfill(jsonResponse(snapshot));
      return;
    }
    if (requestURL.pathname === '/api/games/fishing/state' && request.method() === 'GET') {
      await route.fulfill(jsonResponse(fixture.state));
      return;
    }
    if (requestURL.pathname === '/api/games/fishing/leaderboard' && request.method() === 'GET') {
      const board = requestURL.searchParams.get('board');
      if (board !== 'single' && board !== 'recent_single' && board !== 'total') {
        await route.fallback();
        return;
      }
      fixture.boardRequests[board] += 1;
      await route.fulfill(jsonResponse(boardBody(board)));
      return;
    }
    if (
      requestURL.pathname === `/api/games/fishing/batches/${BATCH_ID}/ack` &&
      request.method() === 'POST'
    ) {
      fixture.ackRequests += 1;
      fixture.state = {
        settlement_pending: null,
        unrevealed: null,
        has_more_unrevealed: false,
      };
      await route.fulfill({ status: 204, headers: { 'cache-control': 'no-store' } });
      return;
    }
    await route.fallback();
  });
}

async function installUserShell(
  page: Page,
  language: 'en' | 'zh',
  theme: 'light' | 'dark',
): Promise<void> {
  await page.addInitScript(
    ({ selectedLanguage, selectedTheme }) => {
      localStorage.setItem('nb.lang', selectedLanguage);
      localStorage.setItem('nb.theme', selectedTheme);
    },
    { selectedLanguage: language, selectedTheme: theme },
  );
  await mockPublicConfig(page, 'user');
  await mockRoleSession(page, 'user', 'level4');
}

async function assertNoHorizontalOverflow(page: Page, width: number): Promise<void> {
  await page.setViewportSize({ width, height: 900 });
  const layout = await page.evaluate(() => ({
    viewport: window.innerWidth,
    documentWidth: document.documentElement.scrollWidth,
    bodyWidth: document.body.scrollWidth,
  }));
  expect(
    layout.documentWidth,
    `document overflow at ${width}px: ${JSON.stringify(layout)}`,
  ).toBeLessThanOrEqual(layout.viewport + 1);
  expect(
    layout.bodyWidth,
    `body overflow at ${width}px: ${JSON.stringify(layout)}`,
  ).toBeLessThanOrEqual(layout.viewport + 1);
}

async function assertImageLoaded(image: ReturnType<Page['locator']>): Promise<void> {
  await expect
    .poll(async () => image.evaluate((node) => (node as HTMLImageElement).complete))
    .toBe(true);
  const dimensions = await image.evaluate((node) => {
    const imageElement = node as HTMLImageElement;
    return {
      currentSrc: imageElement.currentSrc,
      naturalWidth: imageElement.naturalWidth,
      naturalHeight: imageElement.naturalHeight,
    };
  });
  expect(dimensions.currentSrc).toContain(BLUE_IMAGE_PATH);
  expect(dimensions.naturalWidth).toBeGreaterThan(0);
  expect(dimensions.naturalHeight).toBeGreaterThan(0);
  const cornerAlpha = await image.evaluate((node) => {
    const canvas = document.createElement('canvas');
    canvas.width = canvas.height = 1;
    const context = canvas.getContext('2d')!;
    context.drawImage(node as HTMLImageElement, 0, 0);
    return context.getImageData(0, 0, 1, 1).data[3];
  });
  expect(cornerAlpha).toBe(0);
}

const SCENARIOS = [
  {
    language: 'en' as const,
    theme: 'light' as const,
    pageTitle: 'A quiet cast, a surprise catch',
    blueName: 'Blue fat fish',
    originalName: 'Original legendary species: Koi',
    historicalTab: 'Historical board',
    recentTab: 'Rolling 30-day window',
    historicalTitle: 'Largest single catch',
    recentTitle: 'Largest single catch · last 30 days',
    totalTitle: '30-day total catch',
    historicalCatch: 'Taimen · 121 cm',
    rulesButton: 'How to play',
    closeRules: 'Close rules',
    helpNotice: 'original species and rewards stay unchanged',
    refresh: 'Refresh board',
  },
  {
    language: 'zh' as const,
    theme: 'dark' as const,
    pageTitle: '悠闲抛竿，看看收获',
    blueName: '蓝色大肥鱼',
    originalName: '原传奇鱼种：锦鲤',
    historicalTab: '历史榜',
    recentTab: '近 30 天窗口',
    historicalTitle: '单次最大收获',
    recentTitle: '近 30 天单次最大收获',
    totalTitle: '近 30 天总收获',
    historicalCatch: '哲罗鲑 · 121 厘米',
    rulesButton: '玩法说明',
    closeRules: '关闭玩法说明',
    helpNotice: '结果及榜单保留原鱼种，奖励不变',
    refresh: '刷新榜单',
  },
  {
    language: 'en' as const,
    theme: 'dark' as const,
    pageTitle: 'A quiet cast, a surprise catch',
    blueName: 'Blue fat fish',
    originalName: 'Original legendary species: Koi',
    historicalTab: 'Historical board',
    recentTab: 'Rolling 30-day window',
    historicalTitle: 'Largest single catch',
    recentTitle: 'Largest single catch · last 30 days',
    totalTitle: '30-day total catch',
    historicalCatch: 'Taimen · 121 cm',
    rulesButton: 'How to play',
    closeRules: 'Close rules',
    helpNotice: 'original species and rewards stay unchanged',
    refresh: 'Refresh board',
  },
  {
    language: 'zh' as const,
    theme: 'light' as const,
    pageTitle: '悠闲抛竿，看看收获',
    blueName: '蓝色大肥鱼',
    originalName: '原传奇鱼种：锦鲤',
    historicalTab: '历史榜',
    recentTab: '近 30 天窗口',
    historicalTitle: '单次最大收获',
    recentTitle: '近 30 天单次最大收获',
    totalTitle: '近 30 天总收获',
    historicalCatch: '哲罗鲑 · 121 厘米',
    rulesButton: '玩法说明',
    closeRules: '关闭玩法说明',
    helpNotice: '结果及榜单保留原鱼种，奖励不变',
    refresh: '刷新榜单',
  },
] as const;

for (const scenario of SCENARIOS) {
  test(`fishing records regression (${scenario.language}, ${scenario.theme})`, async ({ page }) => {
    const consoleGuard = collectConsoleViolations(page);
    const fixture = fixtureWithResult();
    await installUserShell(page, scenario.language, scenario.theme);
    await installFishingRoutes(page, fixture);
    await page.emulateMedia({ reducedMotion: 'reduce' });
    await page.goto(`${USER_ORIGIN}/games/fishing`);

    await expect(page.locator('html')).toHaveAttribute('data-theme', scenario.theme);
    await expect(page.getByRole('heading', { name: scenario.pageTitle })).toBeVisible();
    await page.getByRole('button', { name: scenario.rulesButton }).click();
    await expect(page.getByRole('dialog')).toContainText(scenario.helpNotice);
    await expect(page.getByRole('dialog')).toContainText('10%');
    await expect(page.getByRole('dialog')).toContainText('201');
    await page.getByRole('button', { name: scenario.closeRules }).click();
    await expect(page.locator('.fishing-result')).toBeVisible();

    const result = page.locator('.fishing-result');
    await expect(result.locator('.fishing-art[data-art-key="taimen"]')).toBeVisible();
    await expect(result.locator('[data-blue-fat-fish="true"]')).toContainText(scenario.blueName);
    await expect(result).toContainText(scenario.originalName);
    await expect(result).toContainText(LONG_BLUE_LENGTH);
    await assertImageLoaded(result.locator('.fishing-outcome[data-blue-fat-fish="true"] > img'));
    await assertImageLoaded(page.locator('.fishing-catch-celebration .fishing-blue-fat-fish'));
    expect(LONG_BLUE_LENGTH).toHaveLength(128);
    expect(BigInt(LONG_BLUE_LENGTH)).toBeGreaterThan(BigInt(Number.MAX_SAFE_INTEGER));
    expect(BigInt(UNSAFE_BLUE_LENGTH)).toBeGreaterThan(BigInt(Number.MAX_SAFE_INTEGER));

    await expect.poll(() => fixture.boardRequests.single).toBeGreaterThan(0);
    await expect.poll(() => fixture.boardRequests.recent_single).toBeGreaterThan(0);
    await expect.poll(() => fixture.boardRequests.total).toBeGreaterThan(0);
    await expect.poll(() => fixture.ackRequests).toBeGreaterThan(0);

    const screenshotDirectory = process.env.NONBIRI_TEST_SCREENSHOT_DIR;
    for (const width of [320, 360, 430, 390]) {
      await assertNoHorizontalOverflow(page, width);
    }
    if (screenshotDirectory) {
      await mkdir(screenshotDirectory, { recursive: true });
      await result.screenshot({
        path: join(screenshotDirectory, `${scenario.language}-${scenario.theme}-catch-390px.png`),
      });
      await page.locator('.fishing-stage').screenshot({
        path: join(screenshotDirectory, `${scenario.language}-${scenario.theme}-stage-390px.png`),
      });
    }

    await page.reload();
    await expect(page.getByRole('heading', { name: scenario.pageTitle })).toBeVisible();
    await expect(page.locator('.fishing-result')).toHaveCount(0);
    await expect(page.locator(`[data-batch-id="${BATCH_ID}"]`)).toHaveCount(0);

    const singleBoard = page.locator('.fishing-board-switch .fishing-board');
    const totalBoard = page.locator('.fishing-leaderboards > .fishing-board');
    const recentTab = page.getByRole('tab', { name: scenario.recentTab });
    const historicalTab = page.getByRole('tab', { name: scenario.historicalTab });
    const originalSpecies = scenario.language === 'zh' ? '锦鲤' : 'Koi';
    const unit = scenario.language === 'zh' ? '厘米' : 'cm';
    await expect(page.getByRole('tab').first()).toHaveText(scenario.recentTab);
    await expect(recentTab).toHaveAttribute('aria-selected', 'true');
    await expect(singleBoard.getByRole('heading', { name: scenario.recentTitle })).toBeVisible();
    await historicalTab.click();
    await expect(
      singleBoard.getByRole('heading', { name: scenario.historicalTitle }),
    ).toBeVisible();
    await expect(singleBoard).toContainText(scenario.historicalCatch);
    const historicalBeforeRefresh = fixture.boardRequests.single;
    await singleBoard.getByRole('button', { name: scenario.refresh }).click();
    await expect.poll(() => fixture.boardRequests.single).toBe(historicalBeforeRefresh + 1);
    await expect(historicalTab).toHaveAttribute('aria-selected', 'true');
    await expect(singleBoard).toContainText(
      `${scenario.blueName} · ${originalSpecies} · 262 ${unit}`,
    );
    await expect(singleBoard).not.toContainText(scenario.originalName);

    await page.getByRole('tab', { name: scenario.recentTab }).click();
    await expect(singleBoard.getByRole('heading', { name: scenario.recentTitle })).toBeVisible();
    await page.getByRole('tab', { name: scenario.recentTab }).press('ArrowLeft');
    await expect(page.getByRole('tab', { name: scenario.historicalTab })).toBeFocused();
    await expect(
      singleBoard.getByRole('heading', { name: scenario.historicalTitle, exact: true }),
    ).toBeVisible();
    await page.getByRole('tab', { name: scenario.historicalTab }).press('Home');
    await expect(page.getByRole('tab', { name: scenario.recentTab })).toBeFocused();
    await recentTab.press('End');
    await expect(historicalTab).toBeFocused();
    await historicalTab.press('ArrowRight');
    await expect(recentTab).toBeFocused();
    await expect(singleBoard.getByRole('heading', { name: scenario.recentTitle })).toBeVisible();
    await expect(singleBoard).toContainText(scenario.blueName);
    await expect(singleBoard).toContainText(
      `${scenario.blueName} · ${originalSpecies} · ${LONG_BLUE_LENGTH} ${unit}`,
    );
    await expect(singleBoard).not.toContainText(scenario.originalName);
    await expect(singleBoard).toContainText(LONG_BLUE_LENGTH);
    await expect(singleBoard).toContainText(UNSAFE_BLUE_LENGTH);
    await assertImageLoaded(singleBoard.locator('.fishing-blue-fat-fish--board').first());
    const recentBeforeRefresh = fixture.boardRequests.recent_single;
    await singleBoard.getByRole('button', { name: scenario.refresh }).click();
    await expect.poll(() => fixture.boardRequests.recent_single).toBe(recentBeforeRefresh + 1);
    await expect(recentTab).toHaveAttribute('aria-selected', 'true');

    await expect(totalBoard.getByRole('heading', { name: scenario.totalTitle })).toBeVisible();
    const totalBeforeRefresh = fixture.boardRequests.total;
    await totalBoard.getByRole('button', { name: scenario.refresh }).click();
    await expect.poll(() => fixture.boardRequests.total).toBe(totalBeforeRefresh + 1);

    await page.getByRole('tab', { name: scenario.historicalTab }).click();
    await expect(
      singleBoard.getByRole('heading', { name: scenario.historicalTitle }),
    ).toBeVisible();
    await page.getByRole('tab', { name: scenario.recentTab }).click();
    await expect(singleBoard.getByRole('heading', { name: scenario.recentTitle })).toBeVisible();

    await assertNoHorizontalOverflow(page, 390);
    if (screenshotDirectory) {
      await mkdir(screenshotDirectory, { recursive: true });
      await page.screenshot({
        path: join(screenshotDirectory, `${scenario.language}-${scenario.theme}-390px.png`),
        fullPage: true,
      });
    }

    for (const width of [320, 360, 430]) {
      await assertNoHorizontalOverflow(page, width);
    }
    await historicalTab.click();
    await page.goto(`${USER_ORIGIN}/games`);
    await page.goto(`${USER_ORIGIN}/games/fishing`);
    await expect(recentTab).toHaveAttribute('aria-selected', 'true');
    await expect(singleBoard.getByRole('heading', { name: scenario.recentTitle })).toBeVisible();
    consoleGuard.assertNone();
  });
}
