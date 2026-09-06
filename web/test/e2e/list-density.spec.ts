import { resolve } from 'node:path';
import { expect, test, type Page } from './test';
import { ADMIN_ORIGIN, USER_ORIGIN } from './ports';
import {
  assertResponsiveOperationTables,
  collectConsoleViolations,
  mockJson,
  mockPublicConfig,
  mockRoleSession,
  userSession,
} from './support';

type Locale = 'en' | 'zh';
const NOW = 1_800_000_000;

async function prepare(
  page: Page,
  station: 'user' | 'admin',
  locale: Locale = 'en',
  level5 = false,
) {
  const guard = collectConsoleViolations(page);
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.addInitScript(
    ({ locale }) => {
      localStorage.setItem('nb.lang', locale);
      localStorage.setItem('nb.theme', locale === 'zh' ? 'dark' : 'light');
    },
    { locale },
  );
  await mockPublicConfig(page, station);
  await mockRoleSession(page, station, level5 ? 'level5' : station);
  if (station === 'user') {
    const session = userSession(level5 ? 'level5' : 'user');
    session.user.lang = locale;
    for (const path of ['/api/session', '/api/me'])
      await mockJson(page, { origin: USER_ORIGIN, method: 'GET', path, body: session });
  } else {
    await mockJson(page, {
      origin: ADMIN_ORIGIN,
      method: 'GET',
      path: '/admin/api/maintenance',
      body: { enabled: false, revision: '1' },
    });
  }
  return guard;
}

async function screenshot(page: Page, name: string) {
  if (process.env.NONBIRI_VISUAL_DIR)
    await page.screenshot({
      path: resolve(process.env.NONBIRI_VISUAL_DIR, `${name}.png`),
      fullPage: true,
    });
}

async function fitsPage(page: Page) {
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
}

function publicModel(index: number) {
  const token = index % 2 === 0;
  const provider = 'Provider-' + 'p'.repeat(index === 2 ? 50 : 4);
  const model = `model-${index}-` + 'm'.repeat(index === 2 ? 50 : 4);
  return {
    id: String(index),
    provider,
    model,
    full_name: `[公益]${provider}/${model}`,
    pricing: token
      ? {
          mode: 'per_token',
          user_price_milli: null,
          discounted_user_price_milli: null,
          user_prices_milli: {
            uncached_input: '10000000',
            cache_write_input: '10000000',
            cache_read_input: '1600000',
            output: '30000000',
          },
          discounted_user_prices_milli: {
            uncached_input: '8000000',
            cache_write_input: '8000000',
            cache_read_input: '1280000',
            output: '24000000',
          },
        }
      : {
          mode: 'per_request',
          user_price_milli: '123456789012',
          discounted_user_price_milli: '98765431210',
          user_prices_milli: null,
          discounted_user_prices_milli: null,
        },
    discount: {
      enabled: true,
      percent: 80,
      start_at: index % 3 === 0 ? NOW + 3600 : NOW - 3600,
      end_at: NOW + 7200,
    },
  };
}

test('donation guidance renders Markdown without overflowing mobile or desktop pages', async ({
  page,
}) => {
  const guard = await prepare(page, 'user');
  const markdown =
    '# Donation guide\n\n- **Read first**\n- Use `model/name`\n\n[Documentation](https://example.test/help)\n\n| Model | Details |\n| --- | --- |\n| Example | ' +
    'long'.repeat(70) +
    ' |\n\n```text\n' +
    'x'.repeat(400) +
    '\n```';
  await mockPublicConfig(page, 'user', { charity_donation_notice_en: markdown });
  await mockJson(page, {
    origin: USER_ORIGIN,
    method: 'GET',
    path: '/api/charity/models',
    body: {
      state: 'available',
      donation_intake: 'open',
      server_now: NOW,
      models: [publicModel(1)],
    },
  });
  for (const path of ['/api/donations?limit=100', '/api/endpoints?limit=100'])
    await mockJson(page, {
      origin: USER_ORIGIN,
      method: 'GET',
      path,
      body: { data: [], next_cursor: null },
    });
  await page.goto(`${USER_ORIGIN}/charity?tab=donate`);
  await expect(page.getByRole('heading', { name: 'Donation guide', exact: true })).toBeVisible();
  await expect(page.locator('.economy-donation-notice strong')).toHaveText('Read first');
  for (const width of [320, 390, 1935]) {
    await page.setViewportSize({ width, height: 1000 });
    await fitsPage(page);
    await expect(page.locator('.economy-donation-notice pre')).toBeVisible();
    expect(
      await page
        .locator('.economy-donation-notice th')
        .first()
        .evaluate((element) => element.getBoundingClientRect().width),
    ).toBeGreaterThan(45);
    await screenshot(page, `markdown-guidance-${width}`);
  }
  guard.assertNone();
});

