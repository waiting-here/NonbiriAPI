import { appendFileSync, mkdirSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { expect, test, type Page } from './test';
import { mockPublicConfig, mockRoleSession } from './support';
import { USER_ORIGIN } from './ports';
import { gamesSnapshotWire } from '../../src/user/games/common/testFixtures';

type LinkLinkSpec = '6x8' | '8x8' | '10x10';
type Coordinate = { readonly row: number; readonly col: number };
type MatchPath = readonly Coordinate[];

interface BoundaryPair {
  readonly first: Coordinate;
  readonly second: Coordinate;
  readonly path: MatchPath;
}

interface ViewportFixture {
  readonly name: string;
  readonly width: number;
  readonly height: number;
  readonly language: 'en' | 'zh';
  readonly theme: 'light' | 'dark';
  readonly density: 'comfortable' | 'compact';
  readonly fontSize: 'default' | 'large';
}

const VIEWPORTS: readonly ViewportFixture[] = [
  {
    name: 'phone-320-light-default',
    width: 320,
    height: 760,
    language: 'en',
    theme: 'light',
    density: 'comfortable',
    fontSize: 'default',
  },
  {
    name: 'phone-360-dark-compact',
    width: 360,
    height: 760,
    language: 'zh',
    theme: 'dark',
    density: 'compact',
    fontSize: 'default',
  },
  {
    name: 'phone-390-light-large',
    width: 390,
    height: 844,
    language: 'en',
    theme: 'light',
    density: 'comfortable',
    fontSize: 'large',
  },
  {
    name: 'phone-430-dark-large',
    width: 430,
    height: 900,
    language: 'zh',
    theme: 'dark',
    density: 'compact',
    fontSize: 'large',
  },
  {
    name: 'landscape-dark-default',
    width: 844,
    height: 390,
    language: 'en',
    theme: 'dark',
    density: 'comfortable',
    fontSize: 'default',
  },
  {
    name: 'medium-768-light-compact',
    width: 768,
    height: 900,
    language: 'zh',
    theme: 'light',
    density: 'compact',
    fontSize: 'large',
  },
  {
    name: 'desktop-1440-light-default',
    width: 1_440,
    height: 1_000,
    language: 'en',
    theme: 'light',
    density: 'comfortable',
    fontSize: 'default',
  },
] as const;

const DEADLINES: Record<LinkLinkSpec, number> = {
  '6x8': 150,
  '8x8': 180,
  '10x10': 240,
};

function dimensions(spec: LinkLinkSpec): readonly [number, number] {
  const [rows, cols] = spec.split('x').map(Number);
  return [rows, cols] as const;
}

function cellKey(coordinate: Coordinate): string {
  return `${coordinate.row}:${coordinate.col}`;
}

function boundaryPairs(rows: number, cols: number): readonly BoundaryPair[] {
  return [
    {
      first: { row: 0, col: 0 },
      second: { row: 0, col: 1 },
      path: [
        { row: 0, col: 0 },
        { row: -1, col: 0 },
        { row: -1, col: 1 },
        { row: 0, col: 1 },
      ],
    },
    {
      first: { row: 1, col: 0 },
      second: { row: 2, col: 0 },
      path: [
        { row: 1, col: 0 },
        { row: 1, col: -1 },
        { row: 2, col: -1 },
        { row: 2, col: 0 },
      ],
    },
    {
      first: { row: 0, col: cols - 1 },
      second: { row: 1, col: cols - 1 },
      path: [
        { row: 0, col: cols - 1 },
        { row: 0, col: cols },
        { row: 1, col: cols },
        { row: 1, col: cols - 1 },
      ],
    },
    {
      first: { row: rows - 1, col: 0 },
      second: { row: rows - 1, col: 1 },
      path: [
        { row: rows - 1, col: 0 },
        { row: rows, col: 0 },
        { row: rows, col: 1 },
        { row: rows - 1, col: 1 },
      ],
    },
    {
      first: { row: 2, col: cols - 2 },
      second: { row: 2, col: cols - 1 },
      path: [
        { row: 2, col: cols - 2 },
        { row: 2, col: cols - 1 },
      ],
    },
    {
      first: { row: 2, col: cols - 3 },
      second: { row: 3, col: cols - 2 },
      path: [
        { row: 2, col: cols - 3 },
        { row: 2, col: cols - 2 },
        { row: 3, col: cols - 2 },
      ],
    },
  ];
}

function tileKey(number: number): string {
  return `tile_${String(number).padStart(2, '0')}`;
}

function makeTiles(rows: number, cols: number, pairs: readonly BoundaryPair[]) {
  const cells = Array.from({ length: rows * cols }, (_, index) => ({
    row: Math.floor(index / cols),
    col: index % cols,
  }));
  const reserved = new Set(pairs.flatMap((pair) => [cellKey(pair.first), cellKey(pair.second)]));
  const assigned = new Map<string, string>();
  const fillers = cells.filter((cell) => !reserved.has(cellKey(cell)));
  let keyNumber = 1;
  const assign = (cell: Coordinate, value: string) => assigned.set(cellKey(cell), value);

  for (const pair of pairs) {
    const value = tileKey(keyNumber++);
    assign(pair.first, value);
    assign(pair.second, value);
    assign(fillers.shift()!, value);
    assign(fillers.shift()!, value);
  }
  const remainder = cells.filter((cell) => !assigned.has(cellKey(cell)));
  while (remainder.length > 0) {
    const value = tileKey(keyNumber++);
    for (let index = 0; index < 4; index += 1) assign(remainder.shift()!, value);
  }
  return cells.map((cell) => ({
    row: cell.row,
    col: cell.col,
    tile_key: assigned.get(cellKey(cell))!,
    removed: false,
  }));
}

function makeState(spec: LinkLinkSpec) {
  const [rows, cols] = dimensions(spec);
  const pairs = boundaryPairs(rows, cols);
  return {
    session_id: `ll_${'A'.repeat(22)}`,
    spec,
    state: 'active',
    price: '3',
    revision: '1',
    board: { rows, cols, tiles: makeTiles(rows, cols, pairs) },
    pairs_removed: 0,
    total_pairs: (rows * cols) / 2,
    started_at: 1_800_000_000,
    deadline: 1_800_000_000 + DEADLINES[spec],
    server_now: 1_800_000_010,
  };
}

function writeGeometryEvidence(name: string, value: unknown): void {
  const target = process.env.NONBIRI_LINK_LAYOUT_EVIDENCE;
  if (!target) return;
  mkdirSync(dirname(target), { recursive: true });
  appendFileSync(target, `${JSON.stringify({ name, value })}\n`);
}

async function signedIn(
  page: Page,
  initial: ViewportFixture,
  role: 'anonymous' | 'user' = 'user',
): Promise<void> {
  await page.addInitScript(({ language, theme, density, fontSize }) => {
    if (!localStorage.getItem('nb.lang')) localStorage.setItem('nb.lang', language);
    if (!localStorage.getItem('nb.theme')) localStorage.setItem('nb.theme', theme);
    if (!localStorage.getItem('nb.density')) localStorage.setItem('nb.density', density);
    if (!localStorage.getItem('nb.font-size')) localStorage.setItem('nb.font-size', fontSize);
  }, initial);
  await mockRoleSession(page, 'user', role);
  await mockPublicConfig(page, 'user');
}

async function applyLanguage(page: Page, language: ViewportFixture['language']): Promise<void> {
  const gameURL = page.url();
  await page.goto(`${USER_ORIGIN}/privacy`);
  const labels = language === 'zh' ? ['中文', 'EN'] : ['EN', '中文'];
  const selected = page.locator('.lang-switcher button').filter({ hasText: labels[0] });
  const other = page.locator('.lang-switcher button').filter({ hasText: labels[1] });
  await expect(selected).toBeVisible();
  await expect(other).toBeVisible();
  await selected.click();
  await expect(selected).toHaveAttribute('aria-pressed', 'true');
  await expect(selected).toHaveAttribute('aria-current', 'true');
  await expect(other).toHaveAttribute('aria-pressed', 'false');
  await expect(other).not.toHaveAttribute('aria-current');
  await expect(page.locator('html')).toHaveAttribute('lang', language === 'zh' ? 'zh-CN' : 'en');
  await page.goto(gameURL);
  await expect(page.locator('.linklink-tile:not(.is-removed)').first()).toBeVisible();
  await expect(page.locator('.linklink-feedback')).toContainText(
    language === 'zh'
      ? '点击两个相同图案，连线最多转弯两次。'
      : 'Connect identical pictures with no more than two turns.',
  );
  await expect(page.locator('.linklink-board')).toHaveAttribute(
    'aria-label',
    language === 'zh' ? '进行中的棋盘' : 'Active board',
  );
}

async function applyViewport(
  page: Page,
  fixture: ViewportFixture,
  options: { readonly switchLanguage?: boolean; readonly switchTheme?: boolean } = {},
): Promise<void> {
  await page.setViewportSize({ width: fixture.width, height: fixture.height });
  if (options.switchLanguage !== false) await applyLanguage(page, fixture.language);
  if (options.switchTheme !== false) {
    const accountTrigger = page.locator('.nb-account-trigger');
    if (await accountTrigger.isVisible()) {
      await accountTrigger.click();
      await page.locator('.nb-account-menu .theme-select').selectOption(fixture.theme);
    } else {
      const menuButton = page.locator('.nb-menu-button');
      await expect(menuButton).toBeVisible();
      await menuButton.click();
      await page.locator('.nb-user-drawer-actions .theme-select').selectOption(fixture.theme);
      await menuButton.click();
    }
    await expect(page.locator('html')).toHaveAttribute('data-theme', fixture.theme);
  }
  await page.evaluate(({ density, fontSize }) => {
    document.documentElement.dataset.density = density;
    document.documentElement.dataset.fontSize = fontSize;
  }, fixture);
  const expectedBackground = fixture.theme === 'dark' ? 'rgb(16, 25, 35)' : 'rgb(243, 247, 251)';
  await expect
    .poll(() => page.evaluate(() => getComputedStyle(document.body).backgroundColor))
    .toBe(expectedBackground);
  const styles = await page.evaluate(() => {
    const root = getComputedStyle(document.documentElement);
    const body = getComputedStyle(document.body);
    return {
      densityFactor: Number(root.getPropertyValue('--nb-density-factor')),
      fontScale: Number(root.getPropertyValue('--nb-font-scale')),
      bodyFontSize: body.fontSize,
      backgroundColor: body.backgroundColor,
    };
  });
  expect(styles.densityFactor).toBe(fixture.density === 'compact' ? 0.875 : 1);
  expect(styles.fontScale).toBe(fixture.fontSize === 'large' ? 1.125 : 1);
  expect(styles.bodyFontSize).toBe(fixture.fontSize === 'large' ? '18px' : '16px');
  expect(styles.backgroundColor).toBe(expectedBackground);
  await page.locator('.linklink-tile:not(.is-removed)').first().waitFor({ state: 'visible' });
  await page.evaluate(() => new Promise<void>((resolve) => requestAnimationFrame(() => resolve())));
}

interface BoardMetrics {
  readonly viewport: { readonly width: number; readonly height: number };
  readonly scrollClientWidth: number;
  readonly scrollWidth: number;
  readonly contentWidth: number;
  readonly boardWidth: number;
  readonly boardHeight: number;
  readonly tileWidth: number;
  readonly tileHeight: number;
  readonly gapX: number;
  readonly gapY: number;
  readonly paddingLeft: number;
  readonly paddingTop: number;
  readonly allTilesVisible: boolean;
  readonly documentScrollWidth: number;
  readonly corners: readonly { readonly row: number; readonly col: number; readonly rect: Rect }[];
}

interface Rect {
  readonly left: number;
  readonly top: number;
  readonly right: number;
  readonly bottom: number;
  readonly width: number;
  readonly height: number;
}

async function readBoardMetrics(page: Page, rows: number, cols: number): Promise<BoardMetrics> {
  return page.locator('.linklink-board').evaluate(
    (board, dimensionsValue) => {
      const scroll = board.parentElement!;
      const boardRect = board.getBoundingClientRect();
      const scrollRect = scroll.getBoundingClientRect();
      const scrollStyle = getComputedStyle(scroll);
      const borderX =
        parseFloat(scrollStyle.borderLeftWidth) + parseFloat(scrollStyle.borderRightWidth);
      const contentLeft = scrollRect.left + parseFloat(scrollStyle.borderLeftWidth);
      const contentTop = scrollRect.top + parseFloat(scrollStyle.borderTopWidth);
      const contentRight = scrollRect.right - parseFloat(scrollStyle.borderRightWidth);
      const contentBottom = scrollRect.bottom - parseFloat(scrollStyle.borderBottomWidth);
      const tiles = [...board.querySelectorAll<HTMLButtonElement>('.linklink-tile')];
      const rects = tiles.map((tile) => ({
        element: tile,
        rect: tile.getBoundingClientRect(),
        row: Number(tile.getAttribute('aria-rowindex')) - 1,
        col: Number(tile.getAttribute('aria-colindex')) - 1,
      }));
      const origin = rects.find((tile) => tile.row === 0 && tile.col === 0)?.rect;
      const right = rects.find((tile) => tile.row === 0 && tile.col === 1)?.rect;
      const below = rects.find((tile) => tile.row === 1 && tile.col === 0)?.rect;
      if (!origin || !right || !below)
        throw new Error('Board fixture is missing coordinate anchors.');
      const corners = rects
        .filter(
          (tile) =>
            (tile.row === 0 || tile.row === dimensionsValue.rows - 1) &&
            (tile.col === 0 || tile.col === dimensionsValue.cols - 1),
        )
        .map((tile) => ({
          row: tile.row,
          col: tile.col,
          rect: {
            left: tile.rect.left,
            top: tile.rect.top,
            right: tile.rect.right,
            bottom: tile.rect.bottom,
            width: tile.rect.width,
            height: tile.rect.height,
          },
        }));
      return {
        viewport: { width: window.innerWidth, height: window.innerHeight },
        scrollClientWidth: scroll.clientWidth,
        scrollWidth: scroll.scrollWidth,
        contentWidth: scrollRect.width - borderX,
        boardWidth: boardRect.width,
        boardHeight: boardRect.height,
        tileWidth: origin.width,
        tileHeight: origin.height,
        gapX: right.left - origin.right,
        gapY: below.top - origin.bottom,
        paddingLeft: origin.left - boardRect.left,
        paddingTop: origin.top - boardRect.top,
        allTilesVisible: rects.every(
          ({ rect }) =>
            rect.left >= contentLeft - 1 &&
            rect.right <= contentRight + 1 &&
            rect.top >= contentTop - 1 &&
            rect.bottom <= contentBottom + 1,
        ),
        documentScrollWidth: document.documentElement.scrollWidth,
        corners,
      };
    },
    { rows, cols },
  );
}

function assertBoardMetrics(metrics: BoardMetrics, cols: number): void {
  const expectedTile = Math.min(68, metrics.contentWidth / (cols + 1.5 + 0.08 * (cols + 1)));
  const expectedGap = expectedTile * 0.08;
  const expectedPadding = expectedTile * 0.75 + expectedGap;
  const expectedWidth = cols * expectedTile + (cols - 1) * expectedGap + 2 * expectedPadding;
  expect(Math.abs(metrics.tileWidth - expectedTile)).toBeLessThan(1.25);
  expect(Math.abs(metrics.tileHeight - expectedTile)).toBeLessThan(1.25);
  expect(Math.abs(metrics.gapX - expectedGap)).toBeLessThan(1.25);
  expect(Math.abs(metrics.gapY - expectedGap)).toBeLessThan(1.25);
  expect(Math.abs(metrics.paddingLeft - expectedPadding)).toBeLessThan(1.25);
  expect(Math.abs(metrics.paddingTop - expectedPadding)).toBeLessThan(1.25);
  expect(Math.abs(metrics.boardWidth - expectedWidth)).toBeLessThan(1.5);
  expect(metrics.boardWidth).toBeLessThanOrEqual(metrics.contentWidth + 1.5);
  expect(metrics.scrollWidth).toBeLessThanOrEqual(metrics.scrollClientWidth + 1);
  expect(metrics.documentScrollWidth).toBeLessThanOrEqual(metrics.viewport.width + 1);
  expect(metrics.allTilesVisible).toBe(true);
  expect(metrics.corners).toHaveLength(4);
}

interface EffectMetrics {
  readonly boardWidth: number;
  readonly boardHeight: number;
  readonly viewBoxWidth: number;
  readonly viewBoxHeight: number;
  readonly tileWidth: number;
  readonly tileHeight: number;
  readonly originX: number;
  readonly originY: number;
  readonly pitchX: number;
  readonly pitchY: number;
  readonly sparkPath: string;
  readonly sparkStart: number | null;
  readonly sparkLength: number | null;
  readonly beamFilter: string;
  readonly beamBlur: number | null;
  readonly points: readonly (readonly [number, number])[];
}

async function readEffect(page: Page): Promise<EffectMetrics> {
  return page.locator('.linklink-match-effect').evaluate((svg) => {
    const node = svg as SVGSVGElement;
    const board = svg.parentElement!;
    const origin = board
      .querySelector<HTMLButtonElement>('[aria-rowindex="1"][aria-colindex="1"]')!
      .getBoundingClientRect();
    const right = board
      .querySelector<HTMLButtonElement>('[aria-rowindex="1"][aria-colindex="2"]')!
      .getBoundingClientRect();
    const below = board
      .querySelector<HTMLButtonElement>('[aria-rowindex="2"][aria-colindex="1"]')!
      .getBoundingClientRect();
    const bounds = board.getBoundingClientRect();
    const points = svg
      .querySelector('polyline')!
      .getAttribute('points')!
      .trim()
      .split(/\s+/)
      .filter(Boolean)
      .map((point) => point.split(',').map(Number) as [number, number]);
    const sparkPath =
      svg.querySelector<SVGPathElement>('.linklink-match-sparks path')?.getAttribute('d') ?? '';
    const sparkValues = sparkPath.match(/-?\d+(?:\.\d+)?/g);
    const beam = svg.querySelector<SVGPolylineElement>('.linklink-match-beam')!;
    const beamFilter = getComputedStyle(beam).filter;
    const beamPixels = [...beamFilter.matchAll(/(-?\d+(?:\.\d+)?)px/g)].map((value) =>
      Number(value[1]),
    );
    return {
      boardWidth: bounds.width,
      boardHeight: bounds.height,
      viewBoxWidth: node.viewBox.baseVal.width,
      viewBoxHeight: node.viewBox.baseVal.height,
      tileWidth: origin.width,
      tileHeight: origin.height,
      originX: origin.left - bounds.left + origin.width / 2,
      originY: origin.top - bounds.top + origin.height / 2,
      pitchX: right.left - origin.left,
      pitchY: below.top - origin.top,
      sparkPath,
      sparkStart: sparkValues && sparkValues.length >= 3 ? Number(sparkValues[1]) : null,
      sparkLength: sparkValues && sparkValues.length >= 3 ? Number(sparkValues[2]) : null,
      beamFilter,
      beamBlur: beamPixels.at(-1) ?? null,
      points,
    };
  });
}

function assertEffectMetrics(effect: EffectMetrics, path: MatchPath): void {
  expect(effect.points).toHaveLength(path.length);
  expect(effect.viewBoxWidth).toBeCloseTo(effect.boardWidth, 4);
  expect(effect.viewBoxHeight).toBeCloseTo(effect.boardHeight, 4);
  expect(effect.sparkStart).not.toBeNull();
  expect(effect.sparkLength).not.toBeNull();
  expect(effect.sparkStart!).toBeCloseTo((-effect.tileWidth * 18) / 68, 1);
  expect(effect.sparkLength!).toBeCloseTo((-effect.tileWidth * 7) / 68, 1);
  expect(effect.beamFilter).toContain('drop-shadow');
  expect(effect.beamBlur).not.toBeNull();
  expect(effect.beamBlur!).toBeCloseTo(Math.max(1, (effect.tileWidth * 5) / 68), 1);
  for (const [index, coordinate] of path.entries()) {
    const point = effect.points[index]!;
    const expectedX = effect.originX + coordinate.col * effect.pitchX;
    const expectedY = effect.originY + coordinate.row * effect.pitchY;
    expect(Math.abs(point[0] - expectedX)).toBeLessThan(1.5);
    expect(Math.abs(point[1] - expectedY)).toBeLessThan(1.5);
    expect(point[0]).toBeGreaterThanOrEqual(-1);
    expect(point[1]).toBeGreaterThanOrEqual(-1);
    expect(point[0]).toBeLessThanOrEqual(effect.viewBoxWidth + 1);
    expect(point[1]).toBeLessThanOrEqual(effect.viewBoxHeight + 1);
  }
}

function tileLocator(page: Page, coordinate: Coordinate) {
  return page.locator(
    `.linklink-tile[aria-rowindex="${coordinate.row + 1}"][aria-colindex="${coordinate.col + 1}"]`,
  );
}

async function clickPair(page: Page, pair: BoundaryPair): Promise<void> {
  const first = tileLocator(page, pair.first);
  const second = tileLocator(page, pair.second);
  await expect(first).toBeEnabled();
  await first.click();
  await expect(first).toHaveAttribute('aria-selected', 'true');
  await expect(second).toBeEnabled();
  await second.click();
}

interface StandardFixtureOptions {
  readonly shuffleWireTiles?: boolean;
}

async function installStandardFixture(
  page: Page,
  spec: LinkLinkSpec,
  options: StandardFixtureOptions = {},
) {
  const [rows, cols] = dimensions(spec);
  const pairs = boundaryPairs(rows, cols);
  const current = makeState(spec);
  const matches: unknown[] = [];
  let matchIndex = 0;
  const wireState = () => ({
    ...current,
    board: {
      ...current.board,
      tiles: options.shuffleWireTiles
        ? [...current.board.tiles].reverse()
        : [...current.board.tiles],
    },
  });
  await page.route('**/api/games**', async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path === '/api/games') {
      await route.fulfill({ json: gamesSnapshotWire() });
      return;
    }
    if (path.endsWith('/lease')) {
      await route.fulfill({ json: { expires_at: 1_800_000_035 } });
      return;
    }
    if (path.endsWith('/matches')) {
      const input = route.request().postDataJSON() as {
        readonly first: Coordinate;
        readonly second: Coordinate;
      };
      matches.push(input);
      const pair = pairs[matchIndex];
      if (!pair) {
        await route.fulfill({ status: 500, json: { error: { code: 'unexpected_match' } } });
        return;
      }
      current.revision = String(Number(current.revision) + 1);
      current.pairs_removed += 1;
      const removed = new Set([cellKey(input.first), cellKey(input.second)]);
      current.board.tiles = current.board.tiles.map((tile) => ({
        ...tile,
        removed: removed.has(`${tile.row}:${tile.col}`) ? true : tile.removed,
      }));
      matchIndex += 1;
      await route.fulfill({ json: { ...wireState(), match_path: pair.path } });
      return;
    }
    if (path === '/api/games/linklink/session') {
      await route.fulfill({ json: wireState() });
      return;
    }
    await route.fallback();
  });
  return { pairs, matches, current };
}

