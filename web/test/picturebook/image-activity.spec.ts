import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { randomBytes } from 'node:crypto';
import { test, expect, type Browser, type BrowserContext, type Page } from '@playwright/test';
import type { ImageTask } from '../../src/shared/picturebook/publicTypes';

interface FixtureState {
  user_url: string;
  admin_url: string;
  control_url: string;
  control_token: string;
  users: { id: string; level: number; cookie: { Name: string; Value: string } }[];
  admin_cookie: { Name: string; Value: string };
  private_markers: string[];
}
const base = '/api/limited-activities/picture-book';
const statePath = process.env.NONBIRI_IMAGE_BROWSER_STATE!;
function fixture(): FixtureState {
  return JSON.parse(readFileSync(statePath, 'utf8')) as FixtureState;
}
async function context(browser: Browser, index = 0, language = 'en', admin = false) {
  const state = fixture();
  const origin = admin ? state.admin_url : state.user_url;
  const cookie = admin ? state.admin_cookie : state.users[index].cookie;
  const result = await browser.newContext({ viewport: { width: 1280, height: 900 } });
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
    headers: { Origin: origin, 'Idempotency-Key': randomBytes(16).toString('base64url') },
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
  await page.getByLabel('Prompt', { exact: false }).fill(prompt);
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

test.describe.configure({ mode: 'serial' });
test('real queue cancellation and partial success retain exact accounting and browser privacy', async ({
  browser,
}) => {
  const user = await context(browser),
    other = await context(browser, 1);
  try {
    const page = await user.newPage(),
      checkPrivacy = privacy(page);
    await page.goto(fixture().user_url + '/activities/picture-book');
    await expect(page.getByRole('heading', { name: 'Create images' })).toBeVisible();
    await expect(
      page.getByText('<img src=x onerror=alert(1)> is displayed as text.'),
    ).toBeVisible();
    expect(await page.locator('img[src="x"]').count()).toBe(0);
    await page.getByLabel('Width', { exact: true }).fill('112');
    await page.getByLabel('Height', { exact: true }).fill('176');
    const held = await submit(page, 'hold private-browser-prompt', 1);
    await waitTask(user, held.id, 'running');
    const queued = await submit(page, 'success queued private-browser-prompt', 2);
    expect(queued.charge).toEqual({ paper: '4', brush: '2' });
    await expect(
      details(page).getByRole('button', { name: 'Cancel and refund reservation' }),
    ).toBeVisible();
    const queue = await (await api(other, base + '/queue')).json();
    expect(queue.own).toEqual([]);
    expect(Object.keys(queue).sort()).toEqual(['dispatch_paused', 'own', 'queued', 'running']);
    expect((await api(other, base + '/tasks/' + held.id)).status()).toBe(404);
    await details(page).getByRole('button', { name: 'Cancel and refund reservation' }).click();
    const cancelled = await waitTask(user, queued.id, 'cancelled');
    expect(cancelled.billing_state).toBe('refunded');
    expect(cancelled.charge).toEqual({ paper: '0', brush: '0' });
    expect(cancelled.refund).toEqual({ paper: '4', brush: '2' });
    await control(user, 'release');
    await control(user, 'advance', { seconds: 30 });
    await waitTask(user, held.id, 'succeeded');
    const partial = await submit(page, 'partial private-browser-prompt', 4);
    expect(partial.charge).toEqual({ paper: '8', brush: '4' });
    await control(user, 'advance', { seconds: 5 });
    const finished = await waitTask(user, partial.id, 'succeeded');
    expect(finished.actual_images).toBe(2);
    expect(finished.charge).toEqual({ paper: '8', brush: '4' });
    expect(finished.refund).toEqual({ paper: '0', brush: '0' });
    await expect(details(page).getByRole('img', { name: /Generated image/ })).toHaveCount(2);
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
    await page.screenshot({
      path: join(dirname(statePath), 'user-results-en.png'),
      fullPage: true,
    });
    expect(
      await page.evaluate(() =>
        JSON.stringify({ local: { ...localStorage }, session: { ...sessionStorage } }),
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
    await expect(page.getByLabel('Prompt', { exact: false })).toHaveValue('');
    await expect(page.getByText(/service restarted; queued tasks were refunded/i)).toHaveCount(0);
    const failed = await submit(page, 'fail private-browser-prompt', 2);
    await control(user, 'advance', { seconds: 5 });
    const failure = await waitTask(user, failed.id, 'failed');
    expect(failure.billing_state).toBe('refunded');
    expect(failure.refund).toEqual({ paper: '4', brush: '2' });
    expect(failure.charge).toEqual({ paper: '0', brush: '0' });
    await expect(details(page).getByText('4 paper + 2 brushes', { exact: true })).toBeVisible();
    const wallet = (await (await api(user, base + '/wallet')).json()) as { sketch_paper: string };
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
    await page.goto(fixture().admin_url + '/limited-activities');
    await expect(page.getByRole('heading', { name: 'Image generation service' })).toBeVisible();
    const model = (
      await (
        await api(
          admin,
          '/admin/api/limited-activities/picture-book/models',
          'GET',
          undefined,
          true,
        )
      ).json()
    ).data[0] as { id: string };
    await page.getByLabel('Choose a model to configure').selectOption(model.id);
    await expect(page.getByLabel('Public display name')).toHaveValue('Browser canvas');
    await page
      .getByLabel('Public description')
      .fill('<svg onload=alert(1)> is plain description text.');
    const saved = page.waitForResponse(
      (response) =>
        new URL(response.url()).pathname.endsWith('/models/' + model.id) &&
        response.request().method() === 'PUT',
    );
    await page.getByRole('button', { name: 'Save model settings' }).click();
    expect((await saved).status()).toBe(200);
    await expect(page.getByLabel('Public description')).toHaveValue(
      '<svg onload=alert(1)> is plain description text.',
    );
    await page.screenshot({
      path: join(dirname(statePath), 'admin-settings-en.png'),
      fullPage: true,
    });
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
      await expect(
        page.getByText('<svg onload=alert(1)> is plain description text.'),
      ).toBeVisible();
      expect(await page.locator('svg[onload]').count()).toBe(0);
      await page.getByLabel('提示词', { exact: false }).focus();
      await page.keyboard.type('keyboard-only prompt');
      await expect(page.getByLabel('提示词', { exact: false })).toHaveValue('keyboard-only prompt');
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
      if (index === 0)
        await page.screenshot({
          path: join(dirname(statePath), 'user-form-zh-390.png'),
          fullPage: true,
        });
    } finally {
      await user.close();
    }
  }
});
