import { mkdirSync, readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { randomBytes } from 'node:crypto';
import {
  test,
  expect,
  type Browser,
  type BrowserContext,
  type Locator,
  type Page,
} from '@playwright/test';
import type { ImageTask } from '../../src/shared/picturebook/publicTypes';
import type { SizeCapability } from '../../src/shared/picturebook/capabilities';
import type { AdminModel } from '../../src/admin/features/picturebook/adminApi';

interface FixtureState {
  user_url: string;
  admin_url: string;
  control_url: string;
  control_token: string;
  users: {
    id: string;
    level: number;
    cookie: { Name: string; Value: string };
  }[];
  admin_cookie: { Name: string; Value: string };
  private_markers: string[];
  model_id: string;
}
const base = '/api/limited-activities/picture-book';
const adminBase = '/admin/api/limited-activities/picture-book';
const modelName = 'Browser canvas <svg onload=alert(1)> is plain text.';
const modelDescription = '<img src=x onerror=alert(1)> is displayed as text.';
const statePath = process.env.NONBIRI_IMAGE_BROWSER_STATE!;
const previewDir = process.env.NONBIRI_IMAGE_BROWSER_PREVIEW || join(dirname(statePath), 'preview');
function fixture(): FixtureState {
  return JSON.parse(readFileSync(statePath, 'utf8')) as FixtureState;
}
async function context(browser: Browser, index = 0, language = 'en', admin = false) {
  const state = fixture();
  const origin = admin ? state.admin_url : state.user_url;
  const cookie = admin ? state.admin_cookie : state.users[index].cookie;
  const result = await browser.newContext({
    viewport: { width: 1280, height: 900 },
  });
  await result.addCookies([
    {
      name: cookie.Name,
      value: cookie.Value,
      domain: new URL(origin).hostname,
      path: admin ? '/admin' : '/api',
      httpOnly: true,
      sameSite: 'Lax',
    },
  ]);
  await result.addInitScript((language) => {
    if (!['http:', 'https:'].includes(location.protocol)) return;
    localStorage.setItem('nb.lang', language);
    localStorage.setItem('nb.theme', language === 'zh' ? 'dark' : 'light');
  }, language);
  return result;
}
async function api(
  context: BrowserContext,
  path: string,
  method = 'GET',
  data?: unknown,
  admin = false,
) {
  const origin = admin ? fixture().admin_url : fixture().user_url;
  return context.request.fetch(origin + path, {
    method,
    data,
    headers: {
      Origin: origin,
      'Idempotency-Key': randomBytes(16).toString('base64url'),
    },
  });
}
async function control(context: BrowserContext, action: string, data = {}) {
  const state = fixture();
  const response = await context.request.post(state.control_url + '/' + action, {
    data,
    headers: { Authorization: 'Bearer ' + state.control_token },
  });
  expect(response.status()).toBe(200);
  return response.json();
}
async function task(context: BrowserContext, id: string): Promise<ImageTask> {
  const response = await api(context, base + '/tasks/' + id);
  expect(response.status()).toBe(200);
  const result = (await response.json()) as ImageTask;
  const raw = JSON.stringify(result);
  for (const marker of fixture().private_markers) expect(raw).not.toContain(marker);
  return result;
}
async function waitTask(context: BrowserContext, id: string, status: ImageTask['status']) {
  await expect.poll(async () => (await task(context, id)).status, { timeout: 20_000 }).toBe(status);
  return task(context, id);
}
async function submit(page: Page, prompt: string, n: number): Promise<ImageTask> {
  await page.getByRole('textbox', { name: /^Prompt/ }).fill(prompt);
  await page.getByLabel('Image count').fill(String(n));
  const receipt = page.waitForResponse(
    (response) =>
      new URL(response.url()).pathname === base + '/tasks' &&
      response.request().method() === 'POST',
  );
  await page.getByRole('button', { name: 'Reserve currency and join queue' }).click();
  const response = await receipt;
  expect(response.status()).toBe(200);
  return ((await response.json()) as { task: ImageTask }).task;
}
function details(page: Page) {
  return page.getByRole('heading', { name: 'Task details', exact: true }).locator('..');
}
async function preview(page: Page, name: string) {
  mkdirSync(previewDir, { recursive: true });
  await page.screenshot({ path: join(previewDir, name), fullPage: true });
}
async function noHorizontalOverflow(page: Page) {
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(
    true,
  );
}
function privacy(page: Page) {
  const requests: string[] = [],
    bodies: string[] = [],
    pending: Promise<void>[] = [];
  page.on('request', (request) => {
    if (request.url().startsWith('http')) requests.push(new URL(request.url()).origin);
  });
  page.on('response', (response) => {
    if (
      new URL(response.url()).pathname.startsWith(base) &&
      response.headers()['content-type']?.includes('json')
    ) {
      pending.push(
        response
          .text()
          .then((text) => {
            bodies.push(text);
          })
          .catch(() => {}),
      );
    }
  });
  return async () => {
    await Promise.all(pending);
    expect([...new Set(requests)]).toEqual([fixture().user_url]);
    for (const marker of fixture().private_markers) expect(bodies.join('\n')).not.toContain(marker);
    expect(bodies.join('\n')).not.toContain('private-browser-prompt');
  };
}

async function chooseLinkedSize(page: Page | Locator, mode: SizeCapability['mode']) {
  if (mode === 'width_height') {
    await page.getByLabel('Width', { exact: true }).fill('1024');
    await page.getByLabel('Height', { exact: true }).fill('768');
  } else {
    await page.getByLabel('Aspect ratio', { exact: true }).selectOption('4:3');
    const resolution = page.getByLabel('Resolution', { exact: true });
    if (await resolution.count()) await resolution.selectOption('standard');
  }
}

test.describe.configure({ mode: 'serial' });
test('real queue cancellation and partial success retain exact accounting and browser privacy', async ({
  browser,
}) => {
  const user = await context(browser),
    other = await context(browser, 1);
  try {
    const page = await user.newPage(),
      checkPrivacy = privacy(page);
    await page.clock.install({ time: new Date() });
    await page.goto(fixture().user_url + '/activities/picture-book');
    await expect(page.getByRole('heading', { name: 'Create images' })).toBeVisible();
    await expect(
      page.getByText('<img src=x onerror=alert(1)> is displayed as text.'),
    ).toBeVisible();
    expect(await page.locator('img[src="x"]').count()).toBe(0);
    await noHorizontalOverflow(page);
    await preview(page, 'user-form-en-1280.png');
    await page.getByLabel('Width', { exact: true }).fill('288');
    await page.getByLabel('Height', { exact: true }).fill('400');
    const acceptedRequest = page.waitForRequest(
      (request) =>
        new URL(request.url()).pathname === base + '/tasks' && request.method() === 'POST',
    );
    const held = await submit(page, 'hold private-browser-prompt', 1);
    await waitTask(user, held.id, 'running');
    await page.clock.runFor(5000);
    const waiting = details(page).locator('.picturebook-wait');
    const frames = waiting.locator('img');
    const visibleFrame = waiting.locator('.picturebook-wait__frame--visible');
    const pause = waiting.getByRole('button', { name: 'Pause waiting animation', exact: true });
    const resume = waiting.getByRole('button', { name: 'Resume waiting animation', exact: true });
    await expect(pause).toBeVisible();
    await expect(frames).toHaveCount(4);
    await expect(waiting.locator('.picturebook-wait__frames')).toHaveAttribute(
      'aria-hidden',
      'true',
    );
    await expect
      .poll(() =>
        frames.evaluateAll((images) =>
          images.map((node) => {
            const image = node as HTMLImageElement;
            return {
              complete: image.complete,
              width: image.naturalWidth,
              height: image.naturalHeight,
            };
          }),
        ),
      )
      .toEqual(Array.from({ length: 4 }, () => ({ complete: true, width: 1280, height: 720 })));
    const frameSources = await frames.evaluateAll((images) =>
      images.map((image) => image.getAttribute('src')!),
    );
    expect(new Set(frameSources).size).toBe(4);
    expect(
      frameSources.every((src) => new URL(src, fixture().user_url).origin === fixture().user_url),
    ).toBe(true);
    await page.clock.pauseAt(new Date((await page.evaluate(() => Date.now())) + 1000));
    const initialFrame = frameSources.indexOf((await visibleFrame.getAttribute('src'))!);
    expect(initialFrame).toBeGreaterThanOrEqual(0);
    await page.clock.runFor(2000);
    const nextFrame = frameSources[(initialFrame + 1) % frameSources.length];
    await expect(visibleFrame).toHaveAttribute('src', nextFrame);
    await noHorizontalOverflow(page);
    await preview(page, 'user-wait-running-en-1280.png');
    await pause.focus();
    await page.keyboard.press('Space');
    await expect(resume).toHaveAttribute('aria-pressed', 'true');
    await page.clock.runFor(4000);
    await expect(visibleFrame).toHaveAttribute('src', nextFrame);
    await page.setViewportSize({ width: 390, height: 844 });
    await noHorizontalOverflow(page);
    await preview(page, 'user-wait-paused-en-390.png');
    await page.keyboard.press('Space');
    await expect(pause).toHaveAttribute('aria-pressed', 'false');
    await page.clock.runFor(2000);
    await expect(visibleFrame).toHaveAttribute(
      'src',
      frameSources[(initialFrame + 2) % frameSources.length],
    );
    await page.emulateMedia({ reducedMotion: 'reduce' });
    await expect(pause).toHaveCount(0);
    await expect(resume).toHaveCount(0);
    await expect(visibleFrame).toHaveAttribute('src', frameSources[0]);
    await page.clock.runFor(4000);
    await expect(visibleFrame).toHaveAttribute('src', frameSources[0]);
    await noHorizontalOverflow(page);
    await preview(page, 'user-wait-reduced-en-390.png');
    await page.emulateMedia({ reducedMotion: 'no-preference' });
    await page.setViewportSize({ width: 1280, height: 900 });
    const request = await acceptedRequest;
    const beforeReplay = await control(user, 'stats');
    const replay = await user.request.post(fixture().user_url + base + '/tasks', {
      data: request.postDataJSON(),
      headers: {
        Origin: fixture().user_url,
        'Idempotency-Key': request.headers()['idempotency-key'],
      },
    });
    expect(replay.status()).toBe(200);
    expect((await replay.json()).task.id).toBe(held.id);
    expect((await control(user, 'stats')).submissions).toBe(beforeReplay.submissions);
    const conflict = await user.request.post(fixture().user_url + base + '/tasks', {
      data: { ...request.postDataJSON(), prompt: 'changed replay prompt' },
      headers: {
        Origin: fixture().user_url,
        'Idempotency-Key': request.headers()['idempotency-key'],
      },
    });
    expect(conflict.status()).toBe(409);
    const queued = await submit(page, 'success queued private-browser-prompt', 2);
    expect(queued.charge).toEqual({ paper: '4', brush: '2' });
    await expect(waiting).toBeVisible();
    await expect(visibleFrame).toHaveAttribute('src', frameSources[0]);
    await expect(waiting.getByRole('button')).toHaveCount(0);
    await page.clock.runFor(6000);
    await expect(visibleFrame).toHaveAttribute('src', frameSources[0]);
    await noHorizontalOverflow(page);
    await preview(page, 'user-wait-queued-en-1280.png');
    await page.setViewportSize({ width: 390, height: 844 });
    await noHorizontalOverflow(page);
    await preview(page, 'user-wait-queued-en-390.png');
    await page.setViewportSize({ width: 1280, height: 900 });
    await page.clock.resume();
    const admin = await context(browser, 0, 'en', true);
    try {
      const current = await (
        await api(admin, adminBase + '/models/' + fixture().model_id, 'GET', undefined, true)
      ).json();
      const repriced = await api(
        admin,
        adminBase + '/models/' + current.id,
        'PUT',
        {
          expected_revision: current.revision,
          enabled: true,
          price: { paper: '7', brush: '3' },
        },
        true,
      );
      expect(repriced.status()).toBe(200);
      expect((await task(user, queued.id)).charge).toEqual({
        paper: '4',
        brush: '2',
      });
      expect((await task(user, held.id)).charge).toEqual({
        paper: '2',
        brush: '1',
      });
      const receipt = await repriced.json();
      expect(
        (
          await api(
            admin,
            adminBase + '/models/' + current.id,
            'PUT',
            {
              expected_revision: receipt.revision,
              enabled: true,
              price: { paper: '2', brush: '1' },
            },
            true,
          )
        ).status(),
      ).toBe(200);
    } finally {
      await admin.close();
    }
    await expect(
      details(page).getByRole('button', {
        name: 'Cancel and refund reservation',
      }),
    ).toBeVisible();
    const queue = await (await api(other, base + '/queue')).json();
    expect(queue.own).toEqual([]);
    expect(Object.keys(queue).sort()).toEqual(['dispatch_paused', 'own', 'queued', 'running']);
    expect((await api(other, base + '/tasks/' + held.id)).status()).toBe(404);
    await details(page).getByRole('button', { name: 'Cancel and refund reservation' }).click();
    const cancelled = await waitTask(user, queued.id, 'cancelled');
    await expect(waiting).toHaveCount(0);
    expect(cancelled.billing_state).toBe('refunded');
    expect(cancelled.charge).toEqual({ paper: '0', brush: '0' });
    expect(cancelled.refund).toEqual({ paper: '4', brush: '2' });
    await control(user, 'release');
    await control(user, 'advance', { seconds: 30 });
    await waitTask(user, held.id, 'succeeded');
    await page.reload();
    await expect(page.getByRole('heading', { name: 'Create images' })).toBeVisible();
    const partial = await submit(page, 'partial private-browser-prompt', 4);
    expect(partial.charge).toEqual({ paper: '8', brush: '4' });
    await waitTask(user, partial.id, 'running');
    await control(user, 'advance', { seconds: 6 });
    const finished = await waitTask(user, partial.id, 'succeeded');
    expect(finished.actual_images).toBe(2);
    expect(finished.charge).toEqual({ paper: '8', brush: '4' });
    expect(finished.refund).toEqual({ paper: '0', brush: '0' });
    await expect(details(page).getByRole('img', { name: /Generated image/ })).toHaveCount(2);
    await expect(waiting).toHaveCount(0);
    await expect
      .poll(() =>
        details(page)
          .getByRole('img', { name: /Generated image/ })
          .first()
          .evaluate((node) => {
            const image = node as HTMLImageElement;
            return {
              complete: image.complete,
              width: image.naturalWidth,
              height: image.naturalHeight,
            };
          }),
      )
      .toEqual({ complete: true, width: 8, height: 8 });
    await expect(details(page).getByText(/charged the full submitted price/)).toBeVisible();
    const download = page.waitForEvent('download');
    await details(page).getByRole('link', { name: 'Download original' }).first().click();
    const original = await download;
    expect(original.suggestedFilename()).toMatch(/\.png$/);
    const originalPath = await original.path();
    expect(originalPath).not.toBeNull();
    const originalBytes = readFileSync(originalPath!);
    expect(originalBytes.length).toBe(finished.images[0].bytes);
    expect([...originalBytes.subarray(0, 8)]).toEqual([137, 80, 78, 71, 13, 10, 26, 10]);
    await control(user, 'advance', { seconds: 601 });
    await expect.poll(async () => (await task(user, partial.id)).result_available).toBe(false);
    await expect(details(page).getByText(/remain downloadable until you leave/)).toBeVisible();
    await expect(details(page).getByRole('img', { name: /Generated image/ })).toHaveCount(2);
    expect((await task(user, partial.id)).billing_state).toBe('charged');
    await preview(page, 'user-results-en.png');
    expect(
      await page.evaluate(() =>
        JSON.stringify({
          local: { ...localStorage },
          session: { ...sessionStorage },
        }),
      ),
    ).not.toContain('private-browser-prompt');
    await checkPrivacy();
  } finally {
    await user.close();
    await other.close();
  }
});

test('real close and restart restore known work, refund lost queue memory and never refund completed images', async ({
  browser,
}) => {
  const user = await context(browser);
  try {
    let page = await user.newPage();
    await page.goto(fixture().user_url + '/activities/picture-book');
    const completed = await submit(page, 'synchronous before restart', 1);
    await control(user, 'advance', { seconds: 5 });
    await waitTask(user, completed.id, 'succeeded');
    expect((await task(user, completed.id)).result_available).toBe(true);
    const held = await submit(page, 'hold private-browser-prompt', 1);
    await waitTask(user, held.id, 'running');
    const queued = await submit(page, 'success queued before restart', 1);
    await waitTask(user, queued.id, 'queued');
    const before = (await control(user, 'stats')) as { submissions: number };
    await page.close();
    expect((await task(user, held.id)).status).toBe('running');
    await control(user, 'restart');
    const lost = await waitTask(user, queued.id, 'cancelled');
    expect(lost.error_code).toBe('service_restarted');
    expect(lost.billing_state).toBe('refunded');
    expect(lost.refund).toEqual({ paper: '2', brush: '1' });
    const unavailable = await task(user, completed.id);
    expect(unavailable.result_available).toBe(false);
    expect(unavailable.actual_images).toBe(1);
    expect(unavailable.billing_state).toBe('charged');
    await control(user, 'release');
    await control(user, 'advance', { seconds: 90 });
    await waitTask(user, held.id, 'succeeded');
    expect(((await control(user, 'stats')) as { submissions: number }).submissions).toBe(
      before.submissions,
    );
    page = await user.newPage();
    await page.goto(fixture().user_url + '/activities/picture-book');
    await expect(page.getByRole('textbox', { name: /^Prompt/ })).toHaveValue('');
    await expect(page.getByText(/service restarted; queued tasks were refunded/i)).toHaveCount(0);
    const failed = await submit(page, 'fail private-browser-prompt', 2);
    await waitTask(user, failed.id, 'running');
    await control(user, 'advance', { seconds: 6 });
    const failure = await waitTask(user, failed.id, 'failed');
    expect(failure.billing_state).toBe('refunded');
    expect(failure.refund).toEqual({ paper: '4', brush: '2' });
    expect(failure.charge).toEqual({ paper: '0', brush: '0' });
    await expect(details(page).getByText('4 paper + 2 brushes', { exact: true })).toBeVisible();
    const wallet = (await (await api(user, base + '/wallet')).json()) as {
      sketch_paper: string;
    };
    expect(BigInt(wallet.sketch_paper)).toBeGreaterThan(0n);
    await expect(
      page
        .locator('.limited-facts dt')
        .filter({ hasText: /^Sketch paper$/ })
        .locator('xpath=following-sibling::dd[1]'),
    ).toHaveText(wallet.sketch_paper);
  } finally {
    await user.close();
  }
});

test('real administrator editor and narrow bilingual user pages preserve role and origin boundaries', async ({
  browser,
}) => {
  const admin = await context(browser, 0, 'en', true);
  try {
    const page = await admin.newPage();
    await page.goto(fixture().admin_url + '/limited-activities?activity=picture-book');
    await page.getByRole('tab', { name: 'Image generation service', exact: true }).click();
    await expect(page.getByRole('heading', { name: 'Image generation service' })).toBeVisible();
    await page.getByRole('tab', { name: 'Model catalog', exact: true }).click();
    const model = await (
      await api(admin, adminBase + '/models/' + fixture().model_id, 'GET', undefined, true)
    ).json();
    await page.getByLabel('Choose a model to configure').selectOption(model.id);
    const editor = page
      .getByRole('heading', { name: 'Model availability and pricing' })
      .locator('..');
    await expect(editor.getByText(modelName, { exact: true })).toBeVisible();
    await expect(editor.getByText(modelDescription, { exact: true })).toBeVisible();
    expect(await page.locator('svg[onload], img[src="x"]').count()).toBe(0);
    await expect(
      page.getByLabel(/Public display name|Public description|JSON|catalog_type_pointer/),
    ).toHaveCount(0);
    await editor.getByLabel('Sketch paper per image').fill('3');
    const saved = page.waitForResponse(
      (response) =>
        new URL(response.url()).pathname.endsWith('/models/' + model.id) &&
        response.request().method() === 'PUT',
    );
    await page.getByRole('button', { name: 'Save model settings' }).click();
    const receipt = await saved;
    expect(receipt.status()).toBe(200);
    expect(Object.keys(receipt.request().postDataJSON()).sort()).toEqual([
      'enabled',
      'expected_revision',
      'price',
      'pricing',
    ]);
    await expect(editor.getByLabel('Sketch paper per image')).toBeEnabled();
    const current = await (
      await api(admin, adminBase + '/models/' + model.id, 'GET', undefined, true)
    ).json();
    expect(current.description).toBe(modelDescription);
    expect(current.display_name).toBe(modelName);
    expect(current.price).toEqual({ paper: '3', brush: '1' });
    expect(
      (
        await api(
          admin,
          adminBase + '/models/' + model.id,
          'PUT',
          {
            expected_revision: current.revision,
            enabled: true,
            price: current.price,
            parameters: [],
          },
          true,
        )
      ).status(),
    ).toBe(400);
    const upstream = await (
      await api(admin, adminBase + '/upstream', 'GET', undefined, true)
    ).json();
    expect(
      (
        await api(
          admin,
          adminBase + '/upstream',
          'PUT',
          {
            expected_revision: upstream.revision,
            base_url: upstream.base_url,
            secret: { mode: 'keep' },
            adapter: {},
          },
          true,
        )
      ).status(),
    ).toBe(400);
    expect(
      (
        await api(admin, adminBase + '/upstream/capability-profile', 'GET', undefined, true)
      ).status(),
    ).toBe(404);
    await preview(page, 'admin-settings-en.png');
  } finally {
    await admin.close();
  }
  for (const index of [0, 2, 3]) {
    const user = await context(browser, index, 'zh');
    try {
      const page = await user.newPage(),
        checkPrivacy = privacy(page);
      await page.setViewportSize({ width: 390, height: 844 });
      await page.goto(fixture().user_url + '/activities/picture-book');
      await expect(page.getByRole('heading', { name: '创作图片' })).toBeVisible();
      await expect(page.getByText(modelDescription, { exact: true })).toBeVisible();
      await expect(page.locator('select option:checked').filter({ hasText: modelName })).toHaveText(
        modelName,
      );
      expect(await page.locator('svg[onload], img[src="x"]').count()).toBe(0);
      await page.getByRole('textbox', { name: /^提示词/ }).focus();
      await page.keyboard.type('keyboard-only prompt');
      await expect(page.getByRole('textbox', { name: /^提示词/ })).toHaveValue(
        'keyboard-only prompt',
      );
      expect(
        await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1),
      ).toBe(true);
      for (const method of ['GET', 'PUT']) {
        const result = await api(
          user,
          '/admin/api/limited-activities/picture-book/upstream',
          method,
          method === 'PUT' ? {} : undefined,
          true,
        );
        expect([401, 403]).toContain(result.status());
      }
      await checkPrivacy();
      if (index === 0) await preview(page, 'user-form-zh-390.png');
    } finally {
      await user.close();
    }
  }
});