test('administrator source grouping accepts current key limits on narrow and wide screens', async ({
  page,
}) => {
  const guard = await prepare(page, 'admin');
  await mockJson(page, {
    origin: ADMIN_ORIGIN,
    method: 'GET',
    path: '/admin/api/donations?limit=50',
    body: { data: [managedDonation(9, 'admin')], next_cursor: null },
  });
  await page.goto(`${ADMIN_ORIGIN}/charity`);
  await page.getByRole('tab', { name: 'Browse by source' }).click();
  await expect(page.getByText('Maximum RPM: 45', { exact: true })).toBeVisible();
  for (const width of [320, 1935]) {
    await page.setViewportSize({ width, height: 1000 });
    await fitsPage(page);
    await expect(page.getByText('Maximum concurrency: 3', { exact: true })).toBeVisible();
    await screenshot(page, `source-group-limits-${width}`);
  }
  guard.assertNone();
});

for (const locale of ['en', 'zh'] as const) {
  test(`multiple charity prices remain compact, readable and searchable in ${locale}`, async ({
    page,
  }) => {
    const guard = await prepare(page, 'user', locale);
    const models = Array.from({ length: 12 }, (_, i) => publicModel(i + 1));
    await mockJson(page, {
      origin: USER_ORIGIN,
      method: 'GET',
      path: '/api/charity/models',
      body: { state: 'available', donation_intake: 'closed', server_now: NOW, models },
    });
    await mockJson(page, {
      origin: USER_ORIGIN,
      method: 'GET',
      path: '/api/donations?limit=100',
      body: { data: [], next_cursor: null },
    });
    await page.setViewportSize({ width: 1935, height: 1000 });
    await page.goto(`${USER_ORIGIN}/charity`);
    const cards = page.locator('.economy-model-list > li');
    await expect(cards).toHaveCount(12);
    await screenshot(page, `prices-${locale}-desktop`);
    expect((await cards.first().boundingBox())!.height).toBeLessThan(400);
    const heading = (await cards.first().locator('.economy-model-heading').boundingBox())!;
    const prices = (await cards.first().locator('.charity-price-wrap').boundingBox())!;
    expect(prices.y - heading.y - heading.height).toBeLessThanOrEqual(20);
    const firstRow = cards.first().locator('tbody tr');
    expect((await firstRow.boundingBox())!.height).toBeLessThan(80);
    for (const width of [320, 390, 768, 1119, 1120, 1121, 1935]) {
      await page.setViewportSize({ width, height: 1000 });
      await fitsPage(page);
      const clipped = await page.locator('.charity-price-table').evaluateAll((tables) =>
        tables.some((table) => {
          const wrapper = table.parentElement!;
          const bounds = wrapper.getBoundingClientRect();
          return (
            wrapper.scrollWidth > wrapper.clientWidth + 1 ||
            table.scrollWidth > table.clientWidth + 1 ||
            [...table.querySelectorAll('.charity-amount')].some((amount) => {
              const rect = amount.getBoundingClientRect();
              return (
                rect.left < bounds.left - 1 ||
                rect.right > bounds.right + 1 ||
                amount.scrollWidth > amount.clientWidth + 1
              );
            })
          );
        }),
      );
      expect(clipped).toBe(false);
      if (width === 390) await screenshot(page, `prices-${locale}-mobile`);
    }
    const search = page.getByRole('searchbox', {
      name: locale === 'zh' ? '搜索模型名称' : 'Search model names',
    });
    await search.fill('MODEL-12-');
    await expect(cards).toHaveCount(1);
    await expect(cards).toContainText(models[11].full_name);
    await search.fill('does-not-exist');
    await expect(cards).toHaveCount(0);
    await search.fill('');
    await page
      .getByLabel(locale === 'zh' ? '计价方式' : 'Pricing method', { exact: true })
      .selectOption('per_token');
    await expect(cards).toHaveCount(6);
    await expect(cards.first().locator('tbody tr')).toHaveCount(4);
    guard.assertNone();
  });
}

