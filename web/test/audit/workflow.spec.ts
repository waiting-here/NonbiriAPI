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
import adminEn from '../../src/admin/i18n/en.json' with { type: 'json' };
import en from '../../src/user/i18n/en.json' with { type: 'json' };

type Cookie = { Name: string; Value: string };
interface Fixture {
  admin_url: string;
  user_url: string;
  admin_cookie: Cookie;
  users: { id: string; level: number; cookie: Cookie }[];
  management: {
    workflow: {
      owner: { id: string; cookie: Cookie };
      models: string[];
      templates: string[];
      pending_donation_id: string;
      pending_key_id: string;
      processing_donation_id: string;
      ended_donation_id: string;
      header_request_id: string;
      shortcut_request_id: string;
      shortcut_model_id: string;
      shortcut_attempt_targets: { donation_id: string; donation_key_id: string }[];
    };
  };
}
const workflow = common.common.workflow;
const batch = common.common.bindingBatch;
const operations = common.common.operations.charity;
const pages = common.common.pageControls;
const history = en.user.charity.ownerHistory;
function fixture(): Fixture {
  return JSON.parse(readFileSync(process.env.NONBIRI_AUDIT_BROWSER_STATE!, 'utf8')) as Fixture;
}
test.skip(
  process.env.NONBIRI_WORKFLOW_BROWSER !== '1',
  'Requires the isolated large workflow fixture.',
);

async function session(browser: Browser, role: 'admin' | 'owner' | 5 | 6) {
  const state = fixture();
  const origin = role === 'admin' ? state.admin_url : state.user_url;
  const cookie =
    role === 'admin'
      ? state.admin_cookie
      : role === 'owner'
        ? state.management.workflow.owner.cookie
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
async function read(context: BrowserContext, origin: string, path: string) {
  const response = await context.request.get(origin + path, { headers: { Origin: origin } });
  expect(response.ok(), path + ': ' + response.status()).toBe(true);
  return response.json();
}
async function visuals(page: Page, info: TestInfo, name: string, focus: () => Promise<void>) {
  for (const theme of ['light', 'dark']) {
    await page.evaluate((value) => {
      document.documentElement.dataset.theme = value;
    }, theme);
    for (const width of [1440, 390]) {
      await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
      await focus();
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
        true,
      );
      const path = process.env.NONBIRI_VISUAL_DIR
        ? resolve(process.env.NONBIRI_VISUAL_DIR, `${name}-${theme}-${width}.png`)
        : info.outputPath(`${name}-${theme}-${width}.png`);
      await page.screenshot({ path, animations: 'disabled' });
    }
  }
}

test('charity request and attempt shortcuts preserve the log window for administrators and level 5', async ({
  browser,
}, info) => {
  const state = fixture(),
    data = state.management.workflow;
  for (const role of ['admin', 5] as const) {
    const context = await session(browser, role);
    try {
      const origin = role === 'admin' ? state.admin_url : state.user_url;
      const prefix = role === 'admin' ? '/admin/api' : '/api/steward';
      const page = await context.newPage();
      const model = await read(
        context,
        origin,
        `${prefix}/charity-models/${data.shortcut_model_id}`,
      );
      const detail = await read(
        context,
        origin,
        `${prefix}/logs/${data.shortcut_request_id}?attempt_page=1&attempt_page_size=20`,
      );
      expect(detail.request.charity_model_id).toBe(data.shortcut_model_id);
      expect(
        detail.attempts.data.map((attempt: { donation_id: string; donation_key_id: string }) => ({
          donation_id: attempt.donation_id,
          donation_key_id: attempt.donation_key_id,
        })),
      ).toEqual(data.shortcut_attempt_targets);
      const search = new URLSearchParams({
        ...(role === 'admin' ? {} : { tab: 'logs' }),
        charity_model: model.full_name,
        page: '1',
        page_size: '10',
        from: '',
        to: '',
        request_id: data.shortcut_request_id,
        attempt_page: '1',
        attempt_page_size: '20',
      });
      await page.goto(origin + (role === 'admin' ? '/logs?' : '/steward?') + search);
      const panel = page.locator('.nb-expandable-panel:not([hidden])');
      await expect(
        panel.getByRole('link', { name: common.common.operations.logs.manageCharityModel }),
      ).toBeVisible();
      const original = page.url();
      await panel
        .getByRole('link', { name: common.common.operations.logs.manageCharityModel })
        .click();
      await expect(page).toHaveURL(new RegExp(`charity_model=${data.shortcut_model_id}`));
      await expect(
        page.getByRole('heading', { name: workflow.editModel, exact: true }),
      ).toBeVisible();
      await page
        .getByRole('link', { name: common.common.operations.logs.returnToLogs, exact: true })
        .click();
      await expect(page).toHaveURL(original);
      for (let index = 0; index < data.shortcut_attempt_targets.length; index++) {
        const toggle = panel
          .locator('summary')
          .filter({ hasText: common.common.operations.logs.attempts });
        if ((await toggle.locator('..').getAttribute('open')) === null) await toggle.click();
        const link = panel
          .getByRole('link', { name: common.common.operations.logs.manageCharityKey })
          .nth(index);
        const target = data.shortcut_attempt_targets[index];
        await expect(link).toHaveAttribute(
          'href',
          new RegExp(
            `managed_donation=${target.donation_id}&managed_key=${target.donation_key_id}$`,
          ),
        );
        const scopeRead =
          role === 5
            ? page.waitForResponse(
                (response) =>
                  new URL(response.url()).pathname ===
                  `/api/steward/donations/${target.donation_id}/keys/${target.donation_key_id}`,
              )
            : undefined;
        await link.click();
        await expect(
          page.getByRole('heading', { name: `Manage key #${target.donation_key_id}`, exact: true }),
        ).toBeVisible();
        await expect(
          page.getByLabel(common.common.keyLimits.concurrency, { exact: true }),
        ).toBeVisible();
        if (scopeRead)
          expect(new URL((await scopeRead).url()).searchParams.get('charity_model_id')).toBe(
            data.shortcut_model_id,
          );
        if (role === 'admin' && index === 0)
          await visuals(page, info, 'workflow-log-key-shortcut', () =>
            page
              .getByRole('button', {
                name: common.common.operations.logs.returnToLogs,
                exact: true,
              })
              .scrollIntoViewIfNeeded(),
          );
        await page
          .getByRole('button', { name: common.common.operations.logs.returnToLogs, exact: true })
          .click();
        await expect(page).toHaveURL(original);
      }
    } finally {
      await context.close();
    }
  }
});

