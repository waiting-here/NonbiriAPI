import { expect, test } from './test';
import { USER_ORIGIN } from './ports';
import { collectConsoleViolations, mockPublicConfig, mockRoleSession } from './support';
import { numberedPage } from './numbered-fixtures';

const endpoints = Array.from({ length: 23 }, (_, index) => ({
  id: String(index + 1),
  connector_type: 'openai-compatible',
  base_url: `https://resources.example.test/${index + 1}/v1`,
  origin: { kind: 'custom' },
  note: `Needle resource ${index + 1}`,
  enabled: true,
  revision: '1',
  key_count: '0',
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
    const searches: string[] = [];
    const keySearches: URLSearchParams[] = [];
    await page.route(`${USER_ORIGIN}/api/**`, async (route) => {
      const url = new URL(route.request().url());
      let body: unknown;
      if (url.pathname === '/api/endpoint-create-options')
        body = {
          base_connector_types: ['openai-compatible', 'anthropic-compatible'],
          mainstream_channels: [],
        };
      else if (url.pathname === '/api/endpoints') {
        searches.push(url.search);
        const rows = url.searchParams.get('q') === 'absent' ? [] : endpoints;
        body = numberedPage(rows, url.searchParams);
      } else if (/^\/api\/endpoints\/[0-9]+$/.test(url.pathname))
        body = endpoints[Number(url.pathname.split('/').pop()) - 1];
      else if (/^\/api\/endpoints\/[0-9]+\/keys$/.test(url.pathname)) {
        keySearches.push(url.searchParams);
        body = numberedPage([], url.searchParams);
      } else {
        await route.fallback();
        return;
      }
      await route.fulfill({ json: body });
    });
    await page.goto(`${USER_ORIGIN}/endpoints?page=2&page_size=10`);
    const form = page.getByRole('form', {
      name: locale === 'en' ? 'Resource filters' : '资源筛选',
    });
    await expect(page.getByText('Needle resource 11', { exact: true })).toBeVisible();
    await form.getByRole('searchbox').fill(' Needle ');
    await form
      .getByRole('button', { name: locale === 'en' ? 'Search' : '搜索', exact: true })
      .click();
    await expect.poll(() => new URL(page.url()).searchParams.get('page')).toBe('1');
    await expect.poll(() => new URL(page.url()).searchParams.get('q')).toBe('Needle');
    await form
      .getByRole('combobox', { name: locale === 'en' ? 'Source' : '来源', exact: true })
      .selectOption('custom');
    await expect.poll(() => new URL(page.url()).searchParams.get('q')).toBe('Needle');
    await page
      .getByRole('button', { name: locale === 'en' ? 'Next' : '下一页', exact: true })
      .click();
    await expect(page.getByText('Needle resource 11', { exact: true })).toBeVisible();
    await page.locator('a[href="/endpoints/19"]').scrollIntoViewIfNeeded();
    const before = await page.evaluate(() => scrollY);
    await page.locator('a[href="/endpoints/19"]').click();
    await expect(
      page.getByRole('heading', {
        name: locale === 'en' ? 'Endpoint details' : '端点详情',
        exact: true,
      }),
    ).toBeVisible();
    await form.getByRole('searchbox').fill('masked-tail');
    await form
      .getByRole('button', { name: locale === 'en' ? 'Search' : '搜索', exact: true })
      .click();
    await form
      .getByRole('combobox', { name: locale === 'en' ? 'Enabled state' : '启用状态' })
      .selectOption('true');
    await form
      .getByRole('combobox', { name: locale === 'en' ? 'Donated' : '已捐赠', exact: true })
      .selectOption('true');
    await form
      .getByRole('combobox', { name: locale === 'en' ? 'Security status' : '安全状态' })
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
    await page.getByRole('link', { name: locale === 'en' ? 'Back' : '返回', exact: true }).click();
    await expect.poll(() => new URL(page.url()).searchParams.get('page')).toBe('2');
    expect(new URL(page.url()).searchParams.get('q')).toBe('Needle');
    expect(new URL(page.url()).searchParams.get('source')).toBe('custom');
    await expect
      .poll(async () => Math.abs((await page.evaluate(() => scrollY)) - before))
      .toBeLessThan(100);
    await page.reload();
    await expect(form.getByRole('searchbox')).toHaveValue('Needle');
    await form.getByRole('searchbox').fill('absent');
    await form
      .getByRole('button', { name: locale === 'en' ? 'Search' : '搜索', exact: true })
      .click();
    await expect(
      page.getByText(locale === 'en' ? 'No matching resources' : '没有符合条件的资源', {
        exact: true,
      }),
    ).toBeVisible();
    await form
      .getByRole('button', { name: locale === 'en' ? 'Clear filters' : '清除筛选', exact: true })
      .click();
    await expect(page.getByText('Needle resource 1', { exact: true })).toBeVisible();
    expect(
      searches.some(
        (value) =>
          value.includes('q=Needle') && value.includes('source=custom') && value.includes('page=2'),
      ),
    ).toBe(true);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
      true,
    );
    await page.screenshot({
      path: `../tmp/resource-filters-${locale}-320-dark.png`,
      fullPage: true,
    });
    await form.scrollIntoViewIfNeeded();
    await page.screenshot({ path: `../tmp/resource-filters-${locale}-viewport.png` });
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
    id: '7',
    provider: 'Vendor',
    model: 'Logical',
    full_name: 'Vendor/Logical',
    route_strategy: 'ordered',
    silent_retry: false,
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
  await form.getByRole('textbox', { name: 'Service provider', exact: true }).fill('Vendor');
  await form.getByRole('button', { name: 'Search', exact: true }).click();
  await form.getByRole('combobox', { name: 'Connection strategy' }).selectOption('ordered');
  await form
    .getByRole('combobox', { name: 'Connections', exact: true })
    .selectOption('unconfigured');
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
  await page.locator('.core-endpoint-card').getByRole('button').click();
  await expect.poll(() => new URL(page.url()).searchParams.get('model_id')).toBe('7');
  await page.getByRole('button', { name: 'Back', exact: true }).click();
  await expect(form.getByRole('searchbox')).toHaveValue('upstream-identifier');
  await expect(form.getByRole('textbox', { name: 'Service provider', exact: true })).toHaveValue(
    'Vendor',
  );
  guard.assertNone();
});
