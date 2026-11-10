import { mkdirSync, readFileSync } from 'node:fs';
import { randomUUID } from 'node:crypto';
import { resolve } from 'node:path';
import { setControlChecked } from '../controls';
import {
  expect,
  test,
  type Browser,
  type BrowserContext,
  type Locator,
  type Page,
} from '@playwright/test';
import common from '../../src/shared/i18n/common/en.json' with { type: 'json' };
import userCopy from '../../src/user/i18n/en.json' with { type: 'json' };
import adminCopy from '../../src/admin/i18n/en.json' with { type: 'json' };
import commonZh from '../../src/shared/i18n/common/zh.json' with { type: 'json' };
import adminZh from '../../src/admin/i18n/zh.json' with { type: 'json' };
import userZh from '../../src/user/i18n/zh.json' with { type: 'json' };

type Cookie = { Name: string; Value: string };
interface Fixture {
  admin_url: string;
  user_url: string;
  admin_cookie: Cookie;
  users: { id: string; level: number; cookie: Cookie }[];
  management: { binding_model_ids: string[] };
}
interface Policy {
  revision: string;
  donation_scope: string;
  permissions: Record<string, boolean>;
  limits: Record<string, string>;
}
const fixture = () =>
  JSON.parse(readFileSync(process.env.NONBIRI_AUDIT_BROWSER_STATE!, 'utf8')) as Fixture;
const workflow = common.common.workflow;
const adaptation = common.requestAdaptation.editor;
test.skip(
  process.env.NONBIRI_MANAGEMENT_BROWSER !== '1',
  'Requires the isolated management fixture.',
);