async function installFailureFixture(page: Page, spec: LinkLinkSpec) {
  const current = makeState(spec);
  const candidates = current.board.tiles.filter((tile) => !tile.removed);
  const first = candidates[0]!;
  const second = candidates.find((tile) => tile.tile_key !== first.tile_key)!;
  const pair: BoundaryPair = {
    first: { row: first.row, col: first.col },
    second: { row: second.row, col: second.col },
    path: [],
  };
  const matches: unknown[] = [];
  let matchIndex = 0;
  let failures = 0;
  await page.route('**/api/games**', async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path === '/api/games') {
      await route.fulfill({ json: gamesSnapshotWire() });
      return;
    }
    if (path.endsWith('/lease')) {
      await route.fulfill({ json: { expires_at: 1_800_000_035 } });
      return;
    }
    if (path.endsWith('/matches')) {
      matches.push(route.request().postDataJSON());
      matchIndex += 1;
      if (matchIndex === 1) {
        failures += 1;
        await route.fulfill({
          status: 422,
          json: { error: { code: 'invalid_pair', message: 'Synthetic rejected pair.' } },
        });
        return;
      }
      current.revision = '2';
      current.board.tiles = current.board.tiles.map((tile) => {
        if (tile.row === pair.first.row && tile.col === pair.first.col)
          return { ...tile, tile_key: second.tile_key };
        if (tile.row === pair.second.row && tile.col === pair.second.col)
          return { ...tile, tile_key: first.tile_key };
        return tile;
      });
      await route.fulfill({ json: { ...current } });
      return;
    }
    if (path === '/api/games/linklink/session') {
      await route.fulfill({ json: current });
      return;
    }
    await route.fallback();
  });
  return { pair, matches, failures: () => failures };
}

