import { randomUUID } from 'node:crypto';
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
import userEn from '../../src/user/i18n/en.json' with { type: 'json' };
import type { AdminUser } from '../../src/shared/operations/managedUsers';
import type { CharityModel, TokenPrices } from '../../src/shared/operations/charity';
import type { CharityPriceTemplate } from '../../src/shared/operations/charityPriceTemplates';

type Cookie = { Name: string; Value: string };
interface Fixture {
  admin_url: string;
  user_url: string;
  admin_cookie: Cookie;
  users: { id: string; level: number; cookie: Cookie }[];
  management: { trainee_target_id: string };
}
const limits = common.management.users;
const prices = common.common.modalityPricing;
const workflow = common.common.workflow;
const operations = common.common.operations.charity;
const fields = [
  ['global_rpm_limit', limits.globalRPMLimit, '96'],
  ['rpm_limit', limits.rpmLimit, '72'],
  ['light_rpm_limit', limits.lightRPMLimit, '36'],
  ['concurrency_limit', limits.concurrencyLimit, '7'],
  ['light_concurrency_limit', limits.lightConcurrencyLimit, '21'],
] as const;
const zero: TokenPrices = {
  uncached_input: '0',
  cache_write_input: '0',
  cache_read_input: '0',
  output: '0',
};
function fixture(): Fixture {
  return JSON.parse(readFileSync(process.env.NONBIRI_AUDIT_BROWSER_STATE!, 'utf8')) as Fixture;
}
test.skip(
  process.env.NONBIRI_MANAGEMENT_BROWSER !== '1',
  'Requires the isolated management fixture.',
);

async function session(browser: Browser, role: 'admin' | 1 | 5) {
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
  return { context, origin };
}
async function read<T>(context: BrowserContext, origin: string, path: string): Promise<T> {
  const response = await context.request.get(origin + path, { headers: { Origin: origin } });
  expect(response.ok(), path + ': ' + response.status()).toBe(true);
  return response.json() as Promise<T>;
}
async function post<T>(
  context: BrowserContext,
  origin: string,
  path: string,
  data: unknown,
): Promise<T> {
  const response = await context.request.post(origin + path, {
    headers: { Origin: origin, 'Idempotency-Key': randomUUID() },
    data,
  });
  expect(response.ok(), path + ': ' + response.status()).toBe(true);
  return response.json() as Promise<T>;
}
async function save(page: Page, control: Locator, path: string, method = 'PATCH') {
  const pending = page.waitForResponse(
    (response) =>
      new URL(response.url()).pathname === path && response.request().method() === method,
  );
  await control.click();
  const response = await pending;
  expect(response.ok(), path + ': ' + response.status()).toBe(true);
  return response;
}
async function fold(scope: Page | Locator, title: string) {
  const summary = scope.locator('summary').filter({ has: scope.getByText(title, { exact: true }) });
  const panel = summary.locator('..');
  if ((await panel.getAttribute('open')) === null) await summary.click();
  return panel;
}
async function visuals(
  page: Page,
  info: TestInfo,
  name: string,
  focus: Locator,
  widths: readonly number[] = [1440, 390],
) {
  const directory = process.env.NONBIRI_VISUAL_DIR;
  if (directory) mkdirSync(directory, { recursive: true });
  for (const theme of ['light', 'dark']) {
    await page.evaluate((value) => {
      document.documentElement.dataset.theme = value;
    }, theme);
    for (const width of widths) {
      await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
      await focus.scrollIntoViewIfNeeded();
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
        true,
      );
      await page.screenshot({
        path: directory
          ? resolve(directory, name + '-' + theme + '-' + width + '.png')
          : info.outputPath(name + '-' + theme + '-' + width + '.png'),
        animations: 'disabled',
      });
    }
  }
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.evaluate(() => {
    document.documentElement.dataset.theme = 'light';
  });
}

