import { randomUUID } from 'node:crypto';
import { readFileSync } from 'node:fs';
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
import type { CharityModel } from '../../src/shared/operations/charity';
import type { CharityPriceTemplate } from '../../src/shared/operations/charityPriceTemplates';
import type { RecurringDiscount } from '../../src/shared/operations/recurringDiscount';

type Cookie = { Name: string; Value: string };
interface Fixture {
  admin_url: string;
  user_url: string;
  admin_cookie: Cookie;
  users: { level: number; cookie: Cookie }[];
  management: {
    recurring_discount: { request_model_id: string; token_model_id: string; template_id: string };
  };
}
const copy = common.common.recurringDiscount;
const workflow = common.common.workflow;
const initial: RecurringDiscount = {
  enabled: true,
  time_zone: 'UTC',
  default_percent: 50,
  periods: [],
};
const zero: RecurringDiscount = {
  enabled: false,
  time_zone: 'UTC',
  default_percent: 0,
  periods: [],
};
function fixture(): Fixture {
  return JSON.parse(readFileSync(process.env.NONBIRI_AUDIT_BROWSER_STATE!, 'utf8')) as Fixture;
}
test.skip(
  process.env.NONBIRI_MANAGEMENT_BROWSER !== '1',
  'Requires the isolated management fixture.',
);

async function session(browser: Browser, role: 'admin' | 1 | 5 | 6) {
  const state = fixture(),
    origin = role === 'admin' ? state.admin_url : state.user_url;
  // Ordinary callers cannot use the donation owner's own keys.
  const cookie =
    role === 'admin'
      ? state.admin_cookie
      : role === 1
        ? state.users.filter((user) => user.level === 1)[1]!.cookie
        : state.users.find((user) => user.level === role)!.cookie;
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
  return context;
}
async function read<T>(context: BrowserContext, origin: string, path: string): Promise<T> {
  const response = await context.request.get(origin + path, { headers: { Origin: origin } });
  expect(response.ok(), `${path}: ${response.status()}`).toBe(true);
  return response.json() as Promise<T>;
}
async function patchModel(context: BrowserContext, id: string, fields: Record<string, unknown>) {
  const origin = fixture().admin_url,
    path = '/admin/api/charity-models/' + id;
  const before = await read<CharityModel>(context, origin, path);
  const response = await context.request.patch(origin + path, {
    headers: { Origin: origin, 'Idempotency-Key': randomUUID() },
    data: { expected_revision: before.revision, ...fields },
  });
  expect(response.status()).toBe(200);
  return response.json() as Promise<CharityModel>;
}
async function save(
  page: Page,
  button: Locator,
  method: 'PATCH' | 'PUT',
  path: string,
  status = 200,
) {
  const pending = page.waitForResponse(
    (response) =>
      new URL(response.url()).pathname === path && response.request().method() === method,
  );
  await button.click();
  const response = await pending;
  expect(response.status()).toBe(status);
  return response;
}
async function visuals(page: Page, info: TestInfo, name: string, focus: Locator) {
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
      for (const control of await focus.locator('input:not([type=checkbox])').all()) {
        const box = await control.boundingBox();
        if (box) {
          expect(box.x).toBeGreaterThanOrEqual(0);
          expect(box.x + box.width).toBeLessThanOrEqual(width + 1);
        }
      }
      for (const label of await focus.locator('label:has(input[type=checkbox])').all()) {
        const box = await label.boundingBox();
        expect(box?.height).toBeGreaterThanOrEqual(40);
        expect(box?.width).toBeGreaterThanOrEqual(40);
      }
      for (const row of await focus.locator('.charity-price-table tr:has(td)').all()) {
        const label = await row.locator('th').boundingBox();
        const amount = await row.locator('td').first().boundingBox();
        expect(label).not.toBeNull();
        expect(amount).not.toBeNull();
        expect(
          label!.x + label!.width <= amount!.x + 1 || label!.y + label!.height <= amount!.y + 1,
        ).toBe(true);
      }
      for (const note of await focus.locator('.charity-context-note').all()) {
        const heading = await note.locator('strong').boundingBox();
        const text = await note.locator('span').boundingBox();
        expect(heading).not.toBeNull();
        expect(text).not.toBeNull();
        expect(text!.y).toBeGreaterThanOrEqual(heading!.y + heading!.height);
      }
      await page.screenshot({
        path: process.env.NONBIRI_VISUAL_DIR
          ? resolve(process.env.NONBIRI_VISUAL_DIR, `${name}-${theme}-${width}.png`)
          : info.outputPath(`${name}-${theme}-${width}.png`),
        animations: 'disabled',
      });
    }
}
function openModel(page: Page, role: 'admin' | 5 | 6, id: string) {
  const state = fixture();
  return page.goto(
    (role === 'admin' ? state.admin_url + '/charity?' : state.user_url + '/steward?tab=charity&') +
      'charity_section=models&charity_model=' +
      id,
  );
}
function period(editor: Locator, number: number) {
  return editor.getByRole('group', {
    name: copy.period.replace('{{number}}', String(number)),
    exact: true,
  });
}