async function screenshot(page: Page, spec: LinkLinkSpec, label: string): Promise<void> {
  const configured = process.env.NONBIRI_VISUAL_DIR;
  const path = configured
    ? join(configured, `linklink-${spec}-${label}.png`)
    : test.info().outputPath(`linklink-${spec}-${label}.png`);
  if (configured) mkdirSync(configured, { recursive: true });
  await page.screenshot({ path, fullPage: true });
}

function assertExpectedNetworkErrors(
  violations: readonly { readonly type: string; readonly text: string }[],
  statuses: readonly number[],
): void {
  for (const status of statuses) {
    expect(
      violations.filter(
        (violation) => violation.type === 'error' && violation.text.includes(`status of ${status}`),
      ),
    ).toHaveLength(1);
  }
  expect(
    violations.filter(
      (violation) => !statuses.some((status) => violation.text.includes(`status of ${status}`)),
    ),
  ).toEqual([]);
}

for (const spec of ['6x8', '8x8', '10x10'] as const) {
  test(`LinkLink ${spec} fits the frozen formula and keeps edge paths aligned`, async ({
    page,
  }) => {
    const errors: Array<{ readonly type: string; readonly text: string }> = [];
    page.on('console', (message) => {
      if (message.type() === 'error' || message.type() === 'warning')
        errors.push({ type: message.type(), text: message.text() });
    });
    page.on('pageerror', (error) => errors.push({ type: 'pageerror', text: error.message }));
    const [rows, cols] = dimensions(spec);
    const fixture = await installStandardFixture(page, spec);
    const initialTileKeys = fixture.current.board.tiles.map((tile) => tile.tile_key);
    await signedIn(page, VIEWPORTS[0]!);
    await page.goto(`${USER_ORIGIN}/games/linklink`);
    await expect(page.locator('.linklink-tile')).toHaveCount(rows * cols);

    for (const viewport of VIEWPORTS) {
      await applyViewport(page, viewport);
      const metrics = await readBoardMetrics(page, rows, cols);
      assertBoardMetrics(metrics, cols);
      writeGeometryEvidence(`${spec}:${viewport.name}`, metrics);
      if (viewport.width === 390) await screenshot(page, spec, viewport.name);
    }

    const desktop = VIEWPORTS.at(-1)!;
    await applyViewport(page, desktop);
    const bottomRight = tileLocator(page, { row: rows - 1, col: cols - 1 });
    await bottomRight.click();
    await expect(bottomRight).toHaveAttribute('aria-selected', 'true');
    await bottomRight.click();
    await expect(bottomRight).toHaveAttribute('aria-selected', 'false');
    await clickPair(page, fixture.pairs[0]!);
    await expect(page.locator('.linklink-match-effect')).toHaveCount(1);
    const effectBeforeResize = await readEffect(page);
    assertEffectMetrics(effectBeforeResize, fixture.pairs[0]!.path);
    writeGeometryEvidence(`${spec}:effect-before-resize`, effectBeforeResize);

    const resizeViewport = VIEWPORTS[2]!;
    await applyViewport(page, resizeViewport, { switchLanguage: false, switchTheme: false });
    await expect
      .poll(async () => (await readEffect(page)).tileWidth)
      .not.toBeCloseTo(effectBeforeResize.tileWidth, 1);
    await expect
      .poll(async () => {
        const effect = await readEffect(page);
        return effect.sparkStart === null
          ? Number.POSITIVE_INFINITY
          : Math.abs(effect.sparkStart - -(effect.tileWidth * 18) / 68);
      })
      .toBeLessThan(0.05);
    const effectAfterResize = await readEffect(page);
    assertEffectMetrics(effectAfterResize, fixture.pairs[0]!.path);
    expect(effectAfterResize.sparkPath).not.toBe(effectBeforeResize.sparkPath);
    writeGeometryEvidence(`${spec}:effect-after-resize`, effectAfterResize);
    expect(fixture.matches).toHaveLength(1);
    expect(fixture.current.revision).toBe('2');
    expect(fixture.current.pairs_removed).toBe(1);
    expect(fixture.current.deadline).toBe(1_800_000_000 + DEADLINES[spec]);
    expect(fixture.current.board.tiles.map((tile) => tile.tile_key)).toEqual(initialTileKeys);
    await expect(page.locator('.linklink-tile')).toHaveCount(rows * cols);
    await expect(page.locator('.linklink-board')).toHaveAttribute('aria-rowcount', String(rows));
    await expect(page.locator('.linklink-match-effect')).toHaveCount(0);

    for (const [index, pair] of fixture.pairs.slice(1).entries()) {
      await clickPair(page, pair);
      await expect(page.locator('.linklink-match-effect')).toHaveCount(1);
      const effect = await readEffect(page);
      assertEffectMetrics(effect, pair.path);
      writeGeometryEvidence(`${spec}:effect-edge-${index + 1}`, effect);
      await expect(page.locator('.linklink-match-effect')).toHaveCount(0);
    }
    expect(fixture.matches).toHaveLength(fixture.pairs.length);
    expect(fixture.current.revision).toBe(String(fixture.pairs.length + 1));
    expect(fixture.current.pairs_removed).toBe(fixture.pairs.length);
    expect(fixture.current.board.tiles.map((tile) => tile.tile_key)).toEqual(initialTileKeys);
    for (const [index, pair] of fixture.pairs.entries()) {
      expect(fixture.matches[index]).toMatchObject({
        include_path: true,
        first: pair.first,
        second: pair.second,
      });
    }
    assertExpectedNetworkErrors(errors, []);
  });
}