test('approve a fixed key, page 240 targets and bind 144 cross-page selections with a compact receipt', async ({
  browser,
}, info) => {
  const state = fixture(),
    data = state.management.workflow;
  const context = await session(browser, 'admin');
  try {
    const page = await context.newPage();
    const errors: string[] = [];
    page.on('pageerror', (error) => errors.push(error.message));
    await page.goto(
      state.admin_url +
        `/charity?q=Workflow&donations_page=2&donation_id=${data.pending_donation_id}`,
    );
    await page.getByLabel(operations.reason, { exact: true }).fill('Synthetic workflow approval');
    await page.getByLabel(operations.enableForCharity, { exact: true }).check();
    await page.getByLabel(operations.confirmReview, { exact: true }).check();
    await page.getByRole('button', { name: 'Approve donation', exact: true }).click();
    await expect(
      page.getByRole('button', { name: workflow.bindThisKey, exact: true }),
    ).toBeVisible();
    expect(
      (await read(context, state.admin_url, '/admin/api/donations/' + data.pending_donation_id))
        .status,
    ).toBe('approved');
    await page.getByRole('button', { name: workflow.bindThisKey, exact: true }).click();
    const search = page.getByPlaceholder(workflow.modelSearch);
    await search.fill('workflow');
    await search.press('Enter');
    const pager = page.getByRole('navigation', { name: common.common.pagination }).last();
    await expect(pager).toContainText('240');
    await expect(pager.getByLabel(pages.size)).toHaveValue('20');
    await pager.getByLabel(pages.size).selectOption('10');
    await expect(pager.getByLabel(pages.size)).toHaveValue('10');
    await pager.getByLabel(pages.jump).fill('24');
    await pager.getByLabel(pages.jump).press('Enter');
    await expect(page.getByRole('table', { name: workflow.chooseTarget })).toContainText(
      'model-240',
    );
    await pager.getByLabel(pages.jump).fill('1');
    await pager.getByLabel(pages.jump).press('Enter');
    await page
      .getByRole('row')
      .filter({ hasText: '[公益]workflow/model-001' })
      .getByRole('button', { name: batch.chooseUpstream, exact: true })
      .click();
    const choices = page.getByRole('region', { name: operations.chooseModels, exact: true });
    await expect(choices.getByRole('checkbox')).toHaveCount(20);
    const first = choices.getByRole('checkbox').first();
    await first.focus();
    await page.keyboard.press('Space');
    await expect(first).toBeChecked();
    await page.getByRole('button', { name: 'Select this page (20)', exact: true }).click();
    const candidatePager = page.locator('.ops-binding-picker .nb-pager').first();
    await candidatePager.getByLabel(pages.size).selectOption('100');
    await expect(choices.getByRole('checkbox')).toHaveCount(100);
    await page.getByRole('button', { name: 'Select this page (100)', exact: true }).click();
    await candidatePager.getByRole('button', { name: common.common.next, exact: true }).click();
    await expect(choices.getByRole('checkbox')).toHaveCount(44);
    await page.getByRole('button', { name: 'Select this page (44)', exact: true }).click();
    await expect(
      page.getByRole('button', { name: 'Add 144 connections', exact: true }),
    ).toBeEnabled();
    await expect(
      page.getByText(
        batch.selectionCapacity.replace('{{selected}}', '144').replace('{{capacity}}', '256'),
        { exact: true },
      ),
    ).toBeVisible();
    const idSearch = page.getByPlaceholder(workflow.upstreamSearch).first();
    await idSearch.fill('model-144');
    await idSearch.press('Enter');
    await expect(choices.getByRole('checkbox')).toHaveCount(1);
    await expect(choices.getByRole('checkbox')).toBeChecked();
    await page.getByLabel(batch.searchSelected, { exact: true }).fill('model-144');
    await page
      .getByRole('button', {
        name: workflow.removeSelection.replace(
          '{{id}}',
          '[公益]workflow/model-001: provider/model-144-2026-10',
        ),
        exact: true,
      })
      .click();
    await expect(
      page.getByRole('button', { name: 'Add 143 connections', exact: true }),
    ).toBeEnabled();
    await expect(page.getByLabel(batch.searchSelected, { exact: true })).toBeFocused();
    await choices.getByRole('checkbox').check();
    await idSearch.fill('');
    await idSearch.press('Enter');
    await page.getByLabel(batch.searchSelected, { exact: true }).fill('');
    await expect(choices.getByRole('checkbox')).toHaveCount(100);
    await expect(
      page.getByRole('button', { name: 'Add 144 connections', exact: true }),
    ).toBeEnabled();
    await visuals(page, info, 'workflow-binding', () =>
      candidatePager.evaluate((node) => node.scrollIntoView({ block: 'center' })),
    );
    const receipt = page.waitForResponse(
      (response) =>
        response.request().method() === 'POST' &&
        new URL(response.url()).pathname ===
          `/admin/api/donations/${data.pending_donation_id}/keys/${data.pending_key_id}/bindings/batch`,
    );
    await page.getByRole('button', { name: 'Add 144 connections', exact: true }).click();
    const response = await receipt;
    expect(response.status()).toBe(200);
    expect(await response.json()).toEqual({
      items: [
        {
          donation_key_id: data.pending_key_id,
          results: Array.from({ length: 144 }, () => 'added'),
        },
      ],
      models: [{ charity_model_id: data.models[0], binding_revision: '1' }],
      added_count: '144',
    });
    await expect(page.getByRole('region', { name: batch.results })).toContainText(
      batch.resultSummary.replace('{{count}}', '144'),
    );
    const saved = await read(
      context,
      state.admin_url,
      `/admin/api/charity-models/${data.models[0]}/bindings`,
    );
    expect(saved.bindings).toHaveLength(144);
    expect(
      new Set(saved.bindings.map((entry: { upstream_model_id: string }) => entry.upstream_model_id))
        .size,
    ).toBe(144);
    await expect(choices.getByRole('checkbox')).toHaveCount(100);
    await expect(choices.locator('input[type=checkbox]:enabled')).toHaveCount(0);
    await expect(choices.getByText(batch.boundHere, { exact: true })).toHaveCount(100);
    await page.getByRole('button', { name: workflow.returnDonation, exact: true }).click();
    await page.getByRole('button', { name: operations.returnToList, exact: true }).first().click();
    await expect(page).toHaveURL(/donations_page=2/);
    expect(errors).toEqual([]);
  } finally {
    await context.close();
  }
});

