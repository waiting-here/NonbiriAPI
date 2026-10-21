import { readFileSync } from 'node:fs';
import { randomUUID } from 'node:crypto';
import { resolve } from 'node:path';
import {
  expect,
  test,
  type Browser,
  type BrowserContext,
  type Page,
  type TestInfo,
} from '@playwright/test';
import common from '../../src/shared/i18n/common/en.json' with { type: 'json' };

type Cookie = { Name: string; Value: string };
interface Fixture {
  admin_url: string;
  user_url: string;
  admin_cookie: Cookie;
  users: { id: string; level: number; cookie: Cookie }[];
  management: { charity_model_id: string };
}
type Model = {
  id: string;
  revision: string;
  cloudflare_proxy_enabled: boolean;
  cloudflare_proxy_mode: 'worker' | 'legacy' | 'direct';
};
const copy = common.common.cloudflareProxy;
function fixture(): Fixture {
  return JSON.parse(readFileSync(process.env.NONBIRI_AUDIT_BROWSER_STATE!, 'utf8')) as Fixture;
}
test.skip(
  process.env.NONBIRI_MANAGEMENT_BROWSER !== '1',
  'Requires the isolated management fixture.',
);
async function session(browser: Browser, role: 'admin' | 1 | 5 | 6) {
  const state = fixture();
  const origin = role === 'admin' ? state.admin_url : state.user_url;
  const cookie =
    role === 'admin' ? state.admin_cookie : state.users.find((item) => item.level === role)!.cookie;
  const context = await browser.newContext({
    viewport: { width: 1440, height: 900 },
    reducedMotion: 'reduce',
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
    localStorage.setItem('nb.lang', 'en');
    localStorage.setItem('nb.theme', 'light');
  });
  return { context, origin, prefix: role === 'admin' ? '/admin/api' : '/api/steward' };
}
function managementPath(role: 'admin' | 5 | 6, selection: Record<string, string>) {
  const query = new URLSearchParams({
    ...(role === 'admin' ? {} : { tab: 'charity' }),
    charity_section: 'models',
    ...selection,
  });
  return (role === 'admin' ? '/charity' : '/steward') + '?' + query;
}
async function read(context: BrowserContext, origin: string, path: string): Promise<Model> {
  const response = await context.request.get(origin + path, { headers: { Origin: origin } });
  expect(response.ok(), path + ': ' + response.status()).toBe(true);
  return response.json();
}
async function patch(
  context: BrowserContext,
  origin: string,
  path: string,
  model: Model,
  enabled: boolean,
  changed: Record<string, string> = {},
) {
  const response = await context.request.patch(origin + path, {
    headers: { Origin: origin, 'Idempotency-Key': randomUUID() },
    data: { expected_revision: model.revision, cloudflare_proxy_enabled: enabled, ...changed },
  });
  expect(response.ok(), 'Synthetic model update: ' + response.status()).toBe(true);
  return response.json() as Promise<Model>;
}
async function visuals(page: Page, info: TestInfo, name: string) {
  for (const theme of ['light', 'dark']) {
    await page.evaluate((value) => {
      document.documentElement.dataset.theme = value;
    }, theme);
    for (const width of [1440, 390]) {
      await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
      await page.getByRole('switch', { name: copy.label, exact: true }).scrollIntoViewIfNeeded();
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
        true,
      );
      await page.screenshot({
        path: process.env.NONBIRI_VISUAL_DIR
          ? resolve(process.env.NONBIRI_VISUAL_DIR, `${name}-${theme}-${width}.png`)
          : info.outputPath(`${name}-${theme}-${width}.png`),
        animations: 'disabled',
      });
    }
  }
  await page.setViewportSize({ width: 1440, height: 900 });
}

test('personal models default on and retain a changed setting after a real revision conflict', async ({
  browser,
}, info) => {
  const { context, origin } = await session(browser, 1);
  try {
    const page = await context.newPage();
    await page.goto(origin + '/models');
    await page.getByRole('button', { name: 'Create model', exact: true }).click();
    const toggle = page.getByRole('switch', { name: copy.label, exact: true });
    await expect(toggle).toBeChecked();
    await expect(toggle).toHaveAccessibleDescription(copy.new);
    await visuals(page, info, 'personal-cloudflare-new');
    const name = 'proxy-' + randomUUID().slice(0, 8);
    await page.getByRole('textbox', { name: 'Prefix', exact: true }).fill('browser');
    await page.getByRole('textbox', { name: 'Model name', exact: true }).fill(name);
    await toggle.uncheck();
    await page.getByRole('button', { name: 'Save', exact: true }).click();
    await expect(page.getByRole('button', { name: 'Edit model', exact: true })).toBeVisible();
    const id = new URL(page.url()).searchParams.get('model_id');
    expect(id).toMatch(/^[1-9]\d*$/);
    const path = '/api/models/' + id;
    let current = await read(context, origin, path);
    expect(current.cloudflare_proxy_enabled).toBe(false);
    expect(current.cloudflare_proxy_mode).toBe('worker');
    await page.getByRole('button', { name: 'Edit model', exact: true }).click();
    await expect(toggle).not.toBeChecked();
    await toggle.check();
    await page.getByRole('button', { name: 'Save', exact: true }).click();
    await expect(toggle).toHaveCount(0);
    current = await read(context, origin, path);
    expect(current.cloudflare_proxy_enabled).toBe(true);
    await page.getByRole('button', { name: 'Edit model', exact: true }).click();
    await toggle.uncheck();
    await patch(context, origin, path, current, true, { model: name + '-changed' });
    const conflict = page.waitForResponse(
      (response) =>
        response.url().endsWith(path) &&
        response.request().method() === 'PATCH' &&
        response.status() === 409,
    );
    await page.getByRole('button', { name: 'Save', exact: true }).click();
    await conflict;
    await expect(toggle).not.toBeChecked();
    await visuals(page, info, 'personal-cloudflare-conflict');
    await page.getByRole('button', { name: 'Cancel', exact: true }).click();
    await page.getByRole('button', { name: 'Edit model', exact: true }).click();
    await expect(toggle).toBeChecked();
    await toggle.uncheck();
    await page.getByRole('button', { name: 'Save', exact: true }).click();
    await expect(toggle).toHaveCount(0);
    expect((await read(context, origin, path)).cloudflare_proxy_enabled).toBe(false);
    await page.reload();
    await expect(page.getByText(copy.off, { exact: true })).toBeVisible();
  } finally {
    await context.close();
  }
});

