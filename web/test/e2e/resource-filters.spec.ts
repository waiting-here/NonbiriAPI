import { numberedPage } from './numbered-fixtures';
import { USER_ORIGIN } from './ports';
import { collectConsoleViolations, mockPublicConfig, mockRoleSession } from './support';
import { expect, test } from './test';

const endpoints = Array.from({ length: 23 }, (_, index) => ({
  id: String(index + 1),
  connection_id: '19',
  connector_type: 'openai-compatible',
  configuration_type: 'openai-compatible',
  native_services: ['chat_completions'],
  rerank_format: '',
  base_url: 'https://resources.example.test/' + (index + 1) + '/v1',
  origin: { kind: 'custom' },
  note: 'Needle resource ' + (index + 1),
  enabled: true,
  revision: '1',
  key_count: index === 18 ? '12' : '0',
  created_at: 1_700_000_000,
  updated_at: 1_700_000_001,
}));
const connections = Array.from({ length: 23 }, (_, index) => ({
  id: String(index + 1),
  name: 'Needle connection ' + (index + 1),
  note: '',
  revision: '1',
  entry_count: index === 18 ? String(endpoints.length) : '0',
  credential_count: index === 18 ? '12' : '0',
  created_at: 1_700_000_000,
  updated_at: 1_700_000_001,
}));

const keys = Array.from({ length: 12 }, (_, index) => ({
  id: String(101 + index),
  endpoint_id: '19',
  display_head: 'sk-fixture',
  display_tail: 'tail',
  note: 'masked-tail key ' + (index + 1),
  enabled: true,
  force_store_false: false,
  max_concurrency: 0,
  max_rpm: 0,
  suspension_state: 'security_processing',
  revision: '1',
  created_at: 1_700_000_000,
  updated_at: 1_700_000_001,
}));