test('create with a carried key returns to its fixed binding target and preserves the donation queue page', async ({
  browser,
}) => {
  const state = fixture(),
    data = state.management.workflow;
  const context = await session(browser, 'admin');
  try {
    const page = await context.newPage();
    await page.goto(
      state.admin_url +
        `/charity?q=Workflow&donations_page=2&donation_id=${data.pending_donation_id}&binding_key=${data.pending_key_id}`,
    );
    await page.getByRole('button', { name: workflow.createThenBind, exact: true }).click();
    await expect(page.getByRole('heading', { name: workflow.newModel, exact: true })).toBeVisible();
    await expect(page.getByLabel('Provider', { exact: true })).toHaveValue('');
    await page.getByLabel('Provider', { exact: true }).fill('workflow');
    await page.getByLabel('Model', { exact: true }).fill('carried-key-model');
    const created = page.waitForResponse(
      (response) =>
        response.request().method() === 'POST' &&
        new URL(response.url()).pathname === '/admin/api/charity-models',
    );
    await page.getByRole('button', { name: 'Add charity model', exact: true }).click();
    const result = await created;
    expect(result.status()).toBe(201);
    const model = await result.json();
    await expect(
      page.getByRole('heading', { name: '[公益]workflow/carried-key-model', exact: true }),
    ).toBeVisible();
    await expect(page).toHaveURL(new RegExp(`binding_key=${data.pending_key_id}`));
    await page
      .getByRole('region', { name: operations.chooseModels, exact: true })
      .getByRole('checkbox')
      .first()
      .check();
    await page.getByRole('button', { name: 'Add 1 connections', exact: true }).click();
    await expect
      .poll(
        async () =>
          (await read(context, state.admin_url, `/admin/api/charity-models/${model.id}/bindings`))
            .bindings.length,
      )
      .toBe(1);
    await page.getByRole('button', { name: workflow.returnDonation, exact: true }).click();
    await page.getByRole('button', { name: operations.returnToList, exact: true }).first().click();
    await expect(page).toHaveURL(/donations_page=2/);
  } finally {
    await context.close();
  }
});