test('administrator saves timezone, overnight and full-day periods through normal controls', async ({
  browser,
}, info) => {
  const state = fixture(),
    data = state.management.recurring_discount,
    context = await session(browser, 'admin');
  try {
    await patchModel(context, data.request_model_id, {
      recurring_discount: initial,
      discount: { enabled: false, percent: 80, start_at: null, end_at: null },
    });
    const page = await context.newPage(),
      errors: string[] = [];
    page.on('pageerror', (error) => errors.push(error.message));
    await openModel(page, 'admin', data.request_model_id);
    const editor = page.locator('.nb-recurring-discount');
    await editor.getByLabel(copy.timeZone, { exact: true }).fill('Asia/Shanghai');
    await editor.getByRole('button', { name: copy.add, exact: true }).click();
    const first = period(editor, 1);
    await first.getByLabel(copy.start, { exact: true }).fill('22:00');
    await first.getByLabel(copy.end, { exact: true }).fill('02:00');
    await first.getByLabel(copy.percent, { exact: true }).fill('0');
    await expect(first.getByText(copy.overnight, { exact: true })).toBeVisible();
    await first.getByRole('checkbox', { name: copy.allDay, exact: true }).check();
    await expect(first.getByRole('checkbox', { name: copy.endOfDay, exact: true })).toBeChecked();
    await first.getByRole('checkbox', { name: copy.allDay, exact: true }).uncheck();
    await expect(first.getByLabel(copy.start, { exact: true })).toHaveValue('22:00');
    await expect(first.getByLabel(copy.end, { exact: true })).toHaveValue('02:00');
    await editor.getByRole('button', { name: copy.add, exact: true }).click();
    const second = period(editor, 2);
    for (const day of [copy.monday, copy.tuesday, copy.wednesday, copy.thursday, copy.friday])
      await second.getByRole('checkbox', { name: day, exact: true }).uncheck();
    await second.getByRole('checkbox', { name: copy.sunday, exact: true }).check();
    await second.getByRole('checkbox', { name: copy.allDay, exact: true }).check();
    const response = await save(
      page,
      page.getByRole('button', {
        name: workflow.saveTarget.replace('{{id}}', data.request_model_id),
        exact: true,
      }),
      'PATCH',
      '/admin/api/charity-models/' + data.request_model_id,
    );
    const result = (await response.json()) as CharityModel;
    expect(result.recurring_discount).toEqual({
      enabled: true,
      time_zone: 'Asia/Shanghai',
      default_percent: 50,
      periods: [
        { weekdays: [1, 2, 3, 4, 5], start_time: '22:00', end_time: '02:00', percent: 0 },
        { weekdays: [7], start_time: '00:00', end_time: '24:00', percent: 100 },
      ],
    });
    expect(result.effective_discount.source).toBe('recurring');
    expect(result.server_now).toBeGreaterThan(0);
    await visuals(page, info, 'recurring-discount-period-editor', first);
    expect(errors).toEqual([]);
  } finally {
    await context.close();
  }
});