test('administrator saves inherited five-limit profiles and batches only the selected current-page users', async ({
  browser,
}, info) => {
  const { context, origin } = await session(browser, 'admin');
  try {
    const id = fixture().users[0].id;
    const path = '/admin/api/users/' + id;
    const before = await read<AdminUser>(context, origin, path);
    const page = await context.newPage();
    await page.goto(origin + '/users?user=' + id);
    await page.getByRole('tab', { name: limits.tabLimits, exact: true }).click();
    for (const [, label, value] of fields)
      await page.getByLabel(label, { exact: true }).fill(value);
    await save(page, page.getByRole('button', { name: limits.saveProfile, exact: true }), path);
    const saved = await read<AdminUser>(context, origin, path);
    for (const [key, , value] of fields) {
      expect(saved[key]).toBe(value);
      expect(saved[('effective_' + key) as keyof AdminUser]).toBe(value);
    }
    await page.reload();
    await page.getByRole('tab', { name: limits.tabLimits, exact: true }).click();
    for (const [, label, value] of fields) {
      await expect(page.getByLabel(label, { exact: true })).toHaveValue(value);
      await page.getByLabel(label, { exact: true }).fill('');
    }
    await save(page, page.getByRole('button', { name: limits.saveProfile, exact: true }), path);
    const inherited = await read<AdminUser>(context, origin, path);
    for (const [key] of fields) {
      expect(inherited[key]).toBeNull();
      expect(inherited[('effective_' + key) as keyof AdminUser]).toBe(
        before[('effective_' + key) as keyof AdminUser],
      );
    }
    await expect(page.getByLabel(limits.globalRPMLimit, { exact: true })).toHaveValue('');
    await expect(
      page.getByLabel(limits.globalRPMLimit, { exact: true }),
    ).toHaveAccessibleDescription(/Inherited; currently/);
    await visuals(
      page,
      info,
      'user-limits-inherited',
      page.getByLabel(limits.globalRPMLimit, { exact: true }),
    );

    const listQuery = '?account_state=active&page=1&page_size=10';
    const list = await read<{ data: AdminUser[]; pagination: { total_pages: string } }>(
      context,
      origin,
      '/admin/api/users' + listQuery,
    );
    expect(BigInt(list.pagination.total_pages)).toBeGreaterThan(1n);
    const [first, second, untouched] = list.data.filter((item) => !item.is_admin);
    const unchanged = await read<AdminUser>(context, origin, '/admin/api/users/' + untouched.id);
    await page.goto(origin + '/users' + listQuery);
    const listName = page.getByRole('button', { name: first.username, exact: true });
    const listRow = page.getByRole('row').filter({ has: listName });
    await expect(listName).toHaveAttribute('title', first.username);
    await page.setViewportSize({ width: 390, height: 844 });
    const selectionTarget = listRow.getByRole('checkbox').locator('..');
    await expect(selectionTarget).toBeVisible();
    const selectionBox = await selectionTarget.boundingBox();
    expect(selectionBox?.width).toBeGreaterThanOrEqual(40);
    expect(selectionBox?.height).toBeGreaterThanOrEqual(40);
    expect(
      await page
        .locator('.nb-table-wrap')
        .evaluate((element) => element.scrollWidth <= element.clientWidth),
    ).toBe(true);
    await visuals(page, info, 'user-list-mobile', listRow, [390]);
    await page
      .getByRole('checkbox', {
        name: limits.selectLimitUser.replace('{{user}}', first.username),
        exact: true,
      })
      .check();
    await page.getByRole('button', { name: common.common.next, exact: true }).click();
    await expect(page).toHaveURL(/page=2/);
    await page.getByRole('button', { name: common.common.previous, exact: true }).click();
    await expect(
      page.getByRole('checkbox', {
        name: limits.selectLimitUser.replace('{{user}}', first.username),
        exact: true,
      }),
    ).not.toBeChecked();
    for (const user of [first, second])
      await page
        .getByRole('checkbox', {
          name: limits.selectLimitUser.replace('{{user}}', user.username),
          exact: true,
        })
        .check();
    const batch = await fold(page, limits.batchLimits);
    await batch
      .getByLabel(limits.batchLimitAction.replace('{{label}}', limits.lightRPMLimit), {
        exact: true,
      })
      .selectOption('set');
    await batch.getByLabel(limits.lightRPMLimit, { exact: true }).fill('45');
    await batch
      .getByLabel(limits.batchLimitAction.replace('{{label}}', limits.globalRPMLimit), {
        exact: true,
      })
      .selectOption('inherit');
    const writes: { path: string; body: Record<string, unknown> }[] = [];
    page.on('request', (request) => {
      if (
        request.method() === 'PATCH' &&
        new URL(request.url()).pathname.startsWith('/admin/api/users/')
      )
        writes.push({
          path: new URL(request.url()).pathname,
          body: request.postDataJSON() as Record<string, unknown>,
        });
    });
    await batch.getByRole('button', { name: limits.batchLimitApply, exact: true }).click();
    await expect(
      batch
        .getByRole('listitem')
        .filter({ hasText: new RegExp(' · ' + limits.batchLimitSaved + '$') }),
    ).toHaveCount(2);
    expect(writes.map((write) => write.path).sort()).toEqual(
      [first, second].map((user) => '/admin/api/users/' + user.id).sort(),
    );
    for (const write of writes) {
      expect(write.body).toMatchObject({ global_rpm_limit: null, light_rpm_limit: '45' });
      expect(Object.keys(write.body).sort()).toEqual(
        ['mode', 'expected_revision', 'global_rpm_limit', 'light_rpm_limit'].sort(),
      );
    }
    for (const user of [first, second])
      expect(await read<AdminUser>(context, origin, '/admin/api/users/' + user.id)).toMatchObject({
        global_rpm_limit: null,
        light_rpm_limit: '45',
        effective_light_rpm_limit: '45',
      });
    expect(await read<AdminUser>(context, origin, '/admin/api/users/' + untouched.id)).toEqual(
      unchanged,
    );
  } finally {
    await context.close();
  }
});