test('edit keeps its identity through complete template and model-reference fills, undo, save and new draft', async ({
  browser,
}, info) => {
  const state = fixture(),
    data = state.management.workflow;
  const context = await session(browser, 'admin');
  try {
    const page = await context.newPage();
    const root = `/admin/api/charity-models/${data.models[1]}`;
    const before = await read(context, state.admin_url, root);
    await page.goto(
      state.admin_url + `/charity?charity_section=models&charity_model=${data.models[1]}`,
    );
    const provider = page.getByLabel('Provider', { exact: true });
    const model = page.getByLabel('Model', { exact: true });
    await expect(provider).toHaveValue('workflow');
    await expect(model).toHaveValue('model-002');
    await page.getByRole('button', { name: workflow.openPriceReference, exact: true }).click();
    await page
      .locator('summary')
      .filter({ has: page.getByText(workflow.templates, { exact: true }) })
      .click();
    await page
      .getByRole('row')
      .filter({ hasText: 'Workflow token complete' })
      .getByRole('button', { name: workflow.viewPrice, exact: true })
      .click();
    await page.getByRole('button', { name: workflow.fillTemplate, exact: true }).click();
    await expect(page.getByRole('combobox', { name: 'Pricing mode', exact: true })).toHaveValue(
      'per_token',
    );
    for (const [label, value] of [
      ['Uncached input user price', '20'],
      ['Cache-write input user price', '24'],
      ['Cache-read input user price', '2'],
      ['Output user price', '60'],
      ['Uncached input donor reward', '10'],
      ['Cache-write input donor reward', '12'],
      ['Cache-read input donor reward', '1'],
      ['Output donor reward', '30'],
    ]) {
      await expect(page.getByLabel(label, { exact: true })).toHaveValue(value);
    }
    await page
      .locator('summary')
      .filter({ has: page.getByText(operations.quotaReserve, { exact: true }) })
      .click();
    await expect(page.getByLabel(operations.tokenReserveCredits, { exact: true })).toHaveValue(
      '25.125',
    );
    await page.getByRole('button', { name: workflow.undoPrice, exact: true }).click();
    await expect(page.getByRole('combobox', { name: 'Pricing mode', exact: true })).toHaveValue(
      'per_request',
    );
    await expect(provider).toHaveValue('workflow');
    await page.getByRole('button', { name: workflow.openPriceReference, exact: true }).click();
    await page
      .locator('summary')
      .filter({ has: page.getByText(workflow.reference, { exact: true }) })
      .click();
    const reference = page.getByRole('region', { name: workflow.priceReferencePanel, exact: true });
    await reference.getByPlaceholder(workflow.modelSearch).fill('workflow/model-240');
    await reference.getByPlaceholder(workflow.modelSearch).press('Enter');
    await reference
      .getByRole('table', { name: workflow.reference })
      .getByRole('button', { name: workflow.viewPrice, exact: true })
      .click();
    await expect(
      reference.getByRole('region', { name: workflow.readOnlyPrice }).last(),
    ).toContainText('25.125');
    await visuals(page, info, 'workflow-price-reference', () => reference.scrollIntoViewIfNeeded());
    await reference.getByRole('button', { name: workflow.fillReference, exact: true }).click();
    await expect(model).toHaveValue('model-002');
    const tokenSave = page.waitForResponse(
      (response) =>
        response.request().method() === 'PATCH' && new URL(response.url()).pathname === root,
    );
    await page.getByRole('button', { name: `Save model #${data.models[1]}`, exact: true }).click();
    expect((await tokenSave).ok()).toBe(true);
    const token = await read(context, state.admin_url, root);
    const referenceModel = await read(
      context,
      state.admin_url,
      `/admin/api/charity-models/${data.models[239]}`,
    );
    for (const key of ['pricing', 'token_reserve_credits', 'discount', 'recurring_discount'])
      expect(token[key]).toEqual(referenceModel[key]);
    for (const key of ['id', 'provider', 'model', 'enabled', 'route_strategy', 'allowed_levels'])
      expect(token[key]).toEqual(before[key]);
    await page.getByRole('button', { name: workflow.openPriceReference, exact: true }).click();
    // Reopen the lazy panel if it was closed; retained panel state preserves its search.
    const toggle = page
      .locator('summary')
      .filter({ has: page.getByText(workflow.templates, { exact: true }) });
    if (!(await toggle.evaluate((node) => (node.parentElement as HTMLDetailsElement).open)))
      await toggle.click();
    await page
      .getByRole('row')
      .filter({ hasText: 'Workflow request zero discount' })
      .getByRole('button', { name: workflow.viewPrice, exact: true })
      .click();
    await page.getByRole('button', { name: workflow.fillTemplate, exact: true }).click();
    await expect(page.getByRole('combobox', { name: 'Pricing mode', exact: true })).toHaveValue(
      'per_request',
    );
    await page.getByRole('button', { name: `Save model #${data.models[1]}`, exact: true }).click();
    await expect
      .poll(async () => (await read(context, state.admin_url, root)).pricing.mode)
      .toBe('per_request');
    const request = await read(context, state.admin_url, root);
    expect(request.token_reserve_credits).toBeNull();
    expect(request.discount).toEqual({ enabled: false, percent: 0, start_at: null, end_at: null });
    expect(request.pricing.long_context).toBeUndefined();
    expect(request.pricing.context_penalty).toBeUndefined();
    await page.getByRole('button', { name: workflow.openPriceReference, exact: true }).click();
    await page.getByRole('button', { name: workflow.maintainTemplates, exact: true }).click();
    await page.getByRole('button', { name: workflow.createTemplate, exact: true }).click();
    const templateEditor = page.locator('.nb-template-editor');
    await templateEditor
      .getByLabel(workflow.templateName, { exact: true })
      .fill('Workflow disposable template');
    await templateEditor
      .getByLabel(workflow.templateDescription, { exact: true })
      .fill('Browser complete price snapshot');
    await templateEditor
      .getByRole('switch', { name: workflow.templateEnabled, exact: true })
      .uncheck();
    await templateEditor
      .getByLabel('Request user price', { exact: true })
      .fill(request.pricing.user_price);
    await templateEditor
      .getByLabel('Request donor reward', { exact: true })
      .fill(request.pricing.donor_reward);
    const made = page.waitForResponse(
      (response) =>
        response.request().method() === 'POST' &&
        new URL(response.url()).pathname === '/admin/api/charity-price-templates',
    );
    await templateEditor
      .getByRole('button', { name: workflow.createTemplate, exact: true })
      .click();
    const madeResponse = await made;
    expect(madeResponse.status()).toBe(201);
    const template = await madeResponse.json();
    expect(template.enabled).toBe(false);
    expect(template.pricing).toEqual(request.pricing);
    await templateEditor
      .getByRole('switch', { name: workflow.templateEnabled, exact: true })
      .check();
    const priceLabels = adminEn.admin.charity;
    await templateEditor
      .getByLabel(priceLabels.pricingMode, { exact: true })
      .selectOption('per_token');
    for (const [field, userLabel, rewardLabel] of [
      [
        'uncached_input',
        priceLabels.uncached_user_price_milli,
        priceLabels.uncached_donor_reward_milli,
      ],
      [
        'cache_write_input',
        priceLabels.cache_write_user_price_milli,
        priceLabels.cache_write_donor_reward_milli,
      ],
      [
        'cache_read_input',
        priceLabels.cache_read_user_price_milli,
        priceLabels.cache_read_donor_reward_milli,
      ],
      ['output', priceLabels.output_user_price_milli, priceLabels.output_donor_reward_milli],
    ]) {
      await templateEditor
        .getByLabel(userLabel, { exact: true })
        .fill(referenceModel.pricing.user_prices[field]);
      await templateEditor
        .getByLabel(rewardLabel, { exact: true })
        .fill(referenceModel.pricing.donor_rewards[field]);
    }
    await templateEditor.locator('summary').filter({ hasText: operations.quotaReserve }).click();
    await templateEditor
      .getByLabel(operations.tokenReserveCredits, { exact: true })
      .fill(referenceModel.token_reserve_credits);
    await templateEditor
      .locator('summary')
      .filter({ hasText: operations.context.settings })
      .click();
    await templateEditor
      .getByRole('switch', { name: operations.context.long, exact: true })
      .check();
    await templateEditor
      .getByLabel(operations.context.longThreshold, { exact: true })
      .fill(referenceModel.pricing.long_context.input_threshold);
    for (const [field, label] of [
      ['uncached_input', priceLabels.uncached_user_price_milli],
      ['cache_write_input', priceLabels.cache_write_user_price_milli],
      ['cache_read_input', priceLabels.cache_read_user_price_milli],
      ['output', priceLabels.output_user_price_milli],
    ])
      await templateEditor
        .getByLabel(workflow.longPriceField.replace('{{field}}', label), { exact: true })
        .fill(referenceModel.pricing.long_context.user_prices[field]);
    await templateEditor
      .getByRole('switch', { name: operations.context.penalty, exact: true })
      .check();
    await templateEditor
      .getByLabel(operations.context.penaltyThreshold, { exact: true })
      .fill(referenceModel.pricing.context_penalty.input_threshold);
    await templateEditor
      .getByLabel(operations.context.fine, { exact: true })
      .fill(referenceModel.pricing.context_penalty.fine);
    await templateEditor
      .locator('summary')
      .filter({ has: page.getByText(common.common.recurringDiscount.fixedTitle, { exact: true }) })
      .click();
    await templateEditor
      .getByRole('switch', { name: priceLabels.discountEnabled, exact: true })
      .check();
    await templateEditor.getByLabel(priceLabels.discountPercent, { exact: true }).fill('80');

    const changed = page.waitForResponse(
      (response) =>
        response.request().method() === 'PUT' &&
        new URL(response.url()).pathname.endsWith('/charity-price-templates/' + template.id),
    );
    await page
      .getByRole('button', { name: 'Save template “Workflow disposable template”', exact: true })
      .click();
    expect((await changed).ok()).toBe(true);
    const currentTemplate = await read(
      context,
      state.admin_url,
      '/admin/api/charity-price-templates/' + template.id,
    );
    for (const field of ['pricing', 'token_reserve_credits', 'discount', 'recurring_discount'])
      expect(currentTemplate[field]).toEqual(referenceModel[field]);
    expect(currentTemplate).toMatchObject({
      enabled: true,
      revision: '2',
      description: 'Browser complete price snapshot',
    });
    await page.getByRole('button', { name: workflow.deleteTemplate, exact: true }).click();
    await page
      .getByRole('alertdialog')
      .getByRole('button', { name: workflow.deleteTemplate, exact: true })
      .click();
    await expect
      .poll(async () =>
        (
          await context.request.get(
            state.admin_url + '/admin/api/charity-price-templates/' + template.id,
          )
        ).status(),
      )
      .toBe(404);
    const unchangedModel = await read(context, state.admin_url, root);
    expect(unchangedModel.server_now).toBeGreaterThanOrEqual(request.server_now);
    expect({ ...unchangedModel, server_now: request.server_now }).toEqual(request);
    await page.getByRole('button', { name: 'Return to model draft', exact: true }).click();
    await page.getByRole('button', { name: workflow.returnPriceDraft, exact: true }).click();
    await page.getByRole('button', { name: operations.returnToList, exact: true }).first().click();
    await page.getByRole('button', { name: workflow.newModel, exact: true }).click();
    await expect(provider).toHaveValue('');
    await expect(model).toHaveValue('');
    await model.fill('unsaved-workflow-draft');
    await page.getByRole('button', { name: operations.returnToList, exact: true }).first().click();
    const leave = page.getByRole('alertdialog');
    await expect(leave).toBeVisible();
    await leave.getByRole('button', { name: common.common.leave.stay, exact: true }).click();
    await expect(model).toHaveValue('unsaved-workflow-draft');
    await page.getByRole('button', { name: operations.returnToList, exact: true }).first().click();
    await leave.getByRole('button', { name: common.common.leave.confirm, exact: true }).click();
    await expect(page.getByRole('heading', { name: workflow.models, exact: true })).toBeVisible();
  } finally {
    await context.close();
  }
});

