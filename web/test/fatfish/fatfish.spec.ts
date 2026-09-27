import { randomBytes } from 'node:crypto';
import { mkdirSync, readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import {
  expect,
  test,
  type Browser,
  type BrowserContext,
  type Locator,
  type Page,
} from '@playwright/test';
import { contentHash, parseLevel } from '../../src/shared/fatfish/engine/canonical';
import type { InputTuple } from '../../src/shared/fatfish/engine/types';

type Cookie = { Name: string; Value: string };
interface Fixture {
  admin_url: string;
  user_url: string;
  control_url: string;
  control_token: string;
  admin_cookie: Cookie;
  users: { id: string; level: number; cookie: Cookie }[];
}
interface Example {
  id: string;
  title: string;
  url: string;
  content_hash: string;
}
interface Proof {
  passed: boolean;
  stars: number;
  result: {
    content_hash: string;
    commitment_verified: boolean;
    ticket_charge: string;
    ticket_refund: string;
    rewards: string;
  };
}
const base = '/admin/api/limited-activities/fat-fish';
const fixture = () =>
  JSON.parse(readFileSync(process.env.NONBIRI_FISH_BROWSER_STATE!, 'utf8')) as Fixture;
const examples = (
  JSON.parse(readFileSync('public/examples/fatfish/manifest.json', 'utf8')) as {
    examples: Example[];
  }
).examples;

async function capture(page: Page, name: string) {
  const directory = process.env.NONBIRI_FISH_PREVIEW_DIR;
  if (!directory) return;
  mkdirSync(directory, { recursive: true });
  const previous = page.viewportSize();
  for (const [label, width, height] of [
    ['desktop', 1440, 1000],
    ['mobile-viewport', 390, 844],
  ] as const) {
    await page.setViewportSize({ width, height });
    const menu = page.getByRole('button', { name: 'Close navigation', exact: true });
    if (await menu.isVisible()) await menu.click();
    await page.evaluate(() => window.scrollTo(0, 0));
    await page.screenshot({
      path: resolve(directory, `${name}-${label}.png`),
      fullPage: true,
      animations: 'disabled',
    });
  }
  if (previous) await page.setViewportSize(previous);
}

async function verifyLocalMusic(page: Page, player: Locator) {
  const toggle = player.locator('[data-fatfish-music-toggle]');
  await expect(toggle).toHaveAttribute('aria-pressed', 'false');
  await expect(player.locator('[data-fatfish-music]')).toHaveCount(0);
  const loaded = page.waitForResponse(
    (response) =>
      new URL(response.url()).pathname === '/assets/fatfish/music/monkeys-spinning-monkeys.mp3',
  );
  await toggle.click();
  const response = await loaded;
  expect([200, 206]).toContain(response.status());
  expect(new URL(response.url()).origin).toBe(new URL(page.url()).origin);
  const audio = player.locator('[data-fatfish-music]');
  await expect(audio).toHaveCount(1);
  await expect
    .poll(() =>
      audio.evaluate(
        (element: HTMLAudioElement) =>
          !element.paused && element.currentTime > 0.1 && element.loop && element.error === null,
      ),
    )
    .toBe(true);
  expect(await audio.evaluate((element: HTMLAudioElement) => element.duration)).toBeGreaterThan(
    120,
  );
  await toggle.click();
  await expect(toggle).toHaveAttribute('aria-pressed', 'false');
  expect(await audio.evaluate((element: HTMLAudioElement) => element.paused)).toBe(true);
}

async function session(browser: Browser, role: 'admin' | 1 | 5 | 6, mobile = false) {
  const f = fixture();
  const origin = role === 'admin' ? f.admin_url : f.user_url;
  const cookie =
    role === 'admin' ? f.admin_cookie : f.users.find((user) => user.level === role)!.cookie;
  const context = await browser.newContext({
    viewport: mobile ? { width: 390, height: 844 } : { width: 1280, height: 900 },
    hasTouch: mobile,
  });
  await context.addCookies([
    {
      name: cookie.Name,
      value: cookie.Value,
      domain: new URL(origin).hostname,
      path: role === 'admin' ? '/admin' : '/api',
      httpOnly: true,
      sameSite: 'Lax',
    },
  ]);
  await context.addInitScript(() => {
    if (location.protocol === 'http:') {
      localStorage.setItem('nb.lang', 'en');
      localStorage.setItem('nb.theme', 'light');
    }
  });
  await context.route('**/*', (route) => {
    const url = new URL(route.request().url());
    return [f.admin_url, f.user_url].includes(url.origin)
      ? route.continue()
      : route.abort('blockedbyclient');
  });
  return context;
}
async function control(context: BrowserContext, path: string, data: unknown = {}) {
  const f = fixture();
  const response = await context.request.post(f.control_url + path, {
    data,
    headers: { Authorization: 'Bearer ' + f.control_token },
  });
  expect(response.ok()).toBe(true);
  return response.json() as Promise<Record<string, number>>;
}
async function adminAPI(context: BrowserContext, path: string, method = 'GET', data?: unknown) {
  const origin = fixture().admin_url;
  return context.request.fetch(origin + base + path, {
    method,
    data,
    headers: { Origin: origin, 'Idempotency-Key': randomBytes(24).toString('hex') },
  });
}
async function started(player: Locator) {
  await expect
    .poll(async () =>
      Number(await player.locator('[data-fish-tick]').getAttribute('data-fish-tick')),
    )
    .toBeGreaterThan(0);
}
async function boardPoint(player: Locator, x: number, y: number) {
  const board = player.locator('[data-fish-board]');
  await board.scrollIntoViewIfNeeded();
  const box = await board.boundingBox();
  if (!box) throw new Error('The playfield is not visible.');
  return { x: box.x + (x * box.width) / 480, y: box.y + (y * box.height) / 560 };
}
async function returnPreplacedTool(page: Page, player: Locator, x: number, y: number) {
  await started(player);
  const point = await boardPoint(player, x, y);
  await page.mouse.click(point.x, point.y);
  await player.getByRole('button', { name: 'Return tool', exact: true }).click();
}
async function recordedInputs(page: Page, hash: string): Promise<InputTuple[]> {
  return page.evaluate(
    (expectedHash) =>
      new Promise<InputTuple[]>((resolve, reject) => {
        const request = indexedDB.open('nonbiri-fatfish-inputs-v1', 1);
        request.onerror = () => reject(request.error);
        request.onsuccess = () => {
          const db = request.result;
          const transaction = db.transaction('sessions', 'readonly');
          const records = transaction.objectStore('sessions').getAll();
          let inputs: InputTuple[] = [];
          records.onsuccess = () => {
            const match = (records.result as { content_hash: string; inputs: InputTuple[] }[]).find(
              (record) => record.content_hash === expectedHash,
            );
            inputs = match?.inputs ?? [];
          };
          transaction.oncomplete = () => {
            db.close();
            resolve(inputs);
          };
          transaction.onerror = () => {
            db.close();
            reject(transaction.error);
          };
        };
      }),
    hash,
  );
}
async function importedVersion(page: Page, example: Example) {
  page.on('dialog', (dialog) => void dialog.accept());
  await page.goto(fixture().admin_url + '/limited-activities/fat-fish');
  await expect(page.getByRole('heading', { name: 'Level directory' })).toBeVisible();
  await page.getByRole('button', { name: example.title, exact: true }).click();
  await expect(page.getByLabel('Title', { exact: true })).toHaveValue(example.title);
  if (example.id === examples[0].id) await capture(page, '01-level-editor');
  const downloadPromise = page.waitForEvent('download');
  await page.getByRole('button', { name: 'Export draft', exact: true }).click();
  const download = await downloadPromise;
  const saved = await download.path();
  expect(saved).not.toBeNull();
  const exported = JSON.parse(readFileSync(saved!, 'utf8')) as { draft: unknown };
  expect(contentHash(parseLevel(JSON.stringify(exported.draft)))).toBe(example.content_hash);
  await page.getByLabel('Import JSON / convert v0.6').setInputFiles({
    name: example.id + '.json',
    mimeType: 'application/json',
    buffer: Buffer.from(JSON.stringify(exported)),
  });
  const savedResponse = page.waitForResponse(
    (response) =>
      response.url().endsWith(base + '/levels') && response.request().method() === 'POST',
  );
  await page.getByRole('button', { name: 'Save draft', exact: true }).click();
  expect((await savedResponse).status()).toBe(200);
  const publishedResponse = page.waitForResponse(
    (response) =>
      response.url().includes(base + '/levels/') &&
      response.url().endsWith('/versions') &&
      response.request().method() === 'POST',
  );
  await page
    .getByRole('button', { name: 'Publish immutable version from saved draft', exact: true })
    .click();
  const published = await publishedResponse;
  expect(published.status()).toBe(200);
  const version = (await published.json()) as { id: string; content_hash: string };
  expect(version.content_hash).toBe(example.content_hash);
  await page.getByRole('button', { name: new RegExp('^' + version.id + ' ·') }).click();
  await expect(page.getByRole('heading', { name: 'No-charge playtest' })).toBeVisible();
  if (example.id === examples[0].id) await capture(page, '02-version-and-playtest');
  return version;
}

async function playExample(page: Page, example: Example) {
  const version = await importedVersion(page, example);
  await page.getByRole('button', { name: 'Prepare playtest', exact: true }).click();
  await page.getByRole('button', { name: 'Start playtest', exact: true }).click();
  const player = page.getByRole('region', { name: 'Fat fish play', exact: true });
  const level = parseLevel(readFileSync('public' + example.url, 'utf8'));
  if (level.tools.some((tool) => tool.id === 100)) {
    const tool = level.tools.find((item) => item.id === 100)!;
    await returnPreplacedTool(page, player, tool.x / 64, tool.y / 64);
  }
  if (example.id === examples[0].id) {
    await verifyLocalMusic(page, player);
    await capture(page, '03-administrator-play');
  }
  await expect
    .poll(
      async () => Number(await player.locator('[data-fish-fed]').getAttribute('data-fish-fed')),
      { timeout: 45_000, message: example.id + ' reaches one-star rescue threshold' },
    )
    .toBeGreaterThanOrEqual(level.thresholds[0]);
  const finish = player.getByRole('button', { name: 'Finish', exact: true });
  if (await finish.isEnabled()) await finish.click();
  await player.getByRole('button', { name: 'Submit for verification', exact: true }).click();
  await expect(
    page.getByText('Server-verified one-star proof is available.', { exact: true }),
  ).toBeVisible();
  if (example.id === examples[0].id) await capture(page, '04-verified-playtest');
  const response = await adminAPI(page.context(), '/playtests?version_id=' + version.id);
  expect(response.status()).toBe(200);
  const proofs = (await response.json()) as Proof[];
  expect(proofs).toHaveLength(1);
  expect(proofs[0].passed).toBe(true);
  expect(proofs[0].stars).toBeGreaterThanOrEqual(1);
  expect(proofs[0].result.content_hash).toBe(example.content_hash);
  expect(proofs[0].result.commitment_verified).toBe(true);
  expect(proofs[0].result.ticket_charge).toBe('0');
  expect(proofs[0].result.ticket_refund).toBe('0');
  expect(proofs[0].result.rewards).toBe('0');
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
}

test('eight examples import, roundtrip and pass actual administrator play with server verification', async ({
  browser,
}) => {
  test.setTimeout(360_000);
  const context = await session(browser, 'admin');
  try {
    const before = await control(context, '/snapshot');
    for (const [index, example] of examples.entries()) {
      const page = await context.newPage();
      await page.setViewportSize(
        index % 2 ? { width: 390, height: 844 } : { width: 1280, height: 900 },
      );
      const errors: string[] = [];
      page.on('pageerror', (error) => errors.push(error.message));
      await playExample(page, example);
      expect(errors).toEqual([]);
      await page.close();
    }
    const after = await control(context, '/snapshot');
    expect(after.verified_passes - before.verified_passes).toBe(8);
    expect(after.versions - before.versions).toBe(8);
    expect(after.ledger_seq).toBe(before.ledger_seq);
    expect(after.periods).toBe(0);
    expect(after.progress).toBe(0);
    expect(after.default_closed).toBe(1);
  } finally {
    await context.close();
  }
});

test('ordinary users and both steward levels cannot read or mutate the editor', async ({
  browser,
}) => {
  for (const role of [1, 5, 6] as const) {
    const context = await session(browser, role);
    try {
      for (const [path, method, data] of [
        ['/levels?page=1', 'GET', undefined],
        ['/levels', 'POST', {}],
        ['/playtests', 'POST', {}],
      ] as const) {
        const response = await adminAPI(context, path, method, data);
        expect([401, 403]).toContain(response.status());
      }
      const logs = await context.request.get(
        fixture().user_url + '/api/steward/logs?usage_total_mismatch=true',
      );
      expect(logs.status()).toBe(role === 6 ? 200 : 403);
    } finally {
      await context.close();
    }
  }
});

for (const touch of [false, true]) {
  test(`${touch ? 'touch' : 'mouse'} moves preplaced and outside tools only after start, with durable server-verified inputs`, async ({
    browser,
  }) => {
    const context = await session(browser, 'admin', touch);
    try {
      const title = touch ? 'Touch workshop' : 'Mouse workshop';
      const draft = parseLevel(readFileSync('public' + examples[0].url, 'utf8'));
      draft.fish = [{ id: 1, x: 70 * 64, y: 510 * 64, heading: 0 }];
      draft.speed_pixels_per_second = 16;
      draft.duration_seconds = 20;
      draft.thresholds = [1, 1, 1];
      draft.bowls[0].capacity = 1;
      const saved = await adminAPI(context, '/levels', 'POST', { title, description: '', draft });
      expect(saved.status(), await saved.text()).toBe(200);
      const level = (await saved.json()) as { id: string; revision: string };
      const published = await adminAPI(context, '/levels/' + level.id + '/versions', 'POST', {
        expected_revision: level.revision,
      });
      expect(published.status()).toBe(200);
      const version = (await published.json()) as { id: string; content_hash: string };
      const page = await context.newPage();
      page.on('dialog', (dialog) => void dialog.accept());
      const errors: string[] = [];
      page.on('pageerror', (error) => errors.push(error.message));
      await page.goto(fixture().admin_url + '/limited-activities/fat-fish');
      await page
        .getByRole('button', { name: title + ' · r' + level.revision, exact: true })
        .click();
      await page.getByRole('button', { name: new RegExp('^' + version.id + ' ·') }).click();
      await page.getByRole('button', { name: 'Prepare playtest', exact: true }).click();
      const player = page.getByRole('region', { name: 'Fat fish play', exact: true });
      expect(await recordedInputs(page, version.content_hash)).toEqual([]);
      for (const tool of await player.locator('[data-tool-id]').all())
        await expect(tool).toBeDisabled();
      await page.getByRole('button', { name: 'Start playtest', exact: true }).click();
      await started(player);
      const board = player.locator('[data-fish-board]');
      await board.evaluate((element) => element.scrollIntoView({ block: 'center' }));
      const cdp = touch ? await context.newCDPSession(page) : null;
      const drag = async (from: { x: number; y: number }, to: { x: number; y: number }) => {
        expect(
          await page.evaluate(({ x, y }) => {
            const target = document.elementFromPoint(x, y);
            return !!target?.closest('[data-fish-board], [data-tool-id]');
          }, from),
        ).toBe(true);
        if (cdp) {
          await cdp.send('Input.dispatchTouchEvent', {
            type: 'touchStart',
            touchPoints: [{ ...from, id: 1 }],
          });
          for (let step = 1; step <= 6; step++)
            await cdp.send('Input.dispatchTouchEvent', {
              type: 'touchMove',
              touchPoints: [
                {
                  x: from.x + ((to.x - from.x) * step) / 6,
                  y: from.y + ((to.y - from.y) * step) / 6,
                  id: 1,
                },
              ],
            });
          await cdp.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] });
        } else {
          await page.mouse.move(from.x, from.y);
          await page.mouse.down();
          await page.mouse.move(to.x, to.y, { steps: 6 });
          await page.mouse.up();
        }
        await expect(player).toHaveAttribute('data-dragging', 'false');
      };
      const lastToolInput = async (id: number) =>
        (await recordedInputs(page, version.content_hash))
          .filter((input) => input[3] === id)
          .at(-1)
          ?.slice(2);
      await expect(player.locator('[data-tool-id="100"]')).toHaveCount(0);
      await drag(await boardPoint(player, 174, 430), await boardPoint(player, 214, 440));
      await expect.poll(() => lastToolInput(100)).toEqual(['place', 100, 210 * 64, 430 * 64]);
      const spare = player.locator('[data-tool-id="101"]');
      const spareBox = await spare.boundingBox();
      if (!spareBox) throw new Error('The outside tool is not visible.');
      await drag(
        { x: spareBox.x + spareBox.width / 2, y: spareBox.y + spareBox.height / 2 },
        await boardPoint(player, 350, 80),
      );
      await expect(spare).toHaveCount(0);
      await expect.poll(() => lastToolInput(101)).toEqual(['place', 101, 350 * 64, 80 * 64]);
      await drag(await boardPoint(player, 350, 80), await boardPoint(player, 350, 150));
      await expect.poll(() => lastToolInput(101)).toEqual(['place', 101, 350 * 64, 150 * 64]);
      const tray = await player.locator('[data-staging-area]').boundingBox();
      if (!tray) throw new Error('The outside workbench is not visible.');
      await drag(await boardPoint(player, 350, 150), {
        x: tray.x + tray.width / 2,
        y: tray.y + tray.height / 2,
      });
      await expect(spare).toBeVisible();
      await expect.poll(() => lastToolInput(101)).toEqual(['return', 101]);
      const inputs = await recordedInputs(page, version.content_hash);
      expect(inputs.length).toBeGreaterThanOrEqual(4);
      const perTick = new Map<number, number>();
      for (const input of inputs) perTick.set(input[0], (perTick.get(input[0]) ?? 0) + 1);
      expect(Math.max(...perTick.values())).toBeLessThanOrEqual(2);
      await expect(
        player.getByRole('button', { name: 'Submit for verification', exact: true }),
      ).toBeVisible({ timeout: 30_000 });
      await player.getByRole('button', { name: 'Submit for verification', exact: true }).click();
      await expect(
        page.getByText('This playtest did not produce a one-star pass.', { exact: true }),
      ).toBeVisible();
      const proofs = (await (
        await adminAPI(context, '/playtests?version_id=' + version.id)
      ).json()) as Proof[];
      expect(proofs).toHaveLength(1);
      expect(proofs[0].result.commitment_verified).toBe(true);
      expect(proofs[0].result.ticket_charge).toBe('0');
      expect(proofs[0].result.rewards).toBe('0');
      expect(errors).toEqual([]);
      await cdp?.detach();
    } finally {
      await context.close();
    }
  });
}