for (const locale of ['en', 'zh'] as const) {
  test(`resource filters preserve numbered detail navigation at 320px ${locale}`, async ({
    page,
  }) => {
    const guard = collectConsoleViolations(page);
    await page.setViewportSize({ width: 320, height: 780 });
    await page.addInitScript((lang) => {
      localStorage.setItem('nb.lang', lang);
      localStorage.setItem('nb.theme', 'dark');
    }, locale);
    await mockPublicConfig(page, 'user');
    await mockRoleSession(page, 'user', 'user');
    const connectionSearches: URLSearchParams[] = [];
    const entrySearches: URLSearchParams[] = [];
    const keySearches: URLSearchParams[] = [];
    await page.route(USER_ORIGIN + '/api/**', async (route) => {
      const url = new URL(route.request().url());
      const q = (url.searchParams.get('q') ?? '').toLowerCase();
      let body: unknown;
      if (url.pathname === '/api/connections') {
        connectionSearches.push(url.searchParams);
        body = numberedPage(
          connections.filter((row) => (row.name + ' ' + row.note).toLowerCase().includes(q)),
          url.searchParams,
        );
      } else if (/^\/api\/connections\/[0-9]+$/.test(url.pathname)) {
        body = connections.find((row) => row.id === url.pathname.split('/').pop());
      } else if (url.pathname === '/api/connections/19/entries') {
        entrySearches.push(url.searchParams);
        body = numberedPage(
          endpoints.filter((row) => (row.note + ' ' + row.base_url).toLowerCase().includes(q)),
          url.searchParams,
        );
      } else if (/^\/api\/endpoints\/[0-9]+$/.test(url.pathname)) {
        body = endpoints.find((row) => row.id === url.pathname.split('/').pop());
      } else if (/^\/api\/endpoints\/[0-9]+\/keys$/.test(url.pathname)) {
        keySearches.push(url.searchParams);
        const enabled = url.searchParams.get('enabled');
        const security = url.searchParams.get('suspension_state');
        const rows = keys.filter(
          (key) =>
            key.note.toLowerCase().includes(q) &&
            (!enabled || String(key.enabled) === enabled) &&
            (!security || key.suspension_state === security) &&
            url.searchParams.get('donated') !== 'false',
        );
        body = numberedPage(rows, url.searchParams);
      } else {
        await route.fallback();
        return;
      }
      await route.fulfill({ json: body });
    });
    const searchLabel = locale === 'en' ? 'Search' : '搜索';
    const connectionList = page.locator('section.nb-panel').filter({
      has: page.getByRole('heading', {
        name: locale === 'en' ? 'Your connections' : '我的连接',
        exact: true,
      }),
    });
    const entryList = page.locator('section.nb-panel').filter({
      has: page.getByRole('heading', {
        name: locale === 'en' ? 'Entries' : '入口',
        exact: true,
      }),
    });
    await page.goto(USER_ORIGIN + '/endpoints?connections_page=2&connections_page_size=10');
    await expect(
      page.getByRole('link', { name: 'Needle connection 11', exact: true }),
    ).toBeVisible();
    await connectionList.getByRole('searchbox').fill(' Needle ');
    await connectionList.getByRole('button', { name: searchLabel, exact: true }).click();
    await expect.poll(() => new URL(page.url()).searchParams.get('connections_page')).toBe('1');
    await expect.poll(() => new URL(page.url()).searchParams.get('connections_q')).toBe('Needle');
    await connectionList.getByRole('button', { name: '2', exact: true }).click();
    await page.getByRole('link', { name: 'Needle connection 19', exact: true }).click();
    await expect(
      page.getByRole('heading', { name: 'Needle connection 19', exact: true }),
    ).toBeVisible();
    expect(new URL(page.url()).searchParams.get('connection_id')).toBe('19');
    expect(new URL(page.url()).searchParams.get('connection_view')).toBe('entries');
    await entryList.getByRole('searchbox').fill(' resource ');
    await entryList.getByRole('button', { name: searchLabel, exact: true }).click();
    await entryList
      .getByRole('combobox', {
        name: locale === 'en' ? 'Items per page' : '每页条数',
      })
      .selectOption('10');
    await expect(page.getByText('Needle resource 1', { exact: true })).toBeVisible();
    await entryList
      .getByRole('button', {
        name: locale === 'en' ? 'Next' : '下一页',
        exact: true,
      })
      .click();
    await expect(page.getByText('Needle resource 11', { exact: true })).toBeVisible();
    await expect
      .poll(() => new URL(page.url()).searchParams.get('connection-entries_page'))
      .toBe('2');
    const entryReturnURL = page.url();
    await page
      .locator('a[href="/endpoints/19"]')
      .evaluate((link) => link.scrollIntoView({ block: 'center', behavior: 'instant' }));
    const before = await page.evaluate(() => scrollY);
    expect(before).toBeGreaterThan(0);
    await page.locator('a[href="/endpoints/19"]').click();
    await expect(
      page.getByRole('heading', { name: 'Needle resource 19', exact: true }),
    ).toBeVisible();
    const keyFilters = page.getByRole('form', {
      name: locale === 'en' ? 'Resource filters' : '资源筛选',
    });
    await keyFilters.getByRole('searchbox').fill('masked-tail');
    await keyFilters.locator('summary').click();
    await keyFilters.getByRole('button', { name: searchLabel, exact: true }).click();
    await keyFilters
      .getByRole('combobox', {
        name: locale === 'en' ? 'Enabled state' : '启用状态',
      })
      .selectOption('true');
    await keyFilters
      .getByRole('combobox', {
        name: locale === 'en' ? 'Donated' : '已捐赠',
        exact: true,
      })
      .selectOption('true');
    await keyFilters
      .getByRole('combobox', {
        name: locale === 'en' ? 'Security status' : '安全状态',
      })
      .selectOption('security_processing');
    await expect
      .poll(() =>
        keySearches.some(
          (p) =>
            p.get('q') === 'masked-tail' &&
            p.get('enabled') === 'true' &&
            p.get('donated') === 'true' &&
            p.get('suspension_state') === 'security_processing' &&
            p.get('page') === '1',
        ),
      )
      .toBe(true);
    await page
      .getByRole('link', {
        name: locale === 'en' ? 'My services' : '我的服务',
        exact: true,
      })
      .click();
    await expect(page).toHaveURL(entryReturnURL);
    await expect(entryList.getByRole('searchbox')).toHaveValue('resource');
    expect(new URL(page.url()).searchParams.get('connections_q')).toBe('Needle');
    expect(new URL(page.url()).searchParams.get('connections_page')).toBe('2');
    expect(new URL(page.url()).searchParams.get('connection-entries_page_size')).toBe('10');
    await expect
      .poll(async () => Math.abs((await page.evaluate(() => scrollY)) - before))
      .toBeLessThan(100);
    await page.reload();
    await expect(entryList.getByRole('searchbox')).toHaveValue('resource');
    await entryList.getByRole('searchbox').fill('absent');
    await entryList.getByRole('button', { name: searchLabel, exact: true }).click();
    await expect(
      entryList.getByText(
        locale === 'en'
          ? 'No matches. Adjust your search or filters.'
          : '没有匹配结果，可调整搜索或筛选。',
        { exact: true },
      ),
    ).toBeVisible();
    await entryList.getByRole('searchbox').fill('');
    await entryList.getByRole('button', { name: searchLabel, exact: true }).click();
    await expect(page.getByText('Needle resource 1', { exact: true })).toBeVisible();
    await expect
      .poll(() => new URL(page.url()).searchParams.get('connection-entries_q'))
      .toBeNull();
    await page
      .getByRole('link', {
        name: locale === 'en' ? 'Back to connections' : '返回连接列表',
        exact: true,
      })
      .click();
    await expect(connectionList.getByRole('searchbox')).toHaveValue('Needle');
    await expect(
      page.getByRole('link', { name: 'Needle connection 11', exact: true }),
    ).toBeVisible();
    expect(new URL(page.url()).searchParams.get('connection_id')).toBeNull();
    expect(new URL(page.url()).searchParams.get('connections_page')).toBe('2');
    expect(new URL(page.url()).searchParams.get('connections_page_size')).toBe('10');
    await page.reload();
    await expect(connectionList.getByRole('searchbox')).toHaveValue('Needle');
    await connectionList.getByRole('searchbox').fill('absent');
    await connectionList.getByRole('button', { name: searchLabel, exact: true }).click();
    await expect(
      connectionList.getByText(
        locale === 'en'
          ? 'No matches. Adjust your search or filters.'
          : '没有匹配结果，可调整搜索或筛选。',
        { exact: true },
      ),
    ).toBeVisible();
    await connectionList.getByRole('searchbox').fill('');
    await connectionList.getByRole('button', { name: searchLabel, exact: true }).click();
    await expect(
      page.getByRole('link', { name: 'Needle connection 1', exact: true }),
    ).toBeVisible();
    expect(
      connectionSearches.some(
        (p) => p.get('q') === 'Needle' && p.get('page') === '2' && p.get('page_size') === '10',
      ),
    ).toBe(true);
    expect(
      entrySearches.some(
        (p) => p.get('q') === 'resource' && p.get('page') === '2' && p.get('page_size') === '10',
      ),
    ).toBe(true);
    expect([...connectionSearches, ...entrySearches].every((p) => !p.has('source'))).toBe(true);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
      true,
    );
    await page.screenshot({
      path: '../tmp/resource-filters-' + locale + '-320-dark.png',
      fullPage: true,
    });
    await connectionList.getByRole('search').scrollIntoViewIfNeeded();
    await page.screenshot({ path: '../tmp/resource-filters-' + locale + '-viewport.png' });
    guard.assertNone();
  });
}