test('owner current and ended defaults, mobile numbered controls, single and batched history restore', async ({
  browser,
}, info) => {
  const state = fixture(),
    data = state.management.workflow;
  const context = await session(browser, 'owner');
  try {
    const page = await context.newPage();
    await page.goto(state.user_url + '/charity?tab=donations');
    const panel = page.locator('.economy-owner-pages');
    await expect(panel.getByRole('tab', { name: history.current, exact: true })).toHaveAttribute(
      'aria-selected',
      'true',
    );
    await expect(panel.getByText('Workflow ended donation', { exact: true })).toHaveCount(0);
    await panel.getByRole('tab', { name: history.history, exact: true }).click();
    const pager = panel.getByRole('navigation', { name: common.common.pagination }).first();
    await expect(pager).toContainText('167');
    await expect(pager.getByLabel(pages.size)).toBeVisible();
    await visuals(page, info, 'workflow-owner-history', async () => {
      await pager.scrollIntoViewIfNeeded();
      await expect(pager.getByLabel(pages.size)).toBeVisible();
      await expect(pager.getByLabel(pages.jump)).toBeVisible();
    });
    await pager.getByLabel(pages.size).selectOption('10');
    await expect(pager.getByLabel(pages.size)).toHaveValue('10');
    await pager.getByLabel(pages.jump).fill('2');
    await pager.getByLabel(pages.jump).press('Enter');
    await expect(page).toHaveURL(/donations_page=2/);
    await expect(pager.locator('[aria-current="page"]')).toHaveText('2');
    const card = panel.locator('.economy-donation-summary').first();
    await card.getByRole('button', { name: history.hideOne, exact: true }).click();
    await panel.getByRole('tab', { name: history.hidden, exact: true }).click();
    await expect(panel.locator('.economy-donation-summary')).toHaveCount(1);
    await panel.getByRole('button', { name: history.restoreOne, exact: true }).click();
    await expect(panel.locator('.economy-donation-summary')).toHaveCount(0);
    await panel.getByRole('tab', { name: history.history, exact: true }).click();
    await panel.getByRole('button', { name: history.hideAll, exact: true }).click();
    await expect(panel.getByText(/Hide.*167 changed.*170 checked.*Complete/)).toBeVisible();
    const hidden = await read(
      context,
      state.user_url,
      '/api/donations?view=hidden&page=1&page_size=100',
    );
    expect(hidden.pagination.total_items).toBe('167');
    expect(
      (await read(context, state.user_url, '/api/donations?view=current&page=1&page_size=20'))
        .pagination.total_items,
    ).toBe('3');
    await panel.getByRole('tab', { name: history.hidden, exact: true }).click();
    await panel.getByRole('button', { name: history.restoreAll, exact: true }).click();
    await expect
      .poll(
        async () =>
          (await read(context, state.user_url, '/api/donations?view=hidden&page=1&page_size=20'))
            .pagination.total_items,
      )
      .toBe('0');
    await page.goto(
      state.user_url +
        `/charity?tab=donations&donation_view=history&donation_expanded=${data.ended_donation_id}`,
    );
    await expect(panel.getByRole('tab', { name: history.keys.ended, exact: true })).toHaveAttribute(
      'aria-selected',
      'true',
    );
    const ended = await read(context, state.user_url, `/api/donations/${data.ended_donation_id}`);
    const mask = ended.keys[0].display_head + '…' + ended.keys[0].display_tail;
    await expect(
      panel
        .getByRole('region', { name: en.user.charity.ownerPages.keys, exact: true })
        .getByText(mask, { exact: true })
        .first(),
    ).toBeVisible();
    await page.goto(state.user_url + `/charity/donations/${data.ended_donation_id}`);
    const keyPanel = page.locator('.economy-owner-keys');
    await expect(
      keyPanel.getByRole('tab', { name: history.keys.ended, exact: true }),
    ).toHaveAttribute('aria-selected', 'true');
    const keyPager = keyPanel.getByRole('navigation', { name: common.common.pagination });
    await expect(keyPager.getByLabel(pages.size)).toBeVisible();
    await expect(keyPager.getByLabel(pages.jump)).toBeVisible();
    await keyPager.getByLabel(pages.size).selectOption('10');
    await expect(keyPager.getByLabel(pages.size)).toHaveValue('10');
    await keyPager.getByLabel(pages.jump).fill('2');
    await keyPager.getByLabel(pages.jump).press('Enter');
    await expect(page).toHaveURL(/donation_keys_page=2/);
    await expect(keyPager.locator('[aria-current="page"]')).toHaveText('2');
    await visuals(page, info, 'workflow-owner-detail', () => keyPager.scrollIntoViewIfNeeded());
  } finally {
    await context.close();
  }
});