async function session(browser: Browser, role: 'admin' | 1 | 5 | 6, language = 'en') {
  const state = fixture();
  const origin = role === 'admin' ? state.admin_url : state.user_url;
  const cookie =
    role === 'admin' ? state.admin_cookie : state.users.find((row) => row.level === role)!.cookie;
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
  await context.addInitScript((language) => {
    localStorage.setItem('nb.lang', language);
    localStorage.setItem('nb.theme', 'light');
  }, language);
  return context;
}
async function api(
  context: BrowserContext,
  origin: string,
  path: string,
  method = 'GET',
  data?: unknown,
) {
  const response = await context.request.fetch(origin + path, {
    method,
    headers: { Origin: origin, ...(method === 'GET' ? {} : { 'Idempotency-Key': randomUUID() }) },
    ...(data === undefined ? {} : { data }),
  });
  expect(response.ok(), method + ' ' + path + ': ' + response.status()).toBe(true);
  return response.status() === 204 ? undefined : response.json();
}
async function openFold(scope: Page | Locator, title: string) {
  const fold = scope
    .locator('details')
    .filter({ has: scope.getByText(title, { exact: true }) })
    .last();
  if ((await fold.getAttribute('open')) === null) await fold.locator(':scope > summary').click();
  return fold;
}
async function saveResponse(page: Page, method: string, path: string, click: () => Promise<void>) {
  const waiting = page.waitForResponse(
    (response) =>
      response.request().method() === method && new URL(response.url()).pathname === path,
  );
  await click();
  const response = await waiting;
  expect(response.ok(), method + ' ' + path + ': ' + response.status()).toBe(true);
  return response;
}
async function visuals(
  page: Page,
  name: string,
  focus: Locator,
  check?: (width: number) => Promise<void>,
) {
  const directory = process.env.NONBIRI_VISUAL_DIR ?? test.info().outputPath('layout');
  mkdirSync(directory, { recursive: true });
  for (const theme of ['light', 'dark'])
    for (const width of [1440, 390]) {
      await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
      await page.evaluate((value) => {
        document.documentElement.dataset.theme = value;
      }, theme);
      await focus.scrollIntoViewIfNeeded();
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
        true,
      );
      await check?.(width);
      await page.screenshot({
        path: resolve(directory, `${name}-${theme}-${width}.png`),
        animations: 'disabled',
      });
    }
}
async function saveAdaptation(page: Page, modelID: string, bindingID?: string) {
  if (bindingID) {
    await page
      .getByRole('row')
      .filter({ has: page.getByRole('button', { name: adaptation.title, exact: true }) })
      .first()
      .getByRole('button', { name: adaptation.title, exact: true })
      .click();
  }
  const fold = await openFold(
    page,
    bindingID
      ? common.common.operations.charity.adaptationSettings
      : common.requestAdaptation.model.title,
  );
  const defaults = fold.getByRole('group', { name: adaptation.bodyDefaults, exact: true });
  await expect(defaults).toBeVisible();
  const mode = defaults.getByLabel(adaptation.mode, { exact: true });
  if (bindingID) await mode.selectOption('replace');
  await defaults.getByRole('button', { name: adaptation.add, exact: true }).click();
  await defaults
    .getByRole('textbox', { name: adaptation.name, exact: true })
    .last()
    .fill('/temperature');
  await defaults.getByRole('textbox', { name: adaptation.value, exact: true }).last().fill('0.7');
  const path = `/api/steward/charity-models/${modelID}${bindingID ? '/bindings/' + bindingID : ''}/request-adaptation`;
  const response = await saveResponse(page, 'PUT', path, () =>
    fold.getByRole('button', { name: adaptation.save, exact: true }).click(),
  );
  expect(response.request().postDataJSON().body_defaults.values['/temperature']).toMatchObject({
    action: 'replace',
    value: 0.7,
  });
}
async function maintainTemplate(page: Page, origin: string, suffix: string) {
  await page.goto(origin + '/steward?tab=charity&charity_section=templates&price_template=new');
  const name = 'Synthetic layout ' + suffix;
  await page.getByLabel(workflow.templateName, { exact: true }).fill(name);
  await setControlChecked(
    page.getByRole('switch', { name: workflow.templateEnabled, exact: true }),
    false,
  );
  const root = '/api/steward/charity-price-templates';
  const created = await saveResponse(page, 'POST', root, () =>
    page.getByRole('button', { name: workflow.createTemplate, exact: true }).click(),
  );
  const template = await created.json();
  await page
    .getByLabel(workflow.templateDescription, { exact: true })
    .fill('Synthetic updated template');
  await saveResponse(page, 'PUT', root + '/' + template.id, () =>
    page
      .getByRole('button', {
        name: common.common.templateManagement.save.replace('{{name}}', name),
        exact: true,
      })
      .click(),
  );
  expect((await api(page.context(), origin, root + '/' + template.id)).description).toBe(
    'Synthetic updated template',
  );
  await page.getByRole('button', { name: workflow.deleteTemplate, exact: true }).click();
  await saveResponse(page, 'DELETE', root + '/' + template.id, () =>
    page
      .getByRole('alertdialog')
      .getByRole('button', { name: workflow.deleteTemplate, exact: true })
      .click(),
  );
}