test('LinkLink places shuffled wire tiles by coordinates before posting their coordinates', async ({
  page,
}) => {
  const errors: Array<{ readonly type: string; readonly text: string }> = [];
  page.on('console', (message) => {
    if (message.type() === 'error' || message.type() === 'warning')
      errors.push({ type: message.type(), text: message.text() });
  });
  page.on('pageerror', (error) => errors.push({ type: 'pageerror', text: error.message }));
  const fixture = await installStandardFixture(page, '6x8', { shuffleWireTiles: true });
  await signedIn(page, VIEWPORTS[0]!);
  await page.goto(`${USER_ORIGIN}/games/linklink`);
  await expect(page.locator('.linklink-tile')).toHaveCount(48);
  await applyViewport(page, VIEWPORTS[2]!);

  const metrics = await readBoardMetrics(page, 6, 8);
  assertBoardMetrics(metrics, 8);
  const corners = new Map(metrics.corners.map((corner) => [cellKey(corner), corner.rect]));
  const topLeft = corners.get('0:0')!;
  const topRight = corners.get('0:7')!;
  const bottomLeft = corners.get('5:0')!;
  const bottomRight = corners.get('5:7')!;
  expect(topLeft.left).toBeLessThan(topRight.left);
  expect(bottomLeft.left).toBeLessThan(bottomRight.left);
  expect(topLeft.top).toBeLessThan(bottomLeft.top);
  expect(topRight.top).toBeLessThan(bottomRight.top);
  writeGeometryEvidence('6x8:shuffled-wire-corners', Object.fromEntries(corners));

  const pair = fixture.pairs[0]!;
  await clickPair(page, pair);
  await expect(page.locator('.linklink-match-effect')).toHaveCount(1);
  expect(fixture.matches).toHaveLength(1);
  expect(fixture.matches[0]).toMatchObject({
    first: pair.first,
    second: pair.second,
  });
  writeGeometryEvidence('6x8:shuffled-wire-match', fixture.matches[0]);
  await expect(page.locator('.linklink-match-effect')).toHaveCount(0);
  assertExpectedNetworkErrors(errors, []);
});