test('level five edits lower-user inference limits without exposing profile or protected-user writes', async ({
  browser,
}, info) => {
  const { context, origin } = await session(browser, 5);
  try {
    const id = fixture().management.trainee_target_id;
    const path = '/api/steward/users/' + id;
    const before = await read<AdminUser>(context, origin, path);
    const page = await context.newPage();
    await page.goto(origin + '/steward?tab=users&user=' + id);
    await page.getByRole('tab', { name: limits.tabLimits, exact: true }).click();
    await expect(page.getByLabel(limits.endpointLimit, { exact: true })).toHaveCount(0);
    await expect(page.getByLabel(limits.language, { exact: true })).toHaveCount(0);
    for (const [, label, value] of fields)
      await page.getByLabel(label, { exact: true }).fill(value);
    const response = await save(
      page,
      page.getByRole('button', { name: limits.saveProfile, exact: true }),
      path,
    );
    expect(Object.keys(response.request().postDataJSON()).sort()).toEqual(
      ['mode', 'expected_revision', ...fields.map(([key]) => key)].sort(),
    );
    const changed = await read<AdminUser>(context, origin, path);
    for (const [key, , value] of fields) expect(changed[key]).toBe(value);
    expect(changed.endpoint_limit).toBe(before.endpoint_limit);
    expect(changed.level).toEqual(before.level);
    await visuals(
      page,
      info,
      'trainee-inference-limits',
      page.getByLabel(limits.lightConcurrencyLimit, { exact: true }),
    );
    await page.getByLabel(limits.globalRPMLimit, { exact: true }).fill('');
    await save(page, page.getByRole('button', { name: limits.saveProfile, exact: true }), path);
    expect((await read<AdminUser>(context, origin, path)).global_rpm_limit).toBeNull();

    const higher = fixture().users.find((user) => user.level === 6)!;
    await page.goto(origin + '/steward?tab=users&user=' + higher.id);
    await page.getByRole('tab', { name: limits.tabLimits, exact: true }).click();
    await expect(page.getByRole('button', { name: limits.saveProfile, exact: true })).toHaveCount(
      0,
    );
    await expect(
      page.getByRole('textbox', { name: limits.globalRPMLimit, exact: true }),
    ).toHaveCount(0);
  } finally {
    await context.close();
  }
});