test('personal models send all filters before pagination and preserve them through details', async ({
  page,
}) => {
  const guard = collectConsoleViolations(page);
  await mockPublicConfig(page, 'user');
  await mockRoleSession(page, 'user', 'user');
  const model = {
    model_types: ['chat_completions', 'embeddings'],
    id: '7',
    provider: 'Vendor',
    model: 'Logical',
    full_name: 'Vendor/Logical',
    route_strategy: 'ordered',
    silent_retry: false,
    transport_rule: 'passthrough',
    cloudflare_proxy_enabled: false,
    cloudflare_proxy_mode: 'direct',
    flatten_tool_calls: false,
    revision: '1',
    binding_revision: '1',
    binding_count: '0',
    created_at: 1_700_000_000,
    updated_at: 1_700_000_001,
  };
  const requests: URLSearchParams[] = [];
  await page.route(`${USER_ORIGIN}/api/endpoints?**`, (route) =>
    route.fulfill({ json: numberedPage([], new URL(route.request().url()).searchParams) }),
  );
  await page.route(`${USER_ORIGIN}/api/models**`, async (route) => {
    const url = new URL(route.request().url());
    let body: unknown;
    if (url.pathname === '/api/models') {
      requests.push(url.searchParams);
      body = numberedPage([model], url.searchParams);
    } else if (url.pathname === '/api/models/7') body = model;
    else if (url.pathname === '/api/models/7/bindings')
      body = { binding_revision: '1', bindings: [] };
    else if (url.pathname === '/api/models/7/binding-candidates')
      body = numberedPage([], url.searchParams);
    else {
      await route.fallback();
      return;
    }
    await route.fulfill({ json: body });
  });
  await page.goto(`${USER_ORIGIN}/models`);
  const form = page.getByRole('form', { name: 'Resource filters' });
  await form.getByRole('searchbox').fill('upstream-identifier');
  await form.getByRole('textbox', { name: 'Prefix', exact: true }).fill('Vendor');
  await form.getByRole('button', { name: 'Search', exact: true }).click();
  await form.getByRole('combobox', { name: 'Order' }).selectOption('ordered');
  await form.getByRole('combobox', { name: 'Sources', exact: true }).selectOption('unconfigured');
  await expect
    .poll(() =>
      requests.some(
        (p) =>
          p.get('q') === 'upstream-identifier' &&
          p.get('provider') === 'Vendor' &&
          p.get('route_strategy') === 'ordered' &&
          p.get('connection_state') === 'unconfigured',
      ),
    )
    .toBe(true);
  await page.getByRole('button', { name: 'Edit model', exact: true }).click();
  await expect.poll(() => new URL(page.url()).searchParams.get('model_id')).toBe('7');
  await page.getByRole('button', { name: 'Back', exact: true }).click();
  await expect(form.getByRole('searchbox')).toHaveValue('upstream-identifier');
  await expect(form.getByRole('textbox', { name: 'Prefix', exact: true })).toHaveValue('Vendor');
  guard.assertNone();
});