function endpoint(index: number) {
  return {
    id: String(index),
    connector_type: 'openai-compatible',
    base_url: `https://endpoint-${index}.example.test/v1`,
    origin: { kind: 'custom' },
    note: `Endpoint ${index} — ${'资源说明'.repeat(12)}`,
    enabled: true,
    revision: '1',
    key_count: '61',
    created_at: 1,
    updated_at: 2,
  };
}
function endpointKey(endpointId: string, index: number) {
  return {
    id: String(Number(endpointId) * 1000 + index),
    endpoint_id: endpointId,
    display_head: 'head',
    display_tail: String(index).padStart(4, '0'),
    note: `Key ${index}`,
    enabled: true,
    force_store_false: true,
    suspension_state: 'none',
    revision: '1',
    created_at: 1,
    updated_at: 2,
  };
}
function candidate(endpointId: string, keyId: string, index: number) {
  const ep = endpoint(Number(endpointId));
  return {
    endpoint_key_id: keyId,
    endpoint_base_url: ep.base_url,
    connector_type: ep.connector_type,
    endpoint_note: ep.note,
    endpoint_key_display_head: 'head',
    endpoint_key_display_tail: keyId.slice(-4),
    endpoint_key_note: `Key ${Number(keyId) % 1000}`,
    upstream_model_id: `model-${index}-` + 'm'.repeat(index === 1 ? 490 : 6),
    source_types: ['automatic'],
  };
}
function paged<T>(items: T[], url: URL) {
  return {
    data: url.searchParams.has('cursor') ? items.slice(50) : items.slice(0, 50),
    next_cursor: url.searchParams.has('cursor') || items.length <= 50 ? null : 'bmV4dA',
  };
}