async function modality(
  scope: Page | Locator,
  title: string,
  kind: string,
  input: boolean,
  output: boolean,
  rates: Partial<TokenPrices>,
) {
  const panel = await fold(scope, title);
  await panel.getByRole('switch', { name: prices.input, exact: true }).setChecked(input);
  await panel.getByRole('switch', { name: prices.output, exact: true }).setChecked(output);
  const group = panel.getByRole('group', { name: kind, exact: true });
  await group.getByRole('switch', { name: prices.configure, exact: true }).check();
  const labels = {
    uncached_input: prices.uncached_input,
    cache_write_input: prices.cache_write_input,
    cache_read_input: prices.cache_read_input,
    output: prices.outputRate,
  };
  for (const [key, value] of Object.entries(rates))
    await group.getByLabel(labels[key as keyof TokenPrices], { exact: true }).fill(value);
  return panel;
}
async function openModel(page: Page, origin: string, id: string) {
  await page.goto(origin + '/charity?charity_section=models&charity_model=' + id);
  await expect(page.getByLabel('Model', { exact: true })).toBeVisible();
}
async function saveModel(page: Page, id: string) {
  return save(
    page,
    page.getByRole('button', { name: workflow.saveTarget.replace('{{id}}', id), exact: true }),
    '/admin/api/charity-models/' + id,
  );
}