test('real linked size editors preserve drafts and quote all four modes without consumption', async ({
  browser,
}) => {
  const admin = await context(browser, 0, 'en', true);
  const user = await context(browser);
  try {
    const page = await admin.newPage();
    const userPage = await user.newPage();
    const beforeStats = await control(user, 'stats');
    const beforeWallet = await (await api(user, base + '/wallet')).json();
    const beforeTasks = await (await api(user, base + '/tasks')).json();
    const writes: string[] = [];
    page.on('request', (request) => {
      if (request.method() !== 'GET') writes.push(new URL(request.url()).pathname);
    });
    const models = (await (await api(admin, adminBase + '/models', 'GET', undefined, true)).json())
      .data as AdminModel[];
    expect(models).toHaveLength(5);
    const model = models.find((item) => item.id === fixture().model_id)!;
    const beforeIDs = models.map((item) => item.id).sort();
    await page.goto(fixture().admin_url + '/limited-activities?activity=picture-book');
    await page.getByRole('tab', { name: 'Model catalog', exact: true }).click();
    const editor = page
      .getByRole('heading', { name: 'Model availability and pricing' })
      .locator('..');
    for (const [index, model] of models.entries()) {
      const capability = model.size_capability!;
      const hasTier = capability.combinations?.some((row) => row.tier === 'standard') ?? false;
      await page.setViewportSize({
        width: index % 2 ? 390 : 1280,
        height: 900,
      });
      await page.getByLabel('Choose a model to configure').selectOption(model.id);
      await expect(editor.getByText(model.display_name, { exact: true })).toBeVisible();
      await expect(page.getByLabel(/JSON|catalog_type_pointer/)).toHaveCount(0);
      await editor.getByLabel('Sketch paper per image').fill('2');
      await editor.getByLabel('Brushes per image', { exact: true }).fill('1');
      await editor.getByLabel('Make this model available').check();
      if (!(await editor.getByRole('button', { name: 'Add tier price', exact: true }).isVisible()))
        await editor.getByText('Optional size prices', { exact: true }).click();
      if (hasTier) {
        await page.getByRole('button', { name: 'Add tier price', exact: true }).click();
        await editor.getByLabel('Tier sketch paper 1').fill('4');
        await editor.getByLabel('Tier brushes 1').fill('2');
      } else
        await expect(
          page.getByRole('button', { name: 'Add tier price', exact: true }),
        ).toBeDisabled();
      await page.getByRole('button', { name: 'Add size price', exact: true }).click();
      await editor.getByLabel('Price width 1').fill('1024');
      await editor.getByLabel('Price height 1').fill('768');
      await editor.getByLabel('Size sketch paper 1').fill('7');
      await editor.getByLabel('Size brushes 1').fill('3');
      await editor.getByText('Try parameters and prices (no charge)', { exact: true }).click();
      if (capability.mode === 'resolution_ratio_grid') {
        await expect(editor.getByLabel('Aspect ratio', { exact: true })).toHaveValue('4:3');
        await expect(
          editor.getByLabel('Resolution', { exact: true }).locator('option'),
        ).toHaveCount(1);
      }
      await page.getByRole('textbox', { name: /^Prompt/ }).fill('Synthetic no-charge preview');
      await editor.getByLabel('Image count').fill('2');
      await chooseLinkedSize(editor, capability.mode);
      const checked = page.waitForResponse(
        (response) => new URL(response.url()).pathname === adminBase + '/models/check',
      );
      await page.getByRole('button', { name: 'Check draft', exact: true }).click();
      const checkResponse = await checked;
      expect(checkResponse.status()).toBe(200);
      const check = await checkResponse.json();
      expect(check.valid, JSON.stringify(check.issues)).toBe(true);
      const price =
        capability.mode !== 'ratio_resolution'
          ? { paper: '14', brush: '6' }
          : hasTier
            ? { paper: '8', brush: '4' }
            : { paper: '4', brush: '2' };
      expect(check.quote.total).toEqual(price);
      expect(check.quote.basis).toBe(
        capability.mode !== 'ratio_resolution' ? 'size' : hasTier ? 'tier' : 'default',
      );
      if (
        capability.mode === 'ratio_size_map' ||
        (capability.mode === 'ratio_resolution' && !hasTier)
      )
        expect(check.effective_parameters).not.toHaveProperty('resolution');
      else if (hasTier) expect(check.effective_parameters.resolution).toBe('standard');
      const saved = page.waitForResponse(
        (response) =>
          new URL(response.url()).pathname === adminBase + '/models/' + model.id &&
          response.request().method() === 'PUT',
      );
      const reread = page.waitForResponse(
        (response) =>
          new URL(response.url()).pathname === adminBase + '/models/' + model.id &&
          response.request().method() === 'GET',
      );
      await page.getByRole('button', { name: 'Save model settings', exact: true }).click();
      const receipt = await saved;
      expect(receipt.status()).toBe(200);
      expect(Object.keys(receipt.request().postDataJSON()).sort()).toEqual([
        'enabled',
        'expected_revision',
        'price',
        'pricing',
      ]);
      const accepted = await (await reread).json();
      expect(accepted.size_capability).toEqual(capability);
      expect(accepted.capability_readiness).toBe('ready');
      expect(accepted.capability_issues).toEqual([]);
      expect(
        accepted.parameter_capabilities.every(
          (entry: { source: string }) => entry.source === 'discovered',
        ),
      ).toBe(true);
      expect(accepted.display_name).toBe(model.display_name);
      expect(accepted.description).toBe(model.description);
      await expect(
        page.getByRole('button', { name: 'Save model settings', exact: true }),
      ).toBeEnabled();
      await userPage.setViewportSize({
        width: index % 2 ? 1280 : 390,
        height: 844,
      });
      await userPage.goto(fixture().user_url + '/activities/picture-book');
      await userPage.getByRole('combobox', { name: /^Image model/ }).selectOption(model.id);
      await userPage.getByRole('textbox', { name: /^Prompt/ }).fill('Synthetic no-charge preview');
      await userPage.getByLabel('Image count').fill('2');
      await chooseLinkedSize(userPage, capability.mode);
      await expect(
        userPage.getByText(`${price.paper} paper + ${price.brush} brushes`, {
          exact: true,
        }),
      ).toBeVisible();
      const quote = await api(user, base + '/quote', 'POST', {
        model_id: model.id,
        expected_model_revision: accepted.revision,
        expected_pricing_revision: accepted.pricing_revision,
        ...check.effective_parameters,
      });
      expect(quote.status()).toBe(200);
      expect((await quote.json()).total).toEqual(price);
      expect(
        await userPage.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1),
      ).toBe(true);
    }
    await page.setViewportSize({ width: 1280, height: 900 });
    await page.getByLabel('Choose a model to configure').selectOption(model.id);
    await expect(editor.getByText(model.display_name, { exact: true })).toBeVisible();
    await editor.getByLabel('Sketch paper per image').fill('6');
    await page.locator('a[href="/"]').first().click();
    await expect(page.getByRole('button', { name: 'Save and leave' })).toBeVisible();
    await page.getByRole('button', { name: 'Continue editing' }).click();
    await expect(editor.getByLabel('Sketch paper per image')).toHaveValue('6');
    await page.locator('a[href="/"]').first().click();
    await page.getByRole('button', { name: 'Save and leave' }).click();
    await expect(page).toHaveURL(fixture().admin_url + '/');
    const savedModel = await (
      await api(admin, adminBase + '/models/' + model.id, 'GET', undefined, true)
    ).json();
    expect(savedModel.price).toEqual({ paper: '6', brush: '1' });
    expect(savedModel.display_name).toBe(modelName);
    expect(savedModel.description).toBe(modelDescription);
    const second = await (
      await api(
        admin,
        adminBase + '/models/' + models.find((item) => item.id !== model.id)!.id,
        'GET',
        undefined,
        true,
      )
    ).json();
    const changes = [savedModel, second].map((item) => ({
      id: item.id,
      input: {
        expected_revision: item.revision,
        enabled: true,
        price: { paper: '8', brush: '1' },
        pricing: { ...item.pricing, default: { paper: '8', brush: '1' } },
      },
    }));
    const rejected = await api(
      admin,
      adminBase + '/models/batch',
      'POST',
      {
        models: [
          changes[0],
          {
            ...changes[1],
            input: { ...changes[1].input, expected_revision: '999999' },
          },
        ],
      },
      true,
    );
    expect(rejected.status()).toBe(200);
    const rejection = await rejected.json();
    expect(rejection.applied).toBe(false);
    expect(rejection.receipts).toEqual([]);
    for (const unchanged of [savedModel, second]) {
      const current = await (
        await api(admin, adminBase + '/models/' + unchanged.id, 'GET', undefined, true)
      ).json();
      expect(current.revision).toBe(unchanged.revision);
      expect(current.pricing).toEqual(unchanged.pricing);
    }
    const batch = await api(admin, adminBase + '/models/batch', 'POST', { models: changes }, true);
    expect(batch.status()).toBe(200);
    const batchReceipt = await batch.json();
    expect(batchReceipt.applied).toBe(true);
    expect(batchReceipt.receipts).toHaveLength(2);
    for (const change of changes) {
      const current = await (
        await api(admin, adminBase + '/models/' + change.id, 'GET', undefined, true)
      ).json();
      expect(current.pricing).toEqual(change.input.pricing);
    }
    const afterModels = (
      await (await api(admin, adminBase + '/models', 'GET', undefined, true)).json()
    ).data as AdminModel[];
    expect(afterModels.map((item) => item.id).sort()).toEqual(beforeIDs);
    expect(await control(user, 'stats')).toEqual(beforeStats);
    expect(await (await api(user, base + '/wallet')).json()).toEqual(beforeWallet);
    expect(await (await api(user, base + '/tasks')).json()).toEqual(beforeTasks);
    expect(writes.filter((path) => /\/quote|\/tasks/.test(path))).toEqual([]);
    expect(await page.evaluate(() => JSON.stringify({ ...localStorage }))).not.toContain(
      'private-browser-prompt',
    );
  } finally {
    await admin.close();
    await user.close();
  }
});