test('complete templates keep false, zero and empty periods through conflict recovery and level 6 application', async ({
  browser,
}, info) => {
  const state = fixture(),
    data = state.management.recurring_discount,
    admin = await session(browser, 'admin');
  const steward = await session(browser, 6),
    trainee = await session(browser, 5);
  try {
    const path = '/admin/api/charity-price-templates/' + data.template_id;
    const page = await admin.newPage();
    await page.goto(
      state.admin_url + '/charity?charity_section=templates&price_template=' + data.template_id,
    );
    const editor = page.locator('.nb-recurring-discount');
    await editor.getByRole('switch', { name: copy.enabled, exact: true }).uncheck();
    await editor.getByLabel(copy.defaultPercent, { exact: true }).fill('0');
    const before = await read<CharityPriceTemplate>(admin, state.admin_url, path);
    const concurrent = await admin.request.put(state.admin_url + path, {
      headers: { Origin: state.admin_url, 'Idempotency-Key': randomUUID() },
      data: {
        expected_revision: before.revision,
        name: before.name,
        description: before.description,
        enabled: before.enabled,
        pricing: before.pricing,
        token_reserve_credits: before.token_reserve_credits,
        discount: before.discount,
        recurring_discount: { ...initial, default_percent: 75 },
      },
    });
    expect(concurrent.status()).toBe(200);
    const button = page.getByRole('button', {
      name: common.common.templateManagement.save.replace('{{name}}', before.name),
      exact: true,
    });
    await save(page, button, 'PUT', path, 409);
    await expect(editor.getByLabel(copy.defaultPercent, { exact: true })).toHaveValue('0');
    await expect(editor.getByRole('switch', { name: copy.enabled, exact: true })).not.toBeChecked();
    await page
      .getByRole('button', { name: common.common.templateManagement.loadLatest, exact: true })
      .click();
    await expect(editor.getByLabel(copy.defaultPercent, { exact: true })).toHaveValue('0');
    const saved = await save(page, button, 'PUT', path);
    expect(saved.request().postDataJSON().recurring_discount).toEqual(zero);
    const template = (await saved.json()) as CharityPriceTemplate;
    expect(template.recurring_discount).toEqual(zero);
    const modelBefore = await read<CharityModel>(
      admin,
      state.admin_url,
      '/admin/api/charity-models/' + data.request_model_id,
    );
    const level6 = await steward.newPage();
    await openModel(level6, 6, data.request_model_id);
    await expect(level6.locator('.nb-recurring-discount datalist option[value="UTC"]')).toHaveCount(
      1,
    );
    await level6.getByRole('button', { name: workflow.openPriceReference, exact: true }).click();
    const templates = level6
      .locator('summary')
      .filter({ has: level6.getByText(workflow.templates, { exact: true }) });
    await templates.click();
    const tools = level6.getByRole('region', { name: workflow.priceReferencePanel, exact: true });
    await tools.getByPlaceholder(workflow.templateSearch).fill(template.name);
    await tools.getByPlaceholder(workflow.templateSearch).press('Enter');
    await tools
      .getByRole('row')
      .filter({ hasText: template.name })
      .getByRole('button', { name: workflow.viewPrice, exact: true })
      .click();
    await tools.getByRole('button', { name: workflow.fillTemplate, exact: true }).click();
    await level6.getByRole('button', { name: workflow.returnPriceDraft, exact: true }).click();
    const applied = await save(
      level6,
      level6.getByRole('button', {
        name: workflow.saveTarget.replace('{{id}}', data.request_model_id),
        exact: true,
      }),
      'PATCH',
      '/api/steward/charity-models/' + data.request_model_id,
    );
    const model = (await applied.json()) as CharityModel;
    for (const field of [
      'pricing',
      'token_reserve_credits',
      'discount',
      'recurring_discount',
    ] as const)
      expect(model[field]).toEqual(template[field]);
    expect(model.provider).toBe(modelBefore.provider);
    expect(model.model).toBe(modelBefore.model);
    expect(model.recurring_discount).toEqual(zero);
    const recurrence = level6.locator('.nb-recurring-discount');
    await expect(recurrence.getByLabel(copy.defaultPercent, { exact: true })).toHaveValue('0');
    await visuals(level6, info, 'recurring-discount-template-applied', recurrence);
    const newTemplate = await admin.request.put(state.admin_url + path, {
      headers: { Origin: state.admin_url, 'Idempotency-Key': randomUUID() },
      data: {
        expected_revision: template.revision,
        name: template.name,
        description: template.description,
        enabled: template.enabled,
        pricing: template.pricing,
        token_reserve_credits: template.token_reserve_credits,
        discount: template.discount,
        recurring_discount: initial,
      },
    });
    expect(newTemplate.status()).toBe(200);
    expect(
      (
        await read<CharityModel>(
          admin,
          state.admin_url,
          '/admin/api/charity-models/' + data.request_model_id,
        )
      ).recurring_discount,
    ).toEqual(zero);
    const level5 = await trainee.newPage();
    await openModel(level5, 5, data.request_model_id);
    await expect(
      level5.getByText('[公益]recurring-browser/request', { exact: true }).first(),
    ).toBeVisible();
    await expect(level5.locator('.nb-recurring-discount')).toHaveCount(0);
    const denied = await trainee.request.patch(
      state.user_url + '/api/steward/charity-models/' + data.request_model_id,
      {
        headers: { Origin: state.user_url, 'Idempotency-Key': randomUUID() },
        data: { expected_revision: model.revision, recurring_discount: initial },
      },
    );
    expect(denied.status()).toBe(403);
  } finally {
    await admin.close();
    await steward.close();
    await trainee.close();
  }
});