test('modality prices, long-context rewards, template application and search-unit prices survive actual saves', async ({
  browser,
}, info) => {
  const { context, origin } = await session(browser, 'admin');
  try {
    const model = await post<CharityModel>(context, origin, '/admin/api/charity-models', {
      provider: 'browser',
      model: 'modality-' + randomUUID().slice(0, 8),
      enabled: true,
      is_mainstream: false,
      model_types: ['rerank'],
      flatten_tool_calls: false,
      pricing: { mode: 'per_token', user_prices: zero, donor_rewards: zero },
      discount: { enabled: false, percent: 100, start_at: null, end_at: null },
    });
    const path = '/admin/api/charity-models/' + model.id;
    const page = await context.newPage();
    await openModel(page, origin, model.id);
    await modality(page, prices.user, prices.text, true, false, {
      uncached_input: '10',
      cache_write_input: '2',
      cache_read_input: '1',
    });
    await modality(page, prices.user, prices.image, true, false, {});
    const donor = await modality(page, prices.donor, prices.audio, false, true, { output: '3' });
    await expect(donor.getByLabel(prices.uncached_input, { exact: true })).toHaveCount(0);
    const long = await fold(page, operations.context.settings);
    await long.getByRole('switch', { name: operations.context.long, exact: true }).check();
    await long.getByLabel(operations.context.longThreshold, { exact: true }).fill('1000');
    await long.getByRole('switch', { name: prices.longReward, exact: true }).check();
    await long.getByLabel('Output donor reward', { exact: true }).fill('11');
    await modality(long, prices.longUser, prices.text, true, true, {
      uncached_input: '20',
      cache_write_input: '4',
      cache_read_input: '2',
      output: '30',
    });
    await long.getByRole('switch', { name: prices.longDonorOverride, exact: true }).check();
    const longDonor = await modality(long, prices.longDonor, prices.audio, false, true, {
      output: '13',
    });
    await saveModel(page, model.id);
    const saved = await read<CharityModel>(context, origin, path);
    expect(saved.pricing).toMatchObject({
      mode: 'per_token',
      user_modalities: {
        input_enabled: true,
        output_enabled: false,
        text: { ...zero, uncached_input: '10', cache_write_input: '2', cache_read_input: '1' },
      },
      donor_modalities: {
        input_enabled: false,
        output_enabled: true,
        audio: { ...zero, output: '3' },
      },
      long_context: {
        input_threshold: '1000',
        donor_rewards: { ...zero, output: '11' },
        user_modalities: {
          input_enabled: true,
          output_enabled: true,
          text: {
            uncached_input: '20',
            cache_write_input: '4',
            cache_read_input: '2',
            output: '30',
          },
        },
        donor_modalities: {
          input_enabled: false,
          output_enabled: true,
          audio: { ...zero, output: '13' },
        },
      },
    });
    expect(saved.pricing.user_modalities?.image).toEqual(zero);
    expect(saved.pricing.user_modalities?.video).toBeUndefined();
    await visuals(
      page,
      info,
      'long-context-modality-pricing',
      longDonor.getByLabel(prices.outputRate, { exact: true }),
    );
    await page.reload();
    const reloaded = await fold(page, prices.user);
    await expect(reloaded.getByRole('switch', { name: prices.input, exact: true })).toBeChecked();
    await expect(
      reloaded.getByRole('switch', { name: prices.output, exact: true }),
    ).not.toBeChecked();

    const template = await post<CharityPriceTemplate>(
      context,
      origin,
      '/admin/api/charity-price-templates',
      {
        name: 'Modality ' + randomUUID().slice(0, 8),
        description: 'Synthetic complete prices',
        enabled: true,
        pricing: saved.pricing,
        token_reserve_credits: saved.token_reserve_credits,
        discount: saved.discount,
        recurring_discount: saved.recurring_discount,
      },
    );
    await page.goto(origin + '/charity?charity_section=templates&price_template=' + template.id);
    const templateDonor = await fold(page, prices.donor);
    await templateDonor
      .getByRole('group', { name: prices.audio, exact: true })
      .getByLabel(prices.outputRate, { exact: true })
      .fill('4.5');
    const templateResponse = await save(
      page,
      page.getByRole('button', {
        name: common.common.templateManagement.save.replace('{{name}}', template.name),
        exact: true,
      }),
      '/admin/api/charity-price-templates/' + template.id,
      'PUT',
    );
    const updated = (await templateResponse.json()) as CharityPriceTemplate;
    await openModel(page, origin, model.id);
    await page.getByRole('button', { name: workflow.openPriceReference, exact: true }).click();
    await fold(page, workflow.templates);
    const tools = page.getByRole('region', { name: workflow.priceReferencePanel, exact: true });
    await tools.getByPlaceholder(workflow.templateSearch).fill(updated.name);
    await tools.getByPlaceholder(workflow.templateSearch).press('Enter');
    await tools
      .getByRole('row')
      .filter({ hasText: updated.name })
      .getByRole('button', { name: workflow.viewPrice, exact: true })
      .click();
    await tools.getByRole('button', { name: workflow.fillTemplate, exact: true }).click();
    await page.getByRole('button', { name: workflow.returnPriceDraft, exact: true }).click();
    await saveModel(page, model.id);
    expect((await read<CharityModel>(context, origin, path)).pricing).toEqual(updated.pricing);

    await page.getByLabel('Pricing mode', { exact: true }).selectOption('per_search_unit');
    await page.getByLabel(prices.searchCharge, { exact: true }).fill('1.125');
    await page.getByLabel(prices.searchReward, { exact: true }).fill('0.25');
    await expect(page.getByRole('switch', { name: prices.input, exact: true })).toHaveCount(0);
    await expect(
      page.getByRole('switch', { name: operations.context.long, exact: true }),
    ).toHaveCount(0);
    await saveModel(page, model.id);
    expect((await read<CharityModel>(context, origin, path)).pricing).toEqual({
      mode: 'per_search_unit',
      user_price: '1.125',
      donor_reward: '0.25',
    });
    await page.reload();
    await expect(page.getByLabel(prices.searchCharge, { exact: true })).toHaveValue('1.125');
    await expect(page.getByLabel(prices.searchReward, { exact: true })).toHaveValue('0.25');
    await visuals(
      page,
      info,
      'search-unit-pricing',
      page.getByLabel(prices.searchCharge, { exact: true }),
    );
  } finally {
    await context.close();
  }
});