test('senior stewards save adaptation, affinity and templates; single level-5 grants include their reads', async ({
  browser,
}) => {
  test.setTimeout(180_000);
  const state = fixture();
  const admin = await session(browser, 'admin'),
    senior = await session(browser, 6),
    five = await session(browser, 5);
  const fiveID = state.users.find((row) => row.level === 5)!.id;
  const policyPath = `/admin/api/users/${fiveID}/steward-permissions`;
  const original = (await api(admin, state.admin_url, policyPath)) as Policy;
  const keys = ['charity.request_adaptation', 'charity.affinity', 'charity.price_templates.manage'];
  async function policy(permissions: Record<string, boolean>) {
    const current = (await api(admin, state.admin_url, policyPath)) as Policy;
    await api(admin, state.admin_url, policyPath, 'PUT', {
      expected_revision: current.revision,
      donation_scope: 'full',
      permissions,
      limits: original.limits,
    });
  }
  try {
    const model = await api(admin, state.admin_url, '/admin/api/charity-models', 'POST', {
      provider: 'synthetic',
      model: 'layout-' + randomUUID().slice(0, 8),
      enabled: true,
      is_mainstream: true,
      model_types: ['chat_completions', 'responses'],
      flatten_tool_calls: false,
      pricing: { mode: 'per_request', user_price: '1', donor_reward: '0' },
      discount: { enabled: false, percent: 100, start_at: null, end_at: null },
    });
    const source = await api(
      admin,
      state.admin_url,
      `/admin/api/charity-models/${state.management.binding_model_ids[0]}/bindings`,
    );
    const sourceBinding = source.bindings[0];
    const targets = (sourceBinding.targets ?? [sourceBinding]).map(
      (target: {
        donation_key_id: string;
        upstream_model_id: string;
        native_services?: string[];
      }) => ({
        donation_key_id: target.donation_key_id,
        upstream_model_id: target.upstream_model_id,
        native_services: target.native_services ?? ['chat_completions'],
      }),
    );
    const added = await api(
      admin,
      state.admin_url,
      `/admin/api/charity-models/${model.id}/bindings/batch-receipt`,
      'POST',
      { expected_binding_revision: model.binding_revision, selections: [{ targets }] },
    );
    const bindings = await api(
      senior,
      state.user_url,
      `/api/steward/charity-models/${model.id}/bindings`,
    );
    expect(added).toBeTruthy();
    const bindingID = bindings.bindings[0].id;
    const destination =
      state.user_url + `/steward?tab=charity&charity_section=models&charity_model=${model.id}`;
    const page = await senior.newPage();
    await page.goto(destination);
    await page
      .getByLabel(common.common.operations.charity.routeAffinityTTL, { exact: true })
      .fill('420');
    await saveResponse(page, 'PATCH', `/api/steward/charity-models/${model.id}`, () =>
      page
        .getByRole('button', { name: workflow.saveTarget.replace('{{id}}', model.id), exact: true })
        .click(),
    );
    expect(
      (await api(senior, state.user_url, `/api/steward/charity-models/${model.id}`))
        .affinity_ttl_seconds,
    ).toBe(420);
    await saveAdaptation(page, model.id);
    await saveAdaptation(page, model.id, bindingID);
    const price = page.getByLabel(userCopy.user.steward.request_user_price_milli, { exact: true });
    await price.fill('2.75');
    await page.getByRole('button', { name: workflow.openPriceReference, exact: true }).click();
    await openFold(page, workflow.templates);
    await page.getByRole('button', { name: workflow.maintainTemplates, exact: true }).click();
    await expect(
      page.getByRole('heading', { name: workflow.templates, exact: true }),
    ).toBeVisible();
    await page
      .getByRole('button', { name: common.common.templateManagement.backModel, exact: true })
      .click();
    await page
      .getByRole('button', { name: workflow.returnPriceDraft, exact: true })
      .first()
      .click();
    await expect(price).toHaveValue('2.75');
    await maintainTemplate(page, state.user_url, 'senior-' + randomUUID().slice(0, 8));

    await policy({
      ...original.permissions,
      ...Object.fromEntries(keys.map((key) => [key, false])),
    });
    const trainee = await five.newPage();
    await trainee.goto(destination);
    await expect(
      trainee.getByRole('heading', { name: workflow.editModel, exact: true }),
    ).toBeVisible();
    await expect(
      trainee.getByLabel(common.common.operations.charity.routeAffinityTTL, { exact: true }),
    ).toHaveCount(0);
    await expect(trainee.getByRole('tab', { name: workflow.templates, exact: true })).toHaveCount(
      0,
    );
    await expect(trainee.getByRole('button', { name: adaptation.save, exact: true })).toHaveCount(
      0,
    );
    await policy({
      ...original.permissions,
      'charity.model_settings': true,
      ...Object.fromEntries(keys.map((key) => [key, false])),
    });
    await trainee.reload();
    const readOnly = await openFold(trainee, common.requestAdaptation.model.title);
    await expect(readOnly.getByText(adaptation.readOnly, { exact: true })).toBeVisible();

    for (const key of keys) {
      await policy(
        Object.fromEntries(Object.keys(original.permissions).map((name) => [name, name === key])),
      );
      await trainee.goto(destination);
      if (key === 'charity.affinity') {
        const ttl = trainee.getByLabel(common.common.operations.charity.routeAffinityTTL, {
          exact: true,
        });
        await ttl.fill('600');
        const saved = await saveResponse(
          trainee,
          'PATCH',
          `/api/steward/charity-models/${model.id}`,
          () =>
            trainee
              .getByRole('button', {
                name: workflow.saveTarget.replace('{{id}}', model.id),
                exact: true,
              })
              .click(),
        );
        expect(saved.request().postDataJSON()).toMatchObject({ affinity_ttl_seconds: 600 });
      } else if (key === 'charity.request_adaptation') {
        // A fresh path avoids editing the inherited/saved row while proving both PUT routes.
        await trainee.reload();
        const modelFold = await openFold(trainee, common.requestAdaptation.model.title);
        await expect(
          modelFold.getByRole('button', { name: adaptation.save, exact: true }),
        ).toBeVisible();
        const path = `/api/steward/charity-models/${model.id}/request-adaptation`;
        const view = await api(five, state.user_url, path);
        await api(five, state.user_url, path, 'PUT', {
          expected_revision: view.revision,
          forward_headers: { mode: 'replace', values: [] },
          fixed_headers: { mode: 'replace', values: {} },
          body_defaults: { mode: 'replace', values: {} },
          body_forced: { mode: 'replace', values: {} },
          native_extension_paths: { mode: 'replace', values: [] },
        });
        await trainee.reload();
        await saveAdaptation(trainee, model.id);
        const bindingPath = `/api/steward/charity-models/${model.id}/bindings/${bindingID}/request-adaptation`;
        const bindingView = await api(five, state.user_url, bindingPath);
        await api(five, state.user_url, bindingPath, 'PUT', {
          expected_revision: bindingView.revision,
          forward_headers: { mode: 'inherit', values: [] },
          fixed_headers: { mode: 'inherit', values: {} },
          body_defaults: { mode: 'inherit', values: {} },
          body_forced: { mode: 'inherit', values: {} },
          native_extension_paths: { mode: 'inherit', values: [] },
        });
        await saveAdaptation(trainee, model.id, bindingID);
      } else await maintainTemplate(trainee, state.user_url, 'trainee-' + randomUUID().slice(0, 8));
    }
  } finally {
    try {
      const current = (await api(admin, state.admin_url, policyPath)) as Policy;
      await api(admin, state.admin_url, policyPath, 'PUT', {
        expected_revision: current.revision,
        donation_scope: original.donation_scope,
        permissions: original.permissions,
        limits: original.limits,
      });
    } finally {
      await admin.close();
      await senior.close();
      await five.close();
    }
  }
});