test('many personal endpoints, keys and models preserve cross-page selections without oversized tiles', async ({
  page,
}) => {
  const guard = await prepare(page, 'user');
  let submitted: Record<string, unknown> | undefined;
  let savedBindings: Array<Record<string, unknown>> = [];
  const model = {
    id: '7',
    provider: 'provider',
    model: 'personal',
    full_name: 'provider/personal',
    route_strategy: 'ordered',
    silent_retry: false,
    flatten_tool_calls: false,
    revision: '1',
    binding_revision: '0',
    binding_count: '0',
    created_at: 1,
    updated_at: 2,
  };
  await page.route(`${USER_ORIGIN}/api/models**`, async (route) => {
    const url = new URL(route.request().url());
    if (url.pathname.endsWith('/bindings/batch') && route.request().method() === 'POST') {
      submitted = route.request().postDataJSON();
      savedBindings = (
        submitted!.selections as Array<{ endpoint_key_id: string; upstream_model_id: string }>
      ).map((entry, i) => ({
        ...Object.fromEntries(
          Object.entries(
            candidate(
              String(Math.floor(Number(entry.endpoint_key_id) / 1000)),
              entry.endpoint_key_id,
              1,
            ),
          ).filter(([key]) => key !== 'source_types'),
        ),
        upstream_model_id: entry.upstream_model_id,
        id: String(i + 1),
        ord: i,
      }));
      model.binding_revision = '1';
      model.binding_count = String(savedBindings.length);
      return route.fulfill({ json: { bindings: savedBindings, binding_revision: '1' } });
    } else if (url.pathname.endsWith('/binding-candidates')) {
      const keyId = url.searchParams.get('key_id')!;
      const endpointId = String(Math.floor(Number(keyId) / 1000));
      let items =
        url.searchParams.get('source') === 'manual'
          ? []
          : Array.from({ length: 61 }, (_, i) => candidate(endpointId, keyId, i + 1));
      const query = url.searchParams.get('q');
      if (query) items = items.filter((item) => item.upstream_model_id.includes(query));
      await route.fulfill({ json: paged(items, url) });
    } else if (url.pathname.endsWith('/bindings'))
      await route.fulfill({
        json: { bindings: savedBindings, binding_revision: model.binding_revision },
      });
    else if (url.pathname === '/api/models/7') await route.fulfill({ json: model });
    else if (url.pathname === '/api/models')
      await route.fulfill({ json: { data: [model], next_cursor: null } });
    else await route.fallback();
  });
  await page.route(`${USER_ORIGIN}/api/endpoints**`, async (route) => {
    const url = new URL(route.request().url());
    const keyMatch = url.pathname.match(/^\/api\/endpoints\/(\d+)\/keys$/);
    if (keyMatch)
      await route.fulfill({
        json: paged(
          Array.from({ length: 61 }, (_, i) => endpointKey(keyMatch[1], i + 1)),
          url,
        ),
      });
    else if (url.pathname === '/api/endpoints')
      await route.fulfill({
        json: paged(
          Array.from({ length: 61 }, (_, i) => endpoint(i + 1)),
          url,
        ),
      });
    else await route.fallback();
  });
  await page.setViewportSize({ width: 1935, height: 1000 });
  await page.goto(`${USER_ORIGIN}/models`);
  await page.getByRole('button', { name: 'Manage connections', exact: true }).click();
  const level = page.locator('.core-selector > section:visible');
  await expect(level.locator('.core-choice')).toHaveCount(50);
  await screenshot(page, 'personal-endpoints-desktop');
  expect((await level.locator('.nb-choice-list__items').boundingBox())!.height).toBeLessThanOrEqual(
    514,
  );
  for (const width of [320, 390, 768, 1935]) {
    await page.setViewportSize({ width, height: 1000 });
    await fitsPage(page);
  }
  await level.getByRole('searchbox', { name: 'Filter this page' }).fill('Endpoint 50 —');
  await level.getByRole('button', { name: /Endpoint 50 —/ }).click();
  await level.getByRole('button', { name: 'Next', exact: true }).click();
  await level.getByRole('button', { name: /^Key 61 / }).click();
  const automatic = page.locator('.core-selector__sources > section').first();
  await expect(automatic.locator('.core-choice')).toHaveCount(50);
  await automatic.getByRole('button', { name: /^model-1-/ }).click();
  await automatic.getByRole('button', { name: 'Next models', exact: true }).click();
  await automatic.getByRole('button', { name: /^model-61-/ }).click();
  await page.locator('.core-selector-path button').first().click();
  await level.getByRole('button', { name: 'Next', exact: true }).click();
  await level.getByRole('searchbox', { name: 'Filter this page' }).fill('Endpoint 61 —');
  await level.getByRole('button', { name: /Endpoint 61 —/ }).click();
  await level.getByRole('searchbox', { name: 'Filter this page' }).fill('Key 1');
  await level.getByRole('button', { name: /^Key 1 head/ }).click();
  await automatic.getByRole('button', { name: /^model-2-/ }).click();
  await expect(page.locator('.core-selection-list > li')).toHaveCount(3);
  await page.setViewportSize({ width: 390, height: 1000 });
  await screenshot(page, 'personal-models-mobile');
  await fitsPage(page);
  await page
    .locator('.core-selection-list > li')
    .first()
    .getByRole('button', { name: 'Remove', exact: true })
    .click();
  await expect(page.locator('.core-selection-list > li')).toHaveCount(2);
  await page.getByRole('button', { name: 'Add 2 selected connection(s)', exact: true }).click();
  await expect
    .poll(() => submitted)
    .toEqual({
      expected_binding_revision: '0',
      selections: [
        {
          endpoint_key_id: '50061',
          upstream_model_id: candidate('50', '50061', 61).upstream_model_id,
        },
        {
          endpoint_key_id: '61001',
          upstream_model_id: candidate('61', '61001', 2).upstream_model_id,
        },
      ],
    });
  await expect(page.locator('.core-selection-list > li')).toHaveCount(0);
  guard.assertNone();
});