test('native Chat services retain storage policy and one Chat Responses group alongside other services', async ({
  browser,
}, info) => {
  const { context, origin } = await session(browser, 1);
  const core = userEn.user.core;
  try {
    const page = await context.newPage();
    await page.goto(origin + '/endpoints?quickstart=1');
    await page.getByRole('button', { name: userEn.user.services.manualLink, exact: true }).click();
    const wizard = page.locator('.core-wizard');
    await wizard.getByRole('button', { name: core['endpoints.custom'], exact: true }).click();
    await wizard
      .getByRole('button', { name: core['connector.chatCompletions'], exact: true })
      .click();
    await wizard.getByRole('button', { name: core['common.next'], exact: true }).click();
    await wizard
      .getByLabel(core['endpoints.baseUrl'], { exact: true })
      .fill('https://native-chat.example.test/v1');
    const created = await save(
      page,
      wizard.getByRole('button', { name: core['endpoints.createEndpointStep'], exact: true }),
      '/api/endpoints',
      'POST',
    );
    const endpoint = (await created.json()) as { id: string; connector_type: string };
    expect(endpoint.connector_type).toBe('openai-chat-completions');
    await wizard
      .getByLabel(core['endpoints.secret'], { exact: true })
      .fill('synthetic-native-chat-secret');
    await wizard
      .getByLabel(core['endpoints.keyNote'], { exact: true })
      .fill('Native Chat browser key');
    await wizard.getByLabel(core['endpoints.ownership'], { exact: true }).check();
    await wizard.getByLabel(core['endpoints.storePolicy'], { exact: true }).check();
    const keyResponse = await save(
      page,
      wizard.getByRole('button', { name: core['endpoints.addKey'], exact: true }),
      '/api/endpoints/' + endpoint.id + '/keys',
      'POST',
    );
    const key = (await keyResponse.json()) as { id: string; force_store_false: boolean };
    expect(key.force_store_false).toBe(true);
    await page.goto(origin + '/endpoints/' + endpoint.id);
    await page
      .getByRole('button', {
        name: userEn.user.services.more + ' · Native Chat browser key',
        exact: true,
      })
      .click();
    await save(
      page,
      page.getByRole('menuitem', { name: core['endpoints.storePolicyOff'], exact: true }),
      '/api/endpoints/' + endpoint.id + '/keys/' + key.id,
    );
    const keys = await read<{ data: { id: string; force_store_false: boolean }[] }>(
      context,
      origin,
      '/api/endpoints/' + endpoint.id + '/keys?page=1&page_size=20',
    );
    expect(keys.data.find((item) => item.id === key.id)?.force_store_false).toBe(false);

    await page.goto(origin + '/models');
    await page.getByRole('button', { name: 'Create model', exact: true }).click();
    const editor = page.locator('form.model-editor');
    await editor.getByLabel('Prefix', { exact: true }).fill('browser');
    await editor
      .getByLabel('Model name', { exact: true })
      .fill('services-' + randomUUID().slice(0, 8));
    const choices = editor.getByRole('group', {
      name: common.common.modelTypes.title,
      exact: true,
    });
    await expect(
      choices.getByRole('checkbox', { name: common.common.modelTypes.chat_responses, exact: true }),
    ).toHaveCount(1);
    await expect(
      choices.getByRole('checkbox', { name: common.common.modelTypes.responses, exact: true }),
    ).toHaveCount(0);
    for (const label of [
      common.common.modelTypes.chat_responses,
      common.common.modelTypes.images_generations,
      common.common.modelTypes.images_edits,
      common.common.modelTypes.systemone,
    ])
      await choices.getByRole('checkbox', { name: label, exact: true }).check();
    await visuals(page, info, 'model-service-groups', choices);
    const saved = await save(
      page,
      editor.getByRole('button', { name: 'Save', exact: true }),
      '/api/models',
      'POST',
    );
    const model = (await saved.json()) as { id: string; model_types: string[] };
    expect(model.model_types).toEqual([
      'chat_completions',
      'images_generations',
      'responses',
      'images_edits',
      'systemone',
    ]);
    expect(
      (await read<{ model_types: string[] }>(context, origin, '/api/models/' + model.id))
        .model_types,
    ).toEqual(model.model_types);
  } finally {
    await context.close();
  }
});