test('LinkLink keeps failure and free rearrange feedback without changing geometry state', async ({
  page,
}) => {
  const consoleViolations: Array<{ readonly type: string; readonly text: string }> = [];
  page.on('console', (message) => {
    if (message.type() === 'error' || message.type() === 'warning')
      consoleViolations.push({ type: message.type(), text: message.text() });
  });
  page.on('pageerror', (error) =>
    consoleViolations.push({ type: 'pageerror', text: error.message }),
  );
  const fixture = await installFailureFixture(page, '6x8');
  await signedIn(page, VIEWPORTS[2]!, 'user');
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto(`${USER_ORIGIN}/games/linklink`);
  await expect(page.locator('.linklink-tile')).toHaveCount(48);

  await clickPair(page, fixture.pair);
  await expect(page.locator('.linklink-feedback')).toContainText('could not be removed');
  await expect(page.locator('.linklink-match-effect')).toHaveCount(0);
  await expect.poll(() => consoleViolations.length).toBe(1);
  expect(fixture.failures()).toBe(1);
  expect(consoleViolations[0]?.type).toBe('error');
  expect(consoleViolations[0]?.text).toContain('status of 422');
  await clickPair(page, fixture.pair);
  await expect(page.locator('.linklink-feedback')).toContainText('No moves remained');
  await expect(page.locator('.linklink-match-effect')).toHaveCount(0);
  expect(fixture.matches).toHaveLength(2);
  expect(fixture.matches[0]).toMatchObject({
    first: fixture.pair.first,
    second: fixture.pair.second,
  });
  expect(fixture.matches[1]).toMatchObject({
    first: fixture.pair.first,
    second: fixture.pair.second,
  });
  expect(consoleViolations).toHaveLength(1);
});