const managedModel = {
  id: '7',
  provider: 'provider',
  model: 'shared',
  full_name: '[公益]provider/shared',
  enabled: true,
  pricing: { mode: 'per_request', user_price: '1', donor_reward: '0.1' },
  discount: { enabled: false, percent: 100, start_at: null, end_at: null },
  flatten_tool_calls: false,
  revision: '1',
  binding_revision: '0',
  binding_count: '0',
  rolling_success: { sample_count: '0', success_count: '0', percent: null },
  created_at: 1,
  updated_at: 2,
};
function sharedSource(id: number) {
  return {
    connector_type: 'openai-compatible',
    canonical_base_url: `https://endpoint-${id}.example.test/${'long-path/'.repeat(12)}v1`,
    display_head: 'head',
    display_tail: `tail${id}`,
    max_concurrency: 3,
    max_rpm: 45,
  };
}
function managedDonation(id: number, role: 'admin' | 'steward') {
  return {
    id: String(id),
    status: 'pending',
    revision: '1',
    description: `Donation ${id} — ${'Shared instructions with a long description. '.repeat(6)}`,
    review_result: null,
    keys: [
      {
        id: String(id * 1000 + 1),
        endpoint_key_id: String(id),
        display_head: 'head',
        display_tail: 'tail',
        safe_source: {
          kind: 'custom',
          base_url: 'https://example.test/v1',
          connector_type: 'openai-compatible',
        },
        physical_enabled: true,
        charity_state: 'pending',
        limits: { price: null, calls: null, tokens: null },
        usage: {
          price_used: '0',
          price_inflight: '0',
          calls_used: '0',
          calls_inflight: '0',
          tokens_used: '0',
          tokens_inflight: '0',
        },
        token_reserve: 32,
        authorized_expires_at: null,
        expires_at: null,
        streak: { generation: '1', count: '0', failure_disabled: false },
        ended_reason: null,
        safe_note: 'Reviewed note',
        max_concurrency: 3,
        max_rpm: 45,
      },
    ],
    owner: {
      user_id: '1',
      display_name: 'A donor with a long display name',
      ...(role === 'admin' ? { discord_id: '123456789012345678' } : {}),
    },
    reviewer: null,
    created_at: 1,
    updated_at: 2,
  };
}