test('level 5 handles all four custom-source keys and edits shared limits only in model scope; level 6 applies templates', async ({
  browser,
}) => {
  const state = fixture(),
    data = state.management.workflow;
  const context = await session(browser, 5);
  try {
    const page = await context.newPage();
    await page.goto(
      state.user_url +
        `/steward?tab=charity&charity_section=handling&handling_donation=${data.processing_donation_id}`,
    );
    await expect(page.getByText('4 keys, 0 with connections.', { exact: true })).toBeVisible();
    await expect(
      page.getByText('This action marks the whole donation, including all 4 keys.', {
        exact: true,
      }),
    ).toBeVisible();
    await page.getByRole('button', { name: workflow.processWhole, exact: true }).click();
    const admin = await session(browser, 'admin');
    try {
      await expect
        .poll(
          async () =>
            (
              await read(
                admin,
                state.admin_url,
                '/admin/api/donations/' + data.processing_donation_id,
              )
            ).handling.state,
        )
        .toBe('processed');
    } finally {
      await admin.close();
    }
    await page.goto(
      state.user_url +
        `/steward?tab=charity&charity_section=models&charity_model=${data.models[0]}`,
    );
    await expect(page.getByRole('button', { name: workflow.newModel, exact: true })).toHaveCount(0);
    await expect(
      page.getByRole('button', { name: workflow.openPriceReference, exact: true }),
    ).toHaveCount(0);
    const denied = await context.request.get(
      state.user_url + '/api/steward/charity-price-templates?page=1&page_size=20',
    );
    expect(denied.status()).toBe(403);
    const sourceState = page.getByRole('combobox', { name: workflow.keyState, exact: true }).last();
    await expect(sourceState).toHaveValue('available');
    await sourceState.selectOption('disabled');
    await expect(page.getByText(operations.sourceBrowser.noSources, { exact: true })).toBeVisible();
    await expect(page.locator('.charity-source-browser__source-button')).toHaveCount(0);
    await sourceState.selectOption('available');
    await page
      .locator('.charity-source-browser__source-button')
      .filter({ has: page.getByText('Mainstream channel: Browser service', { exact: true }) })
      .click();
    await page
      .locator('.charity-source-browser')
      .getByRole('button', { name: `Manage key #${data.pending_key_id}`, exact: true })
      .click();
    const concurrency = page.getByLabel(common.common.keyLimits.concurrency, { exact: true });
    const rpm = page.getByLabel(common.common.keyLimits.rpm, { exact: true });
    await concurrency.fill('-1');
    await expect(
      page.getByRole('button', { name: common.common.keyLimits.save, exact: true }),
    ).toBeDisabled();
    await concurrency.fill('3');
    await rpm.fill('77');
    const scopePath = `/api/steward/donations/${data.pending_donation_id}/keys/${data.pending_key_id}?charity_model_id=${data.models[0]}`;
    const before = await read(context, state.user_url, scopePath);
    let firstKey = '';
    let interrupted = false;
    await page.route(
      (url) => url.pathname === scopePath.split('?')[0],
      async (route) => {
        if (route.request().method() !== 'PATCH' || interrupted) return route.continue();
        interrupted = true;
        firstKey = route.request().headers()['idempotency-key'];
        expect(route.request().postDataJSON()).toMatchObject({
          expected_revision: before.donation_revision,
          expected_key_limits_revision: before.key_limits_revision,
          max_concurrency: 3,
          max_rpm: 77,
        });
        const response = await route.fetch();
        expect(response.ok()).toBe(true);
        await route.abort('connectionfailed');
      },
    );
    await page.getByRole('button', { name: common.common.keyLimits.save, exact: true }).click();
    const retry = page.getByRole('button', {
      name: common.common.donationReview.retrySame,
      exact: true,
    });
    await expect(retry).toBeVisible();
    await expect(concurrency).toHaveValue('3');
    const retried = page.waitForResponse(
      (response) =>
        response.request().method() === 'PATCH' &&
        new URL(response.url()).pathname === scopePath.split('?')[0],
    );
    await retry.click();
    const response = await retried;
    expect(response.ok()).toBe(true);
    expect(response.request().headers()['idempotency-key']).toBe(firstKey);
    const saved = await read(context, state.user_url, scopePath);
    expect(saved.max_concurrency).toBe(3);
    expect(saved.max_rpm).toBe(77);
    expect(saved.key_limits_revision).not.toBe(before.key_limits_revision);
  } finally {
    await context.close();
  }
  const six = await session(browser, 6);
  try {
    const page = await six.newPage();
    await page.goto(
      state.user_url +
        `/steward?tab=charity&charity_section=models&charity_model=${data.models[2]}`,
    );
    await page.getByRole('button', { name: workflow.openPriceReference, exact: true }).click();
    await page
      .locator('summary')
      .filter({ has: page.getByText(workflow.templates, { exact: true }) })
      .click();
    await page
      .getByRole('row')
      .filter({ hasText: 'Workflow token complete' })
      .getByRole('button', { name: workflow.viewPrice, exact: true })
      .click();
    await expect(
      page
        .locator('summary')
        .filter({ has: page.getByText(workflow.maintainTemplates, { exact: true }) }),
    ).toHaveCount(0);
    await page.getByRole('button', { name: workflow.fillTemplate, exact: true }).click();
    await expect(page.getByRole('combobox', { name: 'Pricing mode', exact: true })).toHaveValue(
      'per_token',
    );
  } finally {
    await six.close();
  }
});

