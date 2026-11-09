import { mkdirSync, readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import {
  expect,
  test,
  type Browser,
  type BrowserContext,
  type Locator,
  type Page,
  type TestInfo,
} from '@playwright/test';
import common from '../../src/shared/i18n/common/en.json' with { type: 'json' };
import type { ModelLimitView } from '../../src/shared/operations/modelLimits';

interface Cookie {
  Name: string;
  Value: string;
}
interface Fixture {
  admin_url: string;
  user_url: string;
  admin_cookie: Cookie;
  users: { level: number; cookie: Cookie }[];
  management: {
    model_quotas: {
      donation_id: string;
      key_id: string;
      upstream_models: string[];
      model_ids: string[];
    };
  };
}
const copy = common.common.modelLimits;
const fixture = () =>
  JSON.parse(readFileSync(process.env.NONBIRI_AUDIT_BROWSER_STATE!, 'utf8')) as Fixture;
test.skip(
  process.env.NONBIRI_MANAGEMENT_BROWSER !== '1',
  'Requires the isolated management fixture.',
);

async function session(browser: Browser, role: 'admin' | 'owner' | 'caller') {
  const state = fixture();
  const admin = role === 'admin';
  const origin = admin ? state.admin_url : state.user_url;
  const cookie = admin
    ? state.admin_cookie
    : state.users.filter((user) => user.level === 1)[role === 'owner' ? 0 : 1]!.cookie;
  const context = await browser.newContext({
    viewport: { width: 1440, height: 900 },
    reducedMotion: 'reduce',
  });
  const origins = new Set([state.admin_url, state.user_url]);
  await context.route('**/*', (route) =>
    origins.has(new URL(route.request().url()).origin)
      ? route.continue()
      : route.abort('blockedbyclient'),
  );
  await context.addCookies([
    {
      name: cookie.Name,
      value: cookie.Value,
      domain: new URL(origin).hostname,
      path: admin ? '/admin' : '/api',
      httpOnly: true,
      sameSite: 'Lax',
    },
  ]);
  await context.addInitScript(() => {
    localStorage.setItem('nb.lang', 'en');
    localStorage.setItem('nb.theme', 'light');
  });
  return context;
}
async function read<T>(context: BrowserContext, origin: string, path: string): Promise<T> {
  const response = await context.request.get(origin + path, { headers: { Origin: origin } });
  expect(response.status(), path).toBe(200);
  return response.json() as Promise<T>;
}
async function open(page: Page, role: 'admin' | 'owner') {
  const state = fixture(),
    data = state.management.model_quotas;
  await page.goto(
    role === 'admin'
      ? `${state.admin_url}/charity?charity_section=donations&donation_id=${data.donation_id}`
      : `${state.user_url}/charity/donations/${data.donation_id}`,
  );
  await page.getByText(copy.title, { exact: true }).click();
  return page
    .locator('details')
    .filter({ has: page.locator('summary').filter({ hasText: copy.title }) })
    .last();
}
async function choose(fold: Locator, model: string, editable: boolean) {
  await fold
    .getByRole('row')
    .filter({ hasText: model })
    .getByRole('button', { name: editable ? copy.edit : copy.view, exact: true })
    .click();
  const editor = fold.getByRole('region', { name: model, exact: true });
  await expect(editor).toBeVisible();
  return editor;
}
async function visuals(page: Page, focus: Locator, info: TestInfo, name: string) {
  if (process.env.NONBIRI_VISUAL_DIR)
    mkdirSync(process.env.NONBIRI_VISUAL_DIR, { recursive: true });
  for (const theme of ['light', 'dark'])
    for (const width of [1440, 390]) {
      await page.evaluate((theme) => {
        document.documentElement.dataset.theme = theme;
      }, theme);
      await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
      await focus.scrollIntoViewIfNeeded();
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
        true,
      );
      for (const input of await focus.locator('input').all()) {
        const box = await input.boundingBox();
        if (box) {
          expect(box.x).toBeGreaterThanOrEqual(0);
          expect(box.x + box.width).toBeLessThanOrEqual(width + 1);
        }
      }
      await page.screenshot({
        path: process.env.NONBIRI_VISUAL_DIR
          ? resolve(process.env.NONBIRI_VISUAL_DIR, `${name}-${theme}-${width}.png`)
          : info.outputPath(`${name}-${theme}-${width}.png`),
        animations: 'disabled',
      });
    }
}

test('model quotas preserve another upstream and show the owner read-only amounts', async ({
  browser,
}, info) => {
  const state = fixture(),
    data = state.management.model_quotas;
  const admin = await session(browser, 'admin'),
    owner = await session(browser, 'owner'),
    caller = await session(browser, 'caller');
  try {
    const page = await admin.newPage(),
      errors: string[] = [];
    page.on('pageerror', (error) => errors.push(error.message));
    const fold = await open(page, 'admin');
    const path = `/admin/api/donations/${data.donation_id}/keys/${data.key_id}/model-limits`;
    for (const [index, model] of data.upstream_models.entries()) {
      const editor = await choose(fold, model, true);
      const total = editor.getByLabel(copy.tokens, { exact: true });
      await expect(total).toBeEnabled();
      await total.fill(index === 0 ? '5' : '10');
      const saved = page.waitForResponse(
        (response) =>
          new URL(response.url()).pathname === path && response.request().method() === 'PUT',
      );
      await editor.getByRole('button', { name: copy.save, exact: true }).click();
      expect((await saved).status()).toBe(200);
      await expect(editor.getByText(copy.saved, { exact: true })).toBeVisible();
      if (index === 0) await visuals(page, editor, info, 'model-quota-admin');
      await editor.getByRole('button', { name: copy.back, exact: true }).click();
    }
    const saved = await read<{ data: ModelLimitView[] }>(
      admin,
      state.admin_url,
      path + '?page=1&page_size=10',
    );
    expect(
      saved.data.find((model) => model.upstream_model_id === data.upstream_models[0]),
    ).toMatchObject({ limits: { tokens: '5' }, used: { tokens: '5' }, remaining: { tokens: '0' } });
    expect(
      saved.data.find((model) => model.upstream_model_id === data.upstream_models[1]),
    ).toMatchObject({
      limits: { tokens: '10' },
      used: { tokens: '0' },
      remaining: { tokens: '10' },
    });
    const catalog = await read<{ models: { id: string }[] }>(
      caller,
      state.user_url,
      '/api/charity/models?view=catalog&page=1&page_size=20&q=quota-browser&allowed_for_me=true&currently_available=true',
    );
    expect(catalog.models.map((model) => model.id)).toEqual([data.model_ids[1]]);
    const ownerPage = await owner.newPage();
    ownerPage.on('pageerror', (error) => errors.push(error.message));
    const ownerFold = await open(ownerPage, 'owner');
    const details = await choose(ownerFold, data.upstream_models[0]!, false);
    await expect(details.getByLabel(copy.tokens, { exact: true })).toHaveValue('5');
    await expect(details.getByLabel(copy.tokens, { exact: true })).toBeDisabled();
    await expect(details.getByRole('button', { name: copy.save, exact: true })).toHaveCount(0);
    await visuals(ownerPage, details, info, 'model-quota-owner');
    expect(errors).toEqual([]);
  } finally {
    await Promise.all([admin.close(), owner.close(), caller.close()]);
  }
});