test('administrator forms keep basic fields, discounts, session rules and credit actions readable', async ({
  browser,
}) => {
  const state = fixture(),
    admin = await session(browser, 'admin');
  try {
    const modelID = state.management.binding_model_ids[0];
    const page = await admin.newPage();
    await page.goto(state.admin_url + `/charity?charity_section=models&charity_model=${modelID}`);
    const provider = page.getByLabel(adminCopy.admin.charity.provider, { exact: true });
    const model = page.getByLabel(adminCopy.admin.charity.model, { exact: true });
    await expect(provider).toBeVisible();
    await visuals(page, 'admin-charity-basic', provider, async (width) => {
      const a = await provider.boundingBox(),
        b = await model.boundingBox();
      expect(a && b).toBeTruthy();
      if (width === 1440) expect(Math.abs(a!.y - b!.y)).toBeLessThan(2);
      else expect(b!.y).toBeGreaterThan(a!.y);
    });
    const discount = await openFold(page, common.common.recurringDiscount.fixedTitle);
    const percentage = discount.getByLabel(adminCopy.admin.charity.discountPercent, {
      exact: true,
    });
    await visuals(page, 'admin-charity-discount', discount, async () => {
      expect((await percentage.boundingBox())!.width).toBeLessThanOrEqual(256);
    });
    await openFold(page, common.common.operations.charity.advancedRequests);
    const headers = await openFold(page, common.common.operations.charity.sessionHeaders.title);
    await headers
      .getByRole('button', {
        name: common.common.operations.charity.sessionHeaders.add,
        exact: true,
      })
      .click();
    await headers
      .getByLabel(common.common.operations.charity.sessionHeaders.name.replace('{{n}}', '1'), {
        exact: true,
      })
      .fill('X-Synthetic-Session');
    await visuals(page, 'admin-charity-session-rule', headers);
    const trigger = page
      .getByRole('button', { name: common.common.connections.editBindingSources, exact: true })
      .first();
    await trigger.click();
    const dialog = page.getByRole('dialog', {
      name: new RegExp('^' + common.common.connections.editBindingSources),
    });
    await expect(
      dialog.getByText(common.common.connections.bindingEditScope, { exact: true }),
    ).toBeVisible();
    await visuals(page, 'admin-charity-edit-connection', dialog);
    await dialog
      .getByRole('button', { name: common.common.connections.returnToBindings, exact: true })
      .click();
    await expect(trigger).toBeFocused();
    await page.goto(
      state.admin_url + '/users?user=' + state.users.find((row) => row.level === 1)!.id,
    );
    await page.getByRole('tab', { name: common.management.users.tabEconomy, exact: true }).click();
    const credits = page.getByRole('tabpanel').filter({
      has: page.getByRole('heading', { name: common.management.users.economyTitle, exact: true }),
    });
    await expect(
      credits.getByRole('group', { name: common.management.users.economyTarget, exact: true }),
    ).toBeVisible();
    await expect(
      credits.getByRole('group', { name: common.management.users.economyDirection, exact: true }),
    ).toBeVisible();
    await visuals(page, 'admin-credit-adjustment', credits);
  } finally {
    await admin.close();
  }
});