test('complete headers stay lazy, preserve duplicates and physical responses, and apply level 5 IPv4 redaction', async ({
  browser,
}, info) => {
  const state = fixture(),
    id = state.management.workflow.header_request_id;
  for (const role of ['admin', 5] as const) {
    const context = await session(browser, role);
    try {
      const origin = role === 'admin' ? state.admin_url : state.user_url;
      const page = await context.newPage();
      const reads: string[] = [];
      page.on('request', (request) => {
        if (/\/headers$/.test(new URL(request.url()).pathname)) reads.push(request.url());
      });
      await page.goto(
        origin + (role === 'admin' ? '/logs?request_id=' : '/steward?tab=logs&request_id=') + id,
      );
      const detail = page.locator('.nb-expandable-panel:not([hidden])');
      await expect(detail).toBeVisible();
      const requestToggle = detail
        .locator('summary')
        .filter({ hasText: common.common.headers.request });
      await expect(requestToggle.locator('..')).not.toHaveAttribute('open');
      expect(reads).toHaveLength(0);
      await requestToggle.focus();
      await page.keyboard.press('Enter');
      const requestHeaders = detail.getByRole('region', {
        name: common.common.headers.request,
        exact: true,
      });
      await expect(requestHeaders).toContainText('WorkflowBrowser/1.0');
      await expect(requestHeaders).toContainText('[redacted]');
      if (role === 5) {
        await expect(requestHeaders).not.toContainText('198.51.100.71');
        await expect(requestHeaders).toContainText(common.common.headers.ipv4);
      } else {
        await expect(requestHeaders).toContainText('198.51.100.71');
        await expect(requestHeaders).toContainText('198.51.100.72');
      }
      await detail.locator('summary').filter({ hasText: 'Service call attempts' }).click();
      await detail.locator('summary').filter({ hasText: common.common.headers.response }).click();
      const responses = detail.getByRole('region', {
        name: common.common.headers.response,
        exact: true,
      });
      await expect(
        responses.getByRole('heading', { name: 'Response 1', exact: true }),
      ).toBeVisible();
      await expect(
        responses.getByRole('heading', { name: 'Response 2', exact: true }),
      ).toBeVisible();
      await expect(responses).not.toContainText('synthetic-response-credential');
      if (role === 'admin') {
        await expect(responses.getByText('198.51.100.91', { exact: true })).toHaveCount(2);
        await visuals(page, info, 'workflow-log-headers', () => responses.scrollIntoViewIfNeeded());
      } else {
        await expect(responses).not.toContainText('198.51.100.91');
        await expect(detail).toContainText('127.0.0.1');
      }
      expect(reads).toHaveLength(2);
    } finally {
      await context.close();
    }
  }
});