for (const role of ['admin', 'steward'] as const)
  test(`${role} review IDs and many shared connections stay readable and retain atomic selections`, async ({
    page,
  }) => {
    const station = role === 'admin' ? 'admin' : 'user';
    const origin = role === 'admin' ? ADMIN_ORIGIN : USER_ORIGIN;
    const base = role === 'admin' ? '/admin/api' : '/api/steward';
    const guard = await prepare(page, station, 'en', role === 'steward');
    let submitted: Record<string, unknown> | undefined;
    let bindings: Array<Record<string, unknown>> = [];
    const donation = managedDonation(9, role);
    await page.route(`${origin}${base}/**`, async (route) => {
      const request = route.request();
      const url = new URL(request.url());
      const path = url.pathname.slice(base.length);
      const currentModel = {
        ...managedModel,
        binding_count: String(bindings.length),
        binding_revision: bindings.length ? '1' : '0',
      };
      if (path === '/logs') return route.fulfill({ json: { data: [], next_cursor: null } });
      if (path === '/donations')
        return route.fulfill({ json: { data: [donation], next_cursor: null } });
      if (path === '/donations/9') return route.fulfill({ json: donation });
      if (path === '/charity-models')
        return route.fulfill({ json: { data: [currentModel], next_cursor: null } });
      if (path === '/charity-models/7') return route.fulfill({ json: currentModel });
      if (path === '/charity-models/7/bindings')
        return route.fulfill({ json: { bindings, binding_revision: bindings.length ? '1' : '0' } });
      if (path === '/charity-models/7/binding-donations')
        return route.fulfill({
          json: paged(
            Array.from({ length: 61 }, (_, i) => ({
              id: String(i + 1),
              description: `Donation ${i + 1} — ${'Clear instructions. '.repeat(12)}`,
              key_count: 61,
            })),
            url,
          ),
        });
      const keysMatch = path.match(/^\/charity-models\/7\/binding-donations\/(\d+)\/keys$/);
      if (keysMatch)
        return route.fulfill({
          json: paged(
            Array.from({ length: 61 }, (_, i) => ({
              donation_key_id: String(Number(keysMatch[1]) * 1000 + i + 1),
              source: sharedSource(Number(keysMatch[1])),
              note: `Reviewed key ${i + 1} — ${'long note '.repeat(12)}`,
            })),
            url,
          ),
        });
      if (path === '/charity-models/7/binding-candidates') {
        const donationId = url.searchParams.get('donation_id')!;
        const keyId = url.searchParams.get('donation_key_id')!;
        expect(donationId).toBeTruthy();
        expect(keyId).toBeTruthy();
        return route.fulfill({
          json: paged(
            Array.from({ length: 61 }, (_, i) => ({
              donation_id: donationId,
              donation_key_id: keyId,
              upstream_model_id: `shared-model-${i + 1}-${'m'.repeat(i === 0 ? 440 : 20)}`,
              source: sharedSource(Number(donationId)),
              source_types: ['automatic'],
            })),
            url,
          ),
        });
      }
      if (path === '/charity-models/7/bindings/batch' && request.method() === 'POST') {
        submitted = request.postDataJSON();
        const selections = submitted!.selections as Array<{
          donation_key_id: string;
          upstream_model_id: string;
        }>;
        bindings = selections.map((entry, i) => ({
          ...entry,
          id: String(i + 1),
          ord: i,
          donation_id: String(Math.floor(Number(entry.donation_key_id) / 1000)),
          source: sharedSource(Math.floor(Number(entry.donation_key_id) / 1000)),
          source_types: ['automatic'],
        }));
        return route.fulfill({ json: { bindings, binding_revision: '1' } });
      }
      return route.fallback();
    });
    await page.setViewportSize({ width: 1935, height: 1000 });
    await page.goto(`${origin}/${role === 'admin' ? 'charity' : 'steward'}`);
    if (role === 'steward') await page.getByRole('tab', { name: 'Charity management' }).click();
    const reviewRow = page
      .locator('.ops-table tbody tr')
      .filter({ has: page.getByText(donation.description, { exact: true }) });
    await expect(reviewRow).toBeVisible();
    await expect(reviewRow.locator('td').first()).toHaveText('9');
    await expect(reviewRow.locator('td').nth(1)).toHaveText(donation.description);
    for (const width of [320, 390, 768, 959, 960, 961, 1935]) {
      await page.setViewportSize({ width, height: 1000 });
      await fitsPage(page);
      expect(
        await reviewRow.evaluate((row) =>
          [...row.querySelectorAll('td')].every((cell) => cell.scrollWidth <= cell.clientWidth + 1),
        ),
      ).toBe(true);
    }
    await page.setViewportSize({ width: 390, height: 1000 });
    await screenshot(page, `${role}-review-mobile`);
    await page.getByRole('tab', { name: /Charity models/ }).click();
    await page
      .locator('.ops-table tbody tr')
      .filter({ has: page.getByText(managedModel.full_name, { exact: true }) })
      .getByRole('button', { name: 'Manage', exact: true })
      .click();
    const picker = page.locator('.ops-binding-picker');
    await expect(picker.locator('.ops-picker-choice')).toHaveCount(50);
    await picker.getByRole('button', { name: /Donation #1 / }).click();
    await picker.getByRole('button', { name: /^Reviewed key 1 —/ }).click();
    await picker.getByRole('checkbox', { name: /shared-model-1-/ }).check();
    await picker.getByRole('button', { name: 'Next', exact: true }).click();
    await picker.getByRole('checkbox', { name: /shared-model-61-/ }).check();
    await picker.getByRole('button', { name: '1 · Choose donation' }).click();
    await picker.getByRole('button', { name: 'Next', exact: true }).click();
    await picker.getByRole('button', { name: /Donation #61 / }).click();
    await picker.getByRole('button', { name: 'Next', exact: true }).click();
    await picker.getByRole('button', { name: /^Reviewed key 61 —/ }).click();
    await picker.getByRole('checkbox', { name: /shared-model-2-/ }).check();
    await expect(picker.locator('.ops-picker-selection li')).toHaveCount(3);
    for (const width of [320, 390, 768, 1935]) {
      await page.setViewportSize({ width, height: 1000 });
      await fitsPage(page);
    }
    await screenshot(page, `${role}-shared-selected-desktop`);
    const editor = picker.locator('..');
    await editor.getByRole('button', { name: /Add selected/ }).click();
    await expect
      .poll(() => submitted)
      .toMatchObject({
        expected_binding_revision: '0',
        selections: [
          { donation_key_id: '1001', upstream_model_id: `shared-model-1-${'m'.repeat(440)}` },
          { donation_key_id: '1001', upstream_model_id: `shared-model-61-${'m'.repeat(20)}` },
          { donation_key_id: '61061', upstream_model_id: `shared-model-2-${'m'.repeat(20)}` },
        ],
      });
    expect(Object.keys(submitted!)).toEqual(['expected_binding_revision', 'selections']);
    await expect(page.locator('.ops-picker-selection li')).toHaveCount(0);
    guard.assertNone();
  });

test('donation resources can be filtered across many endpoints without losing selected keys or expiry', async ({
  page,
}) => {
  const guard = await prepare(page, 'user');
  let submitted: Record<string, unknown> | undefined;
  let saved: Record<string, unknown> | undefined;
  await mockJson(page, {
    origin: USER_ORIGIN,
    method: 'GET',
    path: '/api/charity/models',
    body: {
      state: 'available',
      donation_intake: 'open',
      server_now: NOW,
      models: [publicModel(1)],
    },
  });
  await page.route(`${USER_ORIGIN}/api/donations**`, async (route) => {
    if (route.request().method() === 'POST') {
      submitted = route.request().postDataJSON();
      const selections = submitted!.keys as Array<{
        endpoint_key_id: string;
        expires_at: number | null;
      }>;
      const template = managedDonation(99, 'admin');
      saved = {
        id: '99',
        status: 'pending',
        revision: '1',
        description: submitted!.description,
        review_result: null,
        created_at: NOW,
        updated_at: NOW,
        keys: selections.map((selection, i) => ({
          ...Object.fromEntries(
            Object.entries(template.keys[0]).filter(
              ([key]) =>
                !['safe_note', 'authorized_expires_at', 'max_concurrency', 'max_rpm'].includes(key),
            ),
          ),
          id: String(i + 1),
          endpoint_key_id: selection.endpoint_key_id,
          expires_at: selection.expires_at,
          token_reserve: 0,
        })),
      };
      return route.fulfill({ status: 201, json: saved });
    }
    return route.fulfill({ json: { data: saved ? [saved] : [], next_cursor: null } });
  });
  await page.route(`${USER_ORIGIN}/api/endpoints**`, async (route) => {
    const url = new URL(route.request().url());
    const match = url.pathname.match(/^\/api\/endpoints\/(\d+)\/keys$/);
    const data = match
      ? Array.from({ length: 3 }, (_, i) => ({
          ...endpointKey(match[1], i + 1),
          max_concurrency: 2,
          max_rpm: 12,
        }))
      : Array.from({ length: 20 }, (_, i) => ({ ...endpoint(i + 1), key_count: '3' }));
    return route.fulfill({ json: { data, next_cursor: null } });
  });
  await page.setViewportSize({ width: 1935, height: 1000 });
  await page.goto(`${USER_ORIGIN}/charity?tab=donate`);
  const groups = page.locator('.economy-key-group');
  await expect(groups).toHaveCount(20);
  await expect(groups.locator('input[type=checkbox]:visible')).toHaveCount(0);
  const filter = page.getByRole('searchbox', { name: 'Find endpoints or key notes' });
  await filter.fill('Endpoint 20 —');
  await expect(groups).toHaveCount(1);
  const choice = page.locator('.economy-key-choice').first();
  await choice.getByRole('checkbox').check();
  const expiry = choice.locator('input[type=datetime-local]');
  await expiry.fill('2027-02-01T12:00');
  await expect(choice.getByRole('checkbox')).toBeChecked();
  await filter.fill('Endpoint 1 —');
  await page.locator('.economy-key-choice').nth(1).getByRole('checkbox').check();
  await filter.fill('Endpoint 20 —');
  await expect(page.locator('.economy-key-choice').first().getByRole('checkbox')).toBeChecked();
  await expect(
    page.locator('.economy-key-choice').first().locator('input[type=datetime-local]'),
  ).toHaveValue('2027-02-01T12:00');
  await filter.fill('');
  for (const width of [320, 390, 768, 1935]) {
    await page.setViewportSize({ width, height: 1000 });
    await fitsPage(page);
    expect(
      (await page.locator('.economy-resource-groups').boundingBox())!.height,
    ).toBeLessThanOrEqual(602);
  }
  await page.setViewportSize({ width: 390, height: 1000 });
  await screenshot(page, 'donation-resources-mobile');
  await page
    .getByLabel('Donation description')
    .fill('A helpful description for the shared resources');
  await page.locator('.economy-authorization input').check();
  await page.getByRole('button', { name: 'Submit for review', exact: true }).click();
  await expect
    .poll(() => submitted)
    .toEqual({
      description: 'A helpful description for the shared resources',
      keys: [
        { endpoint_key_id: '20001', expires_at: Date.parse('2027-02-01T12:00:00Z') / 1000 },
        { endpoint_key_id: '1002', expires_at: null },
      ],
      ownership_authorized: true,
    });
  await expect(page.getByText('Donation submitted for review.', { exact: true })).toBeVisible();
  guard.assertNone();
});

for (const locale of ['en', 'zh'] as const)
  test(`administrative references and expanded endpoint users remain readable in ${locale}`, async ({
    page,
  }) => {
    const guard = await prepare(page, 'admin', locale);
    const endpointURL = `https://api.example.test/${'long-resource-path/'.repeat(12)}v1`;
    await mockJson(page, {
      origin: ADMIN_ORIGIN,
      method: 'GET',
      path: '/admin/api/overview/endpoints?limit=50',
      body: {
        data: [
          {
            base_url: endpointURL,
            user_count: '2',
            endpoint_count: '12',
            key_count: '120',
            users: [
              {
                user_id: '9223372036854775807',
                endpoint_count: '6',
                key_count: '60',
                enabled_count: '5',
              },
              { user_id: '2', endpoint_count: '6', key_count: '60', enabled_count: '6' },
            ],
          },
        ],
        next_cursor: null,
      },
    });
    await page.goto(`${ADMIN_ORIGIN}/endpoints`);
    await page.locator('.ops-table tbody button').first().click();
    await expect(page.locator('.ops-table .ops-table')).toHaveCount(1);
    await assertResponsiveOperationTables(page);
    await mockJson(page, {
      origin: ADMIN_ORIGIN,
      method: 'GET',
      path: '/admin/api/alerts?resolved=false&limit=50',
      body: {
        data: [
          {
            id: '1',
            kind: 'forward_error',
            message: 'An upstream connection ended early. '.repeat(12),
            ref: 'reference-' + 'r'.repeat(200),
            subject_user_id: '9223372036854775807',
            created_at: NOW,
            resolved: false,
            resolved_at: null,
          },
        ],
        next_cursor: null,
      },
    });
    await page.goto(`${ADMIN_ORIGIN}/alerts`);
    await expect(page.locator('.ops-table tbody tr')).toHaveCount(1);
    await assertResponsiveOperationTables(page);
    await mockJson(page, {
      origin: ADMIN_ORIGIN,
      method: 'GET',
      path: '/admin/api/site-config',
      body: { revision: '1', values: {} },
    });
    await mockJson(page, {
      origin: ADMIN_ORIGIN,
      method: 'GET',
      path: '/admin/api/site-config/catalog',
      body: { data: [] },
    });
    const objectRef = `rpc_${'A'.repeat(22)}`;
    await mockJson(page, {
      origin: ADMIN_ORIGIN,
      method: 'GET',
      path: '/admin/api/legal-holds?limit=50',
      body: {
        data: [
          {
            id: `lgh_${'A'.repeat(22)}`,
            object_kind: 'report_case',
            object_ref: objectRef,
            state: 'active',
            revision: '1',
            created_at: NOW,
            expires_at: NOW + 86400,
            ended_at: null,
          },
        ],
        next_cursor: null,
      },
    });
    await page.goto(`${ADMIN_ORIGIN}/settings`);
    await expect(page.locator('td').filter({ hasText: objectRef })).toHaveText(objectRef);
    await assertResponsiveOperationTables(page);
    guard.assertNone();
  });
