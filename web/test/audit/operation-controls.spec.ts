import { readFileSync } from 'node:fs';
import { randomUUID } from 'node:crypto';
import { expect, test, type Browser, type BrowserContext } from '@playwright/test';
import commonEn from '../../src/shared/i18n/common/en.json' with { type: 'json' };
import adminZh from '../../src/admin/i18n/zh.json' with { type: 'json' };

interface Fixture {
  admin_url: string;
  admin_cookie: { Name: string; Value: string };
}
function fixture(): Fixture {
  return JSON.parse(readFileSync(process.env.NONBIRI_AUDIT_BROWSER_STATE!, 'utf8')) as Fixture;
}
test.skip(process.env.NONBIRI_MANAGEMENT_BROWSER !== '1', 'Requires the management fixture.');
async function administrator(browser: Browser, narrow = false) {
  const state = fixture();
  const context = await browser.newContext({
    viewport: narrow ? { width: 390, height: 844 } : { width: 1280, height: 900 },
  });
  await context.addCookies([
    {
      name: state.admin_cookie.Name,
      value: state.admin_cookie.Value,
      domain: new URL(state.admin_url).hostname,
      path: '/admin',
      httpOnly: true,
      sameSite: 'Lax',
    },
  ]);
  await context.addInitScript(
    (language) => {
      localStorage.setItem('nb.lang', language);
      localStorage.setItem('nb.theme', 'light');
    },
    narrow ? 'zh' : 'en',
  );
  return context;
}
async function read(context: BrowserContext, path: string) {
  const origin = fixture().admin_url;
  const response = await context.request.get(origin + path, { headers: { Origin: origin } });
  expect(response.ok(), path + ': ' + response.status()).toBe(true);
  return response.json();
}

test('administrator loads inactivity execution records and opens the persisted configuration audit', async ({
  browser,
}, info) => {
  const context = await administrator(browser);
  try {
    const origin = fixture().admin_url;
    const policy = await read(context, '/admin/api/inactivity-policy');
    const preview = await context.request.post(origin + '/admin/api/inactivity-policy/preview', {
      headers: { Origin: origin },
      data: {
        expected_revision: policy.revision,
        policy: { enabled: policy.enabled, decay: policy.decay, protection: policy.protection },
        page_size: 100,
      },
    });
    expect(preview.ok()).toBe(true);
    const runs = await read(context, '/admin/api/inactivity-policy/runs');
    const audits = await read(context, '/admin/api/inactivity-policy/audits');
    const audit = audits.data.find((entry: { action: string }) => entry.action === 'preview');
    expect(audit).toBeDefined();
    const page = await context.newPage();
    await page.goto(origin + '/inactivity-policy');
    const loaded = page.waitForResponse((response) =>
      new URL(response.url()).pathname.endsWith('/inactivity-policy/runs'),
    );
    await page
      .locator('summary')
      .filter({ hasText: /^Execution records$/ })
      .click();
    expect((await loaded).ok()).toBe(true);
    const records = page.getByRole('table', { name: 'Execution records', exact: true });
    await expect(records.locator('tbody tr')).toHaveCount(runs.data.length);
    await page
      .locator('summary')
      .filter({ hasText: /^Configuration and preview audit$/ })
      .click();
    const entry = page
      .locator('details')
      .filter({
        has: page.locator(':scope > summary', {
          hasText: 'Preview · Revision ' + audit.policy_revision,
        }),
      })
      .first();
    await entry.locator('summary').click();
    await expect(entry.locator('pre')).toHaveText(JSON.stringify(audit.details, null, 2));
    await page.screenshot({
      path: info.outputPath('inactivity-records-audit-en-desktop.png'),
      fullPage: true,
    });
  } finally {
    await context.close();
  }
});

test('administrator saves selected image-model availability and reads the authoritative revision', async ({
  browser,
}, info) => {
  const context = await administrator(browser);
  try {
    const origin = fixture().admin_url;
    const prefix = '/admin/api/limited-activities/picture-book';
    const models = await read(context, prefix + '/models');
    const model = models.data.find(
      (entry: { configured: boolean; enabled: boolean }) => entry.configured && entry.enabled,
    );
    expect(model).toBeDefined();
    const original = await read(context, prefix + '/models/' + model.id);
    const page = await context.newPage();
    await page.goto(origin + '/limited-activities?activity=picture-book');
    await page.getByRole('tab', { name: 'Model catalog', exact: true }).click();
    await page
      .getByRole('combobox', { name: 'Choose a model to configure', exact: true })
      .selectOption(model.id);
    const availability = page.getByLabel('Make this model available', { exact: true });
    await expect(availability).toBeChecked();
    await availability.uncheck();
    await page.getByRole('checkbox', { name: model.upstream_model_id, exact: true }).check();
    const saved = page.waitForResponse(
      (response) =>
        response.url().endsWith('/models/batch') && response.request().method() === 'POST',
    );
    await page.getByRole('button', { name: 'Save selected models', exact: true }).click();
    const response = await saved;
    expect(response.ok()).toBe(true);
    const result = await response.json();
    expect(result.applied).toBe(true);
    expect(result.receipts).toHaveLength(1);
    expect(result.receipts[0].id).toBe(model.id);
    const persisted = await read(context, prefix + '/models/' + model.id);
    expect(persisted.enabled).toBe(false);
    expect(persisted.revision).not.toBe(original.revision);
    expect(persisted.price).toEqual(original.price);
    await page.reload();
    await page.getByRole('tab', { name: 'Model catalog', exact: true }).click();
    await page
      .getByRole('combobox', { name: 'Choose a model to configure', exact: true })
      .selectOption(model.id);
    await expect(availability).not.toBeChecked();
    await page.screenshot({
      path: info.outputPath('selected-model-unavailable-en-desktop.png'),
      fullPage: true,
    });
  } finally {
    await context.close();
  }
});