for (const role of ['admin', 6] as const) {
  test(`${role} creates with forwarding off, saves it, and reloads a conflicting draft`, async ({
    browser,
  }, info) => {
    const { context, origin, prefix } = await session(browser, role);
    try {
      const page = await context.newPage();
      await page.goto(origin + managementPath(role, { model_workspace: 'new' }));
      const toggle = page.getByRole('switch', { name: copy.label, exact: true });
      await expect(toggle).not.toBeChecked();
      await visuals(page, info, `charity-${role}-cloudflare-new`);
      await page.getByRole('textbox', { name: 'Provider', exact: true }).fill('browser');
      await page
        .getByRole('textbox', { name: 'Model', exact: true })
        .fill('proxy-' + randomUUID().slice(0, 8));
      await toggle.check();
      await page.getByRole('button', { name: 'Add charity model', exact: true }).click();
      await expect(page).toHaveURL(/charity_model=[1-9]\d*/);
      const id = new URL(page.url()).searchParams.get('charity_model');
      const path = prefix + '/charity-models/' + id;
      let current = await read(context, origin, path);
      expect(current.cloudflare_proxy_enabled).toBe(true);
      expect(current.cloudflare_proxy_mode).toBe('worker');
      await expect(toggle).toBeChecked();
      await toggle.uncheck();
      const save = page.getByRole('button', {
        name: common.common.workflow.saveTarget.replace('{{id}}', id!),
        exact: true,
      });
      await save.click();
      await expect(save).toBeEnabled();
      await expect
        .poll(async () => (await read(context, origin, path)).cloudflare_proxy_enabled)
        .toBe(false);
      await page.reload();
      await expect(toggle).not.toBeChecked();
      current = await read(context, origin, path);
      await toggle.check();
      await patch(context, origin, path, current, false, {
        public_description: 'Changed in another session',
      });
      const conflict = page.waitForResponse(
        (response) =>
          response.url().endsWith(path) &&
          response.request().method() === 'PATCH' &&
          response.status() === 409,
      );
      await save.click();
      await conflict;
      await expect(toggle).toBeChecked();
      await expect(
        page.getByText(common.common.operations.charity.modelChanged, { exact: true }),
      ).toBeVisible();
      await visuals(page, info, `charity-${role}-cloudflare-conflict`);
      await page
        .getByRole('button', { name: common.common.operations.charity.reloadModel, exact: true })
        .click();
      await expect(toggle).not.toBeChecked();
      await toggle.check();
      await save.click();
      await expect
        .poll(async () => (await read(context, origin, path)).cloudflare_proxy_enabled)
        .toBe(true);
      await page.reload();
      await expect(toggle).toBeChecked();
    } finally {
      await context.close();
    }
  });
}

test('level 5 reads the saved setting and cannot change the global model policy', async ({
  browser,
}) => {
  const { context, origin, prefix } = await session(browser, 5);
  try {
    const id = fixture().management.charity_model_id;
    const path = prefix + '/charity-models/' + id;
    const current = await read(context, origin, path);
    const page = await context.newPage();
    await page.goto(origin + managementPath(5, { charity_model: id }));
    await expect(page.getByText(copy.label, { exact: true })).toBeVisible();
    await expect(page.getByRole('switch', { name: copy.label, exact: true })).toHaveCount(0);
    const denied = await context.request.patch(origin + path, {
      headers: { Origin: origin, 'Idempotency-Key': randomUUID() },
      data: {
        expected_revision: current.revision,
        cloudflare_proxy_enabled: !current.cloudflare_proxy_enabled,
      },
    });
    expect(denied.status()).toBe(403);
    expect((await read(context, origin, path)).cloudflare_proxy_enabled).toBe(
      current.cloudflare_proxy_enabled,
    );
  } finally {
    await context.close();
  }
});