test('real endpoint adaptation hides saved values and blocks stale browser writes', async ({
  browser,
}) => {
  const owner = await context(browser);
  const other = await context(browser, 1);
  try {
    const created = await api(owner, '/api/endpoints', 'POST', {
      source: 'custom',
      connector_type: 'openai-compatible',
      base_url: 'https://adaptation.example/v1',
      note: 'Browser adaptation',
      enabled: true,
    });
    expect(created.status()).toBe(201);
    const endpoint = await created.json();
    const path = '/api/endpoints/' + endpoint.id + '/request-adaptation';
    const page = await owner.newPage();
    await page.goto(fixture().user_url + '/endpoints/' + endpoint.id);
    await page.getByText('Request rewriting (advanced)', { exact: true }).click();
    const fixed = page.getByRole('group', {
      name: 'Headers added to requests',
      exact: true,
    });
    await fixed.getByRole('button', { name: 'Add field' }).click();
    await fixed.getByLabel('Header or path').fill('X-Synthetic-Header');
    await fixed.getByLabel('Value', { exact: true }).fill('synthetic-private-header');
    const forced = page.getByRole('group', {
      name: 'Fixed parameters (always override)',
      exact: true,
    });
    await forced.getByRole('button', { name: 'Add field' }).click();
    await forced.getByLabel('Header or path').fill('/reasoning_effort');
    await forced.getByLabel('Value', { exact: true }).fill('"medium"');
    await page
      .getByRole('group', { name: 'Allowed client headers', exact: true })
      .getByRole('textbox')
      .fill('X-Client-Tag');
    const saved = page.waitForResponse(
      (response) =>
        new URL(response.url()).pathname === path && response.request().method() === 'PUT',
    );
    await page.getByRole('button', { name: 'Save request rewriting' }).click();
    const receipt = await saved;
    expect(receipt.status()).toBe(200);
    expect(await receipt.text()).not.toContain('synthetic-private-header');
    await expect(fixed.getByText('Saved value is hidden')).toBeVisible();
    await page.reload();
    await page.getByText('Request rewriting (advanced)', { exact: true }).click();
    await expect(fixed.getByText('Saved value is hidden')).toBeVisible();
    await fixed.getByRole('combobox').selectOption('replace');
    await expect(fixed.getByLabel('Value', { exact: true })).toHaveValue('');
    await fixed.getByLabel('Value', { exact: true }).fill('synthetic-replacement');
    const currentResponse = await api(owner, path);
    expect(currentResponse.status()).toBe(200);
    const current = await currentResponse.json();
    expect(current.forward_headers.values).toEqual(['X-Client-Tag']);
    expect(JSON.stringify(current)).not.toContain('synthetic-private-header');
    expect((await api(other, path)).status()).toBe(404);
    const concurrent = await api(owner, path, 'PUT', {
      expected_revision: current.revision,
      body_forced: {
        mode: 'replace',
        values: { '/reasoning_effort': { action: 'replace', value: 'low' } },
      },
    });
    expect(concurrent.status()).toBe(200);
    const stale = page.waitForResponse(
      (response) =>
        new URL(response.url()).pathname === path && response.request().method() === 'PUT',
    );
    await page.getByRole('button', { name: 'Save request rewriting' }).click();
    expect((await stale).status()).toBe(409);
    await expect(page.getByRole('button', { name: 'Save request rewriting' })).toBeDisabled();
    await page.getByRole('button', { name: 'Refresh configuration', exact: true }).click();
    await expect(page.getByRole('button', { name: 'Save request rewriting' })).toBeEnabled();
    await expect(fixed.getByText('Saved value is hidden')).toBeVisible();
    expect(
      await page.evaluate(() =>
        JSON.stringify({
          local: { ...localStorage },
          session: { ...sessionStorage },
        }),
      ),
    ).not.toContain('synthetic-');
  } finally {
    await owner.close();
    await other.close();
  }
});