test('level 5 reviews independent pending sources, retains receipts after refresh failure and retries an unknown result', async ({
  browser,
}, info) => {
  const state = fixture();
  const owner = await session(browser, 'owner');
  const steward = await session(browser, 5);
  const admin = await session(browser, 'admin');
  const write = async (context: BrowserContext, origin: string, path: string, data: unknown) => {
    const response = await context.request.post(origin + path, {
      headers: { Origin: origin, 'Idempotency-Key': randomUUID() },
      data,
    });
    expect(response.ok(), path + ': ' + response.status()).toBe(true);
    return response.json();
  };
  try {
    for (const decision of ['approve', 'reject'] as const) {
      const endpoint = await write(owner, state.user_url, '/api/endpoints', {
        source: 'custom',
        connector_type: 'openai-compatible',
        base_url: `https://pending-${decision}.example.test/v1`,
        note: 'Synthetic pending review',
        enabled: true,
      });
      const key = await write(owner, state.user_url, `/api/endpoints/${endpoint.id}/keys`, {
        secret: `synthetic-pending-browser-${decision}`,
        note: 'Synthetic review key',
        enabled: true,
        force_store_false: false,
        ownership_confirmed: true,
      });
      const donation = await write(owner, state.user_url, '/api/donations', {
        description: `Independent pending ${decision}`,
        ownership_authorized: true,
        discord_public_thanks: false,
        keys: [{ endpoint_key_id: key.id, expires_at: null }],
      });
      expect(donation.status).toBe('pending');
      const page = await steward.newPage();
      let committed = false,
        failList = false,
        interrupted = false,
        writes = 0,
        completedReads = 0;
      const identities: string[] = [];
      page.on('request', (request) => {
        if (
          committed &&
          request.method() === 'GET' &&
          new URL(request.url()).pathname === `/api/steward/donations/${donation.id}`
        )
          completedReads++;
      });
      await page.route(state.user_url + '/api/steward/donations**', async (route) => {
        const request = route.request(),
          url = new URL(request.url());
        if (
          request.method() === 'POST' &&
          url.pathname === `/api/steward/donations/${donation.id}/review`
        ) {
          writes++;
          identities.push(request.headers()['idempotency-key']);
          const response = await route.fetch();
          expect(response.ok()).toBe(true);
          const receipt = await response.json();
          expect(receipt.owner).toBeNull();
          expect(receipt.reviewer).toBeNull();
          committed = true;
          if (decision === 'reject' && !interrupted) {
            interrupted = true;
            await route.abort('connectionfailed');
            return;
          }
          failList = decision === 'approve';
          await route.fulfill({ response });
          return;
        }
        if (request.method() === 'GET' && url.pathname === '/api/steward/donations' && failList) {
          failList = false;
          await route.fulfill({
            status: 503,
            json: { error: { code: 'temporary_failure', message: 'Synthetic refresh failure' } },
          });
          return;
        }
        await route.continue();
      });
      await page.goto(
        state.user_url +
          `/steward?tab=charity&charity_section=donations&donation_id=${donation.id}`,
      );
      await expect(
        page.getByRole('tab', { name: common.common.pendingReviews.title, exact: true }),
      ).toBeVisible();
      await expect(page.getByRole('tab', { name: workflow.templates, exact: true })).toHaveCount(0);
      await expect(
        page.getByRole('button', { name: common.common.donationReview.forceTitle, exact: true }),
      ).toHaveCount(0);
      await page.getByLabel(operations.decisionLabel).selectOption(decision);
      await page
        .getByLabel(operations.reason, { exact: true })
        .fill('Synthetic independent pending review');
      await page.getByLabel(operations.confirmReview, { exact: true }).check();
      if (decision === 'approve') {
        await page.getByLabel(operations.enableForCharity, { exact: true }).check();
        await visuals(page, info, 'pending-review', () =>
          page.getByLabel(operations.reason, { exact: true }).scrollIntoViewIfNeeded(),
        );
        await page.setViewportSize({ width: 1440, height: 900 });
      }
      await page
        .getByRole('button', {
          name: decision === 'approve' ? 'Approve donation' : 'Reject donation',
          exact: true,
        })
        .click();
      if (decision === 'reject') {
        await page
          .getByRole('button', { name: common.common.donationReview.retrySame, exact: true })
          .click();
        expect(identities).toHaveLength(2);
        expect(identities[0]).toBe(identities[1]);
      } else {
        await expect(page.getByText(/Review saved:/)).toBeVisible();
        await page
          .getByRole('button', { name: common.common.outcome.recheck, exact: true })
          .click();
      }
      await expect(
        page.getByRole('button', {
          name: decision === 'approve' ? 'Approve donation' : 'Reject donation',
          exact: true,
        }),
      ).toHaveCount(0);
      expect(completedReads).toBe(0);
      expect(writes).toBe(decision === 'approve' ? 1 : 2);
      const denied = await steward.request.get(
        state.user_url + `/api/steward/donations/${donation.id}`,
      );
      expect(denied.status()).toBe(403);
      const approved = await read(admin, state.admin_url, `/admin/api/donations/${donation.id}`);
      expect(approved.status).toBe(decision === 'approve' ? 'approved' : 'rejected');
      if (decision === 'approve') {
        expect(approved.first_approval_origin).toBe('manual');
        const senior = await session(browser, 6);
        try {
          const correction = await senior.newPage();
          await correction.goto(state.user_url + `/steward?tab=charity&donation_id=${donation.id}`);
          await correction
            .getByLabel(operations.reason, { exact: true })
            .fill('Synthetic manual-approval correction');
          await correction
            .getByRole('button', { name: common.common.donationReview.forceTitle, exact: true })
            .click();
          await correction
            .getByRole('alertdialog')
            .getByRole('button', { name: common.common.donationReview.forceTitle, exact: true })
            .click();
          await expect
            .poll(
              async () =>
                (await read(admin, state.admin_url, `/admin/api/donations/${donation.id}`)).status,
            )
            .toBe('rejected');
          expect(
            (await read(admin, state.admin_url, `/admin/api/donations/${donation.id}`))
              .first_approval_origin,
          ).toBe('manual');
        } finally {
          await senior.close();
        }
      }

      await page.close();
    }
  } finally {
    await owner.close();
    await steward.close();
    await admin.close();
  }
});