test('catalog uses actual request, token and long-context prices, pauses recurring for fixed windows and refreshes a boundary', async ({
  browser,
}, info) => {
  const state = fixture(),
    data = state.management.recurring_discount,
    admin = await session(browser, 'admin'),
    owner = await session(browser, 1);
  try {
    const fixedOff = { enabled: false, percent: 80, start_at: null, end_at: null };
    for (const id of [data.request_model_id, data.token_model_id])
      await patchModel(admin, id, { recurring_discount: initial, discount: fixedOff });
    const path =
      '/api/charity/models?view=catalog&page=1&page_size=20&q=recurring-browser&allowed_for_me=true&currently_available=true';
    const projected = await read<{ models: Record<string, unknown>[] }>(
      owner,
      state.user_url,
      path,
    );
    const request = projected.models.find((model) => model.id === data.request_model_id)!;
    const token = projected.models.find((model) => model.id === data.token_model_id)!;
    expect(request).toMatchObject({
      effective_discount: { source: 'recurring', percent: 50, next_change_at: null },
      pricing: {
        user_price_milli: '3000',
        discounted_user_price_milli: '2400',
        current_user_price_milli: '1500',
        current_user_prices_milli: null,
        long_context: { current_user_price_milli: '3500' },
      },
    });
    expect(token).toMatchObject({
      pricing: {
        current_user_price_milli: null,
        current_user_prices_milli: {
          uncached_input: '500',
          cache_write_input: '1000',
          cache_read_input: '1500',
          output: '2000',
        },
        long_context: {
          current_user_prices_milli: {
            uncached_input: '1000',
            cache_write_input: '2000',
            cache_read_input: '3000',
            output: '4000',
          },
        },
      },
    });
    expect(JSON.stringify(projected)).not.toContain('donor_reward');
    const page = await owner.newPage();
    await page.goto(state.user_url + '/charity?tab=models&q=recurring-browser');
    await page.getByRole('button', { name: '[公益]recurring-browser/tokens', exact: true }).click();
    const detail = page.getByRole('region', {
      name: '[公益]recurring-browser/tokens',
      exact: true,
    });
    for (const amount of ['0.5', '1.5', '4'])
      await expect(
        detail.getByLabel('Current price: ' + amount, { exact: true }).first(),
      ).toBeVisible();
    await expect(
      detail.getByText('Extra 5 credits per request, without a discount', { exact: true }),
    ).toBeVisible();
    await visuals(page, info, 'recurring-discount-catalog-prices', detail);
    const now = (
      await read<CharityModel>(
        admin,
        state.admin_url,
        '/admin/api/charity-models/' + data.request_model_id,
      )
    ).server_now;
    await patchModel(admin, data.request_model_id, {
      discount: { enabled: true, percent: 80, start_at: now + 3600, end_at: now + 7200 },
    });
    await page.reload();
    await page
      .getByRole('button', { name: '[公益]recurring-browser/request', exact: true })
      .click();
    const current = page.getByRole('region', {
      name: '[公益]recurring-browser/request',
      exact: true,
    });
    await expect(current.getByLabel('Current price: 3', { exact: true })).toBeVisible();
    await expect(current.getByText(copy.paused, { exact: false })).toBeVisible();
    const expired = await patchModel(admin, data.request_model_id, {
      discount: { enabled: true, percent: 80, start_at: now - 3600, end_at: now - 1 },
    });
    expect(expired.effective_discount).toEqual({
      source: 'fixed',
      percent: 100,
      next_change_at: null,
    });
    const latest = await read<CharityModel>(
      admin,
      state.admin_url,
      '/admin/api/charity-models/' + data.request_model_id,
    );
    const scheduled = await patchModel(admin, data.request_model_id, {
      discount: {
        enabled: true,
        percent: 80,
        start_at: latest.server_now + 6,
        end_at: latest.server_now + 120,
      },
    });
    expect(scheduled.effective_discount.percent).toBe(100);
    await page.reload();
    await page
      .getByRole('button', { name: '[公益]recurring-browser/request', exact: true })
      .click();
    await expect(current.getByLabel('Current price: 3', { exact: true })).toBeVisible();
    await expect(current.getByLabel('Current price: 2.4', { exact: true })).toBeVisible({
      timeout: 15_000,
    });
    const free = await patchModel(admin, data.request_model_id, {
      discount: fixedOff,
      recurring_discount: { ...initial, default_percent: 0 },
    });
    expect(free.effective_discount).toEqual({
      source: 'recurring',
      percent: 0,
      next_change_at: null,
    });
    await page.reload();
    await page
      .getByRole('button', { name: '[公益]recurring-browser/request', exact: true })
      .click();
    await expect(current.getByLabel('Current price: 0', { exact: true }).first()).toBeVisible();
  } finally {
    await admin.close();
    await owner.close();
  }
});