test('personal model sheets preserve dismissed drafts and create two fresh models in sequence', async ({
  browser,
}) => {
  const state = fixture(),
    owner = await session(browser, 1);
  try {
    const page = await owner.newPage();
    await page.goto(state.user_url + '/models');
    const create = page.getByRole('button', { name: 'Create model', exact: true }).first();
    await create.click();
    let dialog = page.getByRole('dialog', { name: 'Create model', exact: true });
    const tag = 'layout-' + randomUUID().slice(0, 8);
    await dialog.getByLabel('Prefix', { exact: true }).fill(tag);
    await dialog.getByLabel('Model name', { exact: true }).fill('first');
    await dialog.getByRole('button', { name: 'Cancel', exact: true }).first().click();
    await expect(create).toBeFocused();
    await create.click();
    await expect(dialog.getByLabel('Prefix', { exact: true })).toHaveValue(tag);
    await dialog.locator('.model-types-field__more > summary').click();
    await visuals(
      page,
      'personal-model-all-interfaces',
      dialog.locator('.model-types-field'),
      async (width) => {
        const options = dialog.locator('.model-types-field__choices > .nb-field');
        expect(await options.count()).toBeGreaterThan(4);
        const boxes = await options.evaluateAll((nodes) =>
          nodes.map((node) => {
            const r = node.getBoundingClientRect();
            return { x: r.x, y: r.y, width: r.width };
          }),
        );
        expect(boxes.every((box) => box.width > 150)).toBe(true);
        if (width === 390) expect(new Set(boxes.map((box) => Math.round(box.x))).size).toBe(1);
      },
    );
    const ids: string[] = [];
    for (const name of ['first', 'second']) {
      if (name === 'second') {
        await page.getByRole('button', { name: 'Back', exact: true }).click();
        await create.click();
        dialog = page.getByRole('dialog', { name: 'Create model', exact: true });
        await expect(dialog.getByLabel('Prefix', { exact: true })).toHaveValue('');
        await expect(dialog.getByLabel('Model name', { exact: true })).toHaveValue('');
        await dialog.getByLabel('Prefix', { exact: true }).fill(tag);
        await dialog.getByLabel('Model name', { exact: true }).fill(name);
      }
      const response = await saveResponse(page, 'POST', '/api/models', () =>
        dialog.getByRole('button', { name: 'Save', exact: true }).click(),
      );
      const model = await response.json();
      ids.push(model.id);
      await expect(
        page.getByRole('heading', { name: tag + '/' + name, exact: true }).first(),
      ).toBeVisible();
    }
    expect(new Set(ids).size).toBe(2);
  } finally {
    await owner.close();
  }
});