test('a local season publishes explicitly and the original user tab resumes, settles and shows its results', async ({
  browser,
}) => {
  const administrator = await session(browser, 'admin');
  const participant = await session(browser, 1);
  try {
    let levels = (await (await adminAPI(administrator, '/levels?page=1')).json()) as {
      items: { id: string; title: string }[];
    };
    if (levels.items.length === 0) {
      const preparation = await administrator.newPage();
      await playExample(preparation, examples[0]);
      await preparation.close();
      levels = await (await adminAPI(administrator, '/levels?page=1')).json();
    }
    const level = levels.items.find((item) => item.title === examples[0].title)!;
    expect(level).toBeDefined();
    const versions = (await (
      await adminAPI(administrator, '/levels/' + level.id + '/versions?page=1')
    ).json()) as { items: { id: string }[] };
    const now = Math.floor(Date.now() / 1000);
    const created = await adminAPI(administrator, '/periods', 'POST', {
      title: 'Rice garden season',
      description:
        'Guide the fish to a meal, unlock connected ponds, and improve your personal best.',
      visible: true,
      paused: false,
      past_public: true,
      starts_at: now - 60,
      ends_at: now + 3600,
    });
    expect(created.status()).toBe(200);
    let period = (await created.json()) as { id: string; revision: string };
    const nodes: { id: string; revision: string }[] = [];
    for (let index = 0; index < 3; index++) {
      const saved = await adminAPI(administrator, '/periods/' + period.id + '/nodes', 'POST', {
        title: ['Rice introduction', 'The connected pond', 'A second helping'][index],
        description: 'Rescue at least five fish.',
        version_id: versions.items[0].id,
        map_x: [160, 390, 600][index],
        map_y: [100, 240, 100][index],
        order: index,
        condition: index ? { passed: nodes[index - 1].id } : {},
        hidden_until_eligible: false,
        amounts: {
          unlock_cost: '1',
          ticket_price: '1',
          first_clear_reward: '2',
          star_rewards: ['1', '1', '1'],
        },
        expected_period_revision: period.revision,
      });
      expect(saved.status()).toBe(200);
      nodes.push(await saved.json());
      period = await (await adminAPI(administrator, '/periods/' + period.id)).json();
    }
    const adminPage = await administrator.newPage();
    adminPage.on('dialog', (dialog) => void dialog.accept());
    await adminPage.goto(fixture().admin_url + '/limited-activities/fat-fish');
    await adminPage.getByRole('button', { name: 'Periods and nodes', exact: true }).click();
    await adminPage
      .getByRole('button', { name: 'Rice garden season · draft', exact: true })
      .click();
    await adminPage.getByRole('button', { name: /^2\. The connected pond/ }).click();
    await expect(adminPage.getByRole('heading', { name: 'Edit node', exact: true })).toBeVisible();
    await capture(adminPage, '05-period-layout-and-conditions');
    await adminPage
      .getByRole('button', { name: 'Check and preview publish conditions', exact: true })
      .click();
    await capture(adminPage, '06-publication-check');
    const publishing = adminPage.waitForResponse((response) =>
      response.url().endsWith('/periods/' + period.id + '/publish'),
    );
    await adminPage.getByRole('button', { name: 'Publish period', exact: true }).click();
    expect((await publishing).status()).toBe(200);
    await expect(
      adminPage.getByRole('button', { name: 'Close period', exact: true }),
    ).toBeVisible();
    await expect(
      adminPage.getByRole('button', { name: 'Rice garden season · open', exact: true }),
    ).toBeVisible();
    await capture(adminPage, '07-published-period');
    const activity = (await (await adminAPI(administrator, '')).json()) as { revision: string };
    const configured = await adminAPI(administrator, '', 'PUT', {
      expected_revision: activity.revision,
      visible: true,
      paused: false,
      starts_at: now - 60,
      ends_at: now + 3600,
      module_config: {},
    });
    expect(configured.status()).toBe(200);
    const page = await participant.newPage();
    page.on('dialog', (dialog) => void dialog.accept());
    await page.goto(fixture().user_url + '/activities');
    await expect(page.locator('a[href="/activities/fat-fish"]')).toBeVisible();
    const cover = page.getByRole('img', {
      name: 'Fat Fish move obstacles and happily head toward bowls of rice',
      exact: true,
    });
    await cover.scrollIntoViewIfNeeded();
    await expect
      .poll(() => cover.evaluate((element: HTMLImageElement) => element.naturalWidth))
      .toBe(1536);
    await capture(page, '08-user-activity-directory');
    const path = '/activities/fat-fish?period=' + period.id + '&node=' + nodes[0].id;
    await test.step('the published period and node can be read by the participant', async () => {
      for (const suffix of [
        '/periods/' + period.id,
        '/periods/' + period.id + '/nodes/' + nodes[0].id,
        '/challenges/current',
      ]) {
        const response = await participant.request.get(
          fixture().user_url + '/api/limited-activities/fat-fish' + suffix,
          { timeout: 10_000 },
        );
        expect(response.status(), suffix).toBe(200);
      }
    });
    await test.step('the participant opens the published node', async () => {
      const errors: string[] = [];
      page.on('pageerror', (error) => errors.push(error.message));
      await page.goto(fixture().user_url + path, {
        waitUntil: 'domcontentloaded',
        timeout: 30_000,
      });
      await expect(page.getByRole('button', { name: 'Unlock node', exact: true })).toBeVisible();
      expect(errors).toEqual([]);
    });
    await capture(page, '09-user-map-and-unlock');
    await page.getByRole('button', { name: 'Unlock node', exact: true }).click();
    await page.getByRole('button', { name: 'Prepare challenge (free)', exact: true }).click();
    await expect(
      page.getByRole('button', { name: 'Confirm ticket and start', exact: true }),
    ).toBeVisible();
    await capture(page, '10-prepared-challenge');
    await page.getByRole('button', { name: 'Confirm ticket and start', exact: true }).click();
    let player = page.getByRole('region', { name: 'Fat fish play', exact: true });
    await returnPreplacedTool(page, player, 170, 420);
    await verifyLocalMusic(page, player);
    await expect
      .poll(async () =>
        Number(await player.locator('[data-fish-tick]').getAttribute('data-fish-tick')),
      )
      .toBeGreaterThan(1);
    await capture(page, '11-user-live-challenge');
    await test.step('a copied live tab remains read-only after refresh', async () => {
      const popup = page.waitForEvent('popup');
      await page.evaluate(() => {
        window.open(location.href, '_blank');
      });
      const copy = await popup;
      await expect(
        copy.getByText(
          'This tab cannot continue playing. Wait for expiry or abandon the challenge.',
          { exact: true },
        ),
      ).toBeVisible();
      await copy.reload();
      await expect(
        copy.getByText(
          'This tab cannot continue playing. Wait for expiry or abandon the challenge.',
          { exact: true },
        ),
      ).toBeVisible();
      await expect(copy.locator('[data-tool-id]')).toHaveCount(0);
      await expect(copy.locator('[data-fish-board]')).toHaveCount(0);
      await copy.close();
    });
    await page.bringToFront();
    await test.step('play continues offline and the active challenge survives an application restart', async () => {
      await participant.setOffline(true);
      const before = Number(
        await player.locator('[data-fish-tick]').getAttribute('data-fish-tick'),
      );
      await expect
        .poll(async () =>
          Number(await player.locator('[data-fish-tick]').getAttribute('data-fish-tick')),
        )
        .toBeGreaterThan(before + 30);
      await control(administrator, '/restart');
      await participant.setOffline(false);
    });
    await page.reload();
    player = page.getByRole('region', { name: 'Fat fish play', exact: true });
    await expect(player).toBeVisible();
    await expect
      .poll(
        async () => Number(await player.locator('[data-fish-fed]').getAttribute('data-fish-fed')),
        {
          timeout: 95_000,
        },
      )
      .toBeGreaterThanOrEqual(5);
    await capture(page, '12-restored-challenge');
    if (await player.getByRole('button', { name: 'Finish', exact: true }).isEnabled())
      await player.getByRole('button', { name: 'Finish', exact: true }).click();
    await player.getByRole('button', { name: 'Submit for verification', exact: true }).click();
    await expect(
      page.locator('.fatfish-history').getByText('Passed', { exact: true }),
    ).toBeVisible();
    await capture(page, '13-results-ranking-and-history');
    await expect(page.locator('.fatfish-board')).toContainText('#1');
    expect(await page.locator('.fatfish-history').innerText()).toContain(
      'Ticket: 1 · Refund: 0 · Rewards:',
    );
    const response = await participant.request.get(
      fixture().user_url +
        '/api/limited-activities/fat-fish/periods/' +
        period.id +
        '/nodes/' +
        nodes[1].id,
    );
    expect(response.status()).toBe(200);
    expect(((await response.json()) as { eligible: boolean }).eligible).toBe(true);
    await adminPage.getByRole('button', { name: 'Close period', exact: true }).click();
    await expect(
      adminPage.getByRole('button', { name: 'Reopen period', exact: true }),
    ).toBeVisible();
    await capture(adminPage, '14-closed-period');
  } finally {
    await participant.close().catch(() => undefined);
    await administrator.close().catch(() => undefined);
  }
});