test('administrator filters open welfare pools and selects the returned pool for adjustment', async ({
  browser,
}, info) => {
  const context = await administrator(browser, true);
  try {
    const origin = fixture().admin_url;
    const copy = adminZh.admin.activities;
    const pools = await read(
      context,
      '/admin/api/pools?pool_type=welfare&state=open&page=1&page_size=20',
    );
    expect(pools.data.length).toBeGreaterThan(0);
    const page = await context.newPage();
    await page.goto(origin + '/activities');
    await page
      .getByRole('combobox', { name: copy.pools.filterType, exact: true })
      .selectOption('welfare');
    const filtered = page.waitForResponse((response) => {
      const url = new URL(response.url());
      return (
        url.pathname === '/admin/api/pools' &&
        url.searchParams.get('pool_type') === 'welfare' &&
        url.searchParams.get('state') === 'open'
      );
    });
    await page
      .getByRole('combobox', { name: copy.pools.filterState, exact: true })
      .selectOption('open');
    const returned = await (await filtered).json();
    expect(returned.data.map((entry: { id: string }) => entry.id)).toEqual(
      pools.data.map((entry: { id: string }) => entry.id),
    );
    const row = page
      .getByRole('row')
      .filter({ hasText: copy.states.poolType.welfare })
      .filter({ hasText: copy.states.pool.open });
    await row.getByRole('button', { name: copy.pools.select, exact: true }).click();
    await expect(
      page.getByRole('heading', {
        name: copy.adjustment.title + ' · ' + copy.states.poolType.welfare,
        exact: true,
      }),
    ).toBeVisible();
    await expect(page.getByLabel(copy.adjustment.amount, { exact: true })).toHaveValue('');
    await page.screenshot({
      path: info.outputPath('pool-filter-selection-zh-narrow.png'),
      fullPage: true,
    });
  } finally {
    await context.close();
  }
});

test('administrator confirms one automatic client-rule consequence and removes the saved rule', async ({
  browser,
}, info) => {
  const context = await administrator(browser);
  try {
    const origin = fixture().admin_url;
    const page = await context.newPage();
    const name = 'Synthetic bounded client rule ' + randomUUID().slice(0, 8);
    await page.goto(origin + '/abuse-audit');
    await page
      .getByRole('tablist', { name: 'Abuse audit', exact: true })
      .getByRole('tab', { name: 'Client rules', exact: true })
      .click();
    await page.getByRole('button', { name: 'New rule', exact: true }).click();
    await page.getByLabel('Rule name', { exact: true }).fill(name);
    await page
      .getByLabel('Match value', { exact: true })
      .fill('synthetic-no-dispatch-' + randomUUID());
    await page.getByRole('combobox', { name: 'Ban duration', exact: true }).selectOption('hours');
    await page.getByLabel('Automatic ban enabled', { exact: true }).check();
    await page.getByLabel('Ban duration (Hours)', { exact: true }).fill('2');
    await page.getByText('Risk classification and supporting evidence', { exact: true }).click();
    await page.getByRole('combobox', { name: 'Status', exact: true }).selectOption('confirmed');
    await page.getByRole('button', { name: 'Save', exact: true }).click();
    const dialog = page.getByRole('alertdialog', {
      name: commonEn.common.riskAudit.confirmAutoBan,
      exact: true,
    });
    await expect(page.getByRole('alertdialog')).toHaveCount(1);
    await expect(dialog).toContainText(name);
    await expect(dialog).toContainText(commonEn.common.riskAudit.autoBanHelp);
    await expect(dialog).toContainText('2 Hours');
    await dialog.getByRole('button', { name: 'Cancel', exact: true }).click();
    await expect(page.getByLabel('Rule name', { exact: true })).toHaveValue(name);
    await page.getByRole('button', { name: 'Save', exact: true }).click();
    const saved = page.waitForResponse(
      (response) =>
        new URL(response.url()).pathname === '/admin/api/abuse-audit/client-rules' &&
        response.request().method() === 'POST',
    );
    await dialog.getByRole('button', { name: 'Save', exact: true }).click();
    const response = await saved;
    expect(response.status()).toBe(201);
    const rule = await response.json();
    expect(rule.auto_ban).toEqual({ enabled: true, duration_seconds: 7200 });
    const rules = await read(context, '/admin/api/abuse-audit/client-rules?page=1&page_size=20');
    expect(rules.items.find((entry: { id: string }) => entry.id === rule.id)).toMatchObject({
      name,
      auto_ban: rule.auto_ban,
    });
    const card = page.getByRole('heading', { name, exact: true }).locator('..');
    const deleted = page.waitForResponse(
      (value) =>
        new URL(value.url()).pathname.endsWith('/client-rules/' + rule.id) &&
        value.request().method() === 'DELETE',
    );
    await card.getByRole('button', { name: 'Delete', exact: true }).click();
    expect((await deleted).ok()).toBe(true);
    await expect(page.getByRole('heading', { name, exact: true })).toHaveCount(0);
    const remaining = await read(
      context,
      '/admin/api/abuse-audit/client-rules?page=1&page_size=20',
    );
    expect(remaining.items.some((entry: { id: string }) => entry.id === rule.id)).toBe(false);
    await page.screenshot({
      path: info.outputPath('automatic-rule-deleted-en-desktop.png'),
      fullPage: true,
    });
  } finally {
    await context.close();
  }
});