test('Chinese shared task forms remain readable in both stations', async ({ browser }) => {
  const state = fixture(),
    admin = await session(browser, 'admin', 'zh'),
    owner = await session(browser, 1, 'zh');
  try {
    const page = await admin.newPage();
    await page.goto(
      state.admin_url +
        `/charity?charity_section=models&charity_model=${state.management.binding_model_ids[0]}`,
    );
    const provider = page.getByLabel(adminZh.admin.charity.provider, { exact: true });
    await expect(provider).toBeVisible();
    await visuals(page, 'zh-admin-charity-basic', provider);
    await page
      .getByRole('button', { name: commonZh.common.connections.editBindingSources, exact: true })
      .first()
      .click();
    const connection = page.getByRole('dialog', {
      name: new RegExp('^' + commonZh.common.connections.editBindingSources),
    });
    await visuals(page, 'zh-admin-edit-connection', connection);
    await connection
      .getByRole('button', { name: commonZh.common.connections.returnToBindings, exact: true })
      .click();
    const personal = await owner.newPage();
    await personal.goto(state.user_url + '/models');
    const title = userZh.user.core['models.create'];
    await personal.getByRole('button', { name: title, exact: true }).click();
    const dialog = personal.getByRole('dialog', { name: title, exact: true });
    await dialog.locator('.model-types-field__more > summary').click();
    await visuals(personal, 'zh-personal-all-interfaces', dialog.locator('.model-types-field'));
  } finally {
    await admin.close();
    await owner.close();
  }
});

test('gacha management groups prices and lays out one, two and five member rosters', async ({
  browser,
}) => {
  const state = fixture(),
    admin = await session(browser, 'admin');
  try {
    const page = await admin.newPage();
    await page.goto(state.admin_url + '/activities/gacha');
    await page.getByRole('tab', { name: 'Rules and shop', exact: true }).click();
    const costs = page.getByRole('group', { name: 'Chibi card costs', exact: true });
    await visuals(page, 'gacha-shop-groups', costs, async (width) => {
      const n = await costs.getByLabel('N shards per chibi card').boundingBox();
      const r = await costs.getByLabel('R shards per chibi card').boundingBox();
      expect(n && r).toBeTruthy();
      if (width === 1440) expect(Math.abs(n!.y - r!.y)).toBeLessThan(2);
      else expect(r!.y).toBeGreaterThan(n!.y);
    });
    await page.getByRole('tab', { name: 'Limited pools', exact: true }).click();
    await page.getByRole('button', { name: 'New pool', exact: true }).click();
    const dialog = page.getByRole('dialog', { name: 'Limited pool', exact: true });
    for (const count of [1, 2, 5]) {
      const groups = await dialog.locator('.gacha-admin-roster').all();
      const match = (
        await Promise.all(
          groups.map(async (item) => ({
            item,
            count: await item.locator(':scope > div > label').count(),
          })),
        )
      ).find((item) => item.count === count);
      expect(match, `catalog includes a ${count}-member theme`).toBeTruthy();
      await visuals(page, `gacha-roster-${count}`, match!.item, async (width) => {
        const grid = match!.item.locator(':scope > div');
        expect(await grid.evaluate((node) => node.scrollWidth <= node.clientWidth)).toBe(true);
        const cards = await grid.locator(':scope > label').evaluateAll((nodes) =>
          nodes.map((node) => {
            const bounds = node.getBoundingClientRect();
            return { y: Math.round(bounds.y), width: bounds.width };
          }),
        );
        expect(cards.every((card) => card.width <= (width === 390 ? 160 : 192))).toBe(true);
        if (width === 1440) expect(new Set(cards.map((card) => card.y)).size).toBe(1);
      });
    }
    await dialog.getByRole('button', { name: 'Close', exact: true }).first().click();
  } finally {
    await admin.close();
  }
});
