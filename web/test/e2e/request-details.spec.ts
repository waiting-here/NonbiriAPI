import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { expect, test } from './test';
import { collectConsoleViolations, mockPublicConfig, mockRoleSession } from './support';
import { ADMIN_ORIGIN, USER_ORIGIN } from './ports';

const fixtures = JSON.parse(
  readFileSync(resolve(process.cwd(), '../internal/logapi/testdata/role_dtos.golden'), 'utf8'),
) as Record<
  string,
  {
    request: { id: string } & Record<string, unknown>;
    attempts: { data: Record<string, unknown>[]; next_cursor: null };
  }
>;

for (const station of ['user', 'admin'] as const) {
  test(`${station} request details wrap long fields and keep attempts vertical on desktop and mobile`, async ({
    page,
  }) => {
    const errors = collectConsoleViolations(page);
    await mockRoleSession(page, station, station === 'admin' ? 'admin' : 'user');
    await mockPublicConfig(page, station);
    const detail = structuredClone(fixtures[station === 'admin' ? 'admin' : 'user_self']);
    const longEndpoint = `https://example.test/${'very-long-route-'.repeat(20)}/v1`;
    const longDiagnostic = 'A safe diagnostic '.repeat(90).trimEnd();
    detail.attempts.data[0].endpoint_base_url = longEndpoint;
    detail.attempts.data[1].diag = longDiagnostic;
    const prefix = station === 'admin' ? '/admin/api/logs' : '/api/logs';
    let detailReads = 0;
    await page.route(`**${prefix}**`, async (route) => {
      const url = new URL(route.request().url());
      const path = url.pathname;
      if (path === prefix) {
        expect(url.searchParams.get('page')).toBe('1');
        expect(url.searchParams.get('page_size')).toBe('20');
        await route.fulfill({
          json: {
            data: [detail.request],
            next_cursor: null,
            pagination: { page: '1', page_size: 20, total_items: '1', total_pages: '1' },
          },
        });
        return;
      }
      if (path === `${prefix}/${detail.request.id}`) {
        detailReads++;
        expect(url.searchParams.get('attempt_page')).toBe('1');
        expect(url.searchParams.get('attempt_page_size')).toBe('20');
        await route.fulfill({
          json: {
            ...detail,
            attempt_pagination: { page: '1', page_size: 20, total_items: '2', total_pages: '1' },
          },
        });
        return;
      }
      await route.fallback();
    });
    const origin = station === 'admin' ? ADMIN_ORIGIN : USER_ORIGIN;
    await page.setViewportSize({ width: 1600, height: 1000 });
    await page.goto(
      `${origin}/logs${station === 'user' ? `?request_id=${detail.request.id}` : ''}`,
    );
    if (station === 'admin') await page.locator('.log-table tbody button').first().click();
    const drawer = page.locator('.inspection-dock, .nb-sheet:has(.log-detail-content)');
    await expect(drawer).toBeVisible();
    await drawer.locator('summary').filter({ hasText: 'Service call attempts' }).click();
    await expect(drawer.locator('.log-attempt')).toHaveCount(2);
    await expect(drawer.locator('table')).toHaveCount(0);
    await expect(drawer.locator('.log-attempt-notice')).toBeVisible();
    expect(detailReads).toBeGreaterThan(0);
    await expect
      .poll(() => drawer.evaluate((node) => node.scrollWidth <= node.clientWidth + 1))
      .toBe(true);
    await page.screenshot({ path: `../tmp/${station}-request-detail-desktop.png` });
    for (const width of [390, 320]) {
      await page.setViewportSize({ width, height: 844 });
      await page.evaluate(() => {
        document.documentElement.style.scrollbarGutter = 'stable';
      });
      await expect
        .poll(() => drawer.evaluate((node) => node.getBoundingClientRect().left))
        .toBeGreaterThanOrEqual(0);
      // Switching from the desktop dock to a sheet mounts a fresh disclosure.
      await expect(page.locator('.nb-sheet:has(.log-detail-content)')).toBeVisible();
      const endpoint = drawer.getByText(longEndpoint, { exact: true });
      if (!(await endpoint.isVisible())) {
        await drawer.locator('summary').filter({ hasText: 'Service call attempts' }).click();
      }
      await expect(endpoint).toBeVisible();
      const secondAttempt = drawer.locator('.log-attempt').nth(1);
      const diagnostic = secondAttempt.locator('.log-attempt-diagnostic');
      if (!(await diagnostic.isVisible())) {
        await secondAttempt
          .locator('summary')
          .filter({ hasText: /^Diagnostic$/ })
          .click();
      }
      await expect(diagnostic).toBeVisible();
      await expect(diagnostic).toHaveText(longDiagnostic);
      await expect(drawer.getByRole('textbox', { name: 'Go to page' })).toHaveCount(0);
      await expect
        .poll(() =>
          drawer.evaluate((node) =>
            [node, ...node.querySelectorAll<HTMLElement>('*')]
              .filter(
                (element) =>
                  element.clientWidth > 0 &&
                  element.scrollWidth > element.clientWidth + 1 &&
                  !element.classList.contains('visually-hidden') &&
                  !element.matches('.page-pagination__jump > span'),
              )
              .map((element) => ({
                element: `${element.tagName}.${element.className}`,
                width: element.clientWidth,
                content: element.scrollWidth,
              })),
          ),
        )
        .toEqual([]);
      await expect.poll(() => page.evaluate(() => document.body.style.overflow)).not.toBe('hidden');
      await drawer.getByRole('button', { name: 'Close', exact: true }).scrollIntoViewIfNeeded();
      await expect(drawer.getByRole('button', { name: 'Close', exact: true })).toBeInViewport();
      await page.screenshot({ path: `../tmp/${station}-request-detail-mobile-${width}.png` });
    }
    // Larger browser text exposes single-line labels that just fit with some platform fonts.
    await page.evaluate(() => {
      document.documentElement.style.fontSize = '125%';
    });
    const copySummary = drawer.getByRole('button', { name: 'Copy request summary for this page' });
    await expect(copySummary).toBeVisible();
    expect(
      await copySummary.evaluate((button) => {
        const range = document.createRange();
        range.selectNodeContents(button);
        const lines = Array.from(range.getClientRects());
        const bounds = button.getBoundingClientRect();
        return (
          lines.length > 1 &&
          lines.every((line) => line.left >= bounds.left && line.right <= bounds.right)
        );
      }),
    ).toBe(true);
    await expect
      .poll(() =>
        drawer
          .locator('.log-detail-content')
          .evaluate((node) => node.scrollWidth <= node.clientWidth + 1),
      )
      .toBe(true);
    await drawer.getByRole('button', { name: 'Close', exact: true }).click();
    await expect(drawer).toHaveCount(0);
    await expect.poll(() => page.evaluate(() => document.body.style.overflow)).not.toBe('hidden');
    if (station === 'user') expect(new URL(page.url()).searchParams.has('request_id')).toBe(false);
    errors.assertNone();
  });
}

test('an expired or foreign request link stays unavailable in the current account', async ({
  page,
}) => {
  await mockRoleSession(page, 'user', 'user');
  await mockPublicConfig(page, 'user');
  const id = fixtures.user_self.request.id;
  await page.route('**/api/logs**', (route) =>
    new URL(route.request().url()).pathname.endsWith(id)
      ? route.fulfill({ status: 404, json: { error: { code: 'not_found', message: 'not found' } } })
      : route.fulfill({
          json: {
            data: [],
            next_cursor: null,
            pagination: { page: '1', page_size: 20, total_items: '0', total_pages: '1' },
          },
        }),
  );
  await page.goto(`${USER_ORIGIN}/logs?request_id=${id}`);
  await expect(page.locator('.inspection-dock, .nb-sheet:has(.log-detail-content)')).toContainText(
    /no longer available|unavailable|expired/i,
  );
  await expect(page.locator('.log-attempt')).toHaveCount(0);
});

test('management diagnostics use shared buttons and recover without refreshing', async ({
  page,
}) => {
  const errors = collectConsoleViolations(page);
  const modules: string[] = [];
  page.on('request', (request) => {
    if (request.resourceType() === 'script') modules.push(new URL(request.url()).pathname);
  });
  const loadsActivity = () =>
    modules.some((path) => /\/IndependentDiagnostics-[^/]+\.js$/.test(path));
  await mockRoleSession(page, 'admin', 'admin');
  await mockPublicConfig(page, 'admin');
  await page.route('**/admin/api/logs?*', (route) =>
    route.fulfill({
      json: {
        data: [],
        next_cursor: null,
        pagination: { page: '1', page_size: 20, total_items: '0', total_pages: '1' },
      },
    }),
  );
  let fail = true;
  await page.route('**/admin/api/diagnostics?*', async (route) => {
    const params = new URL(route.request().url()).searchParams;
    await route.fulfill({
      json: fail
        ? {}
        : {
            data: [],
            next_before: null,
            from: Number(params.get('from')),
            to: Number(params.get('to')),
          },
    });
  });
  await page.goto(ADMIN_ORIGIN + '/logs');
  const activityTab = page.getByRole('tab', { name: 'Discovery and activity' });
  await expect(
    page.getByRole('region', { name: 'Discovery and activity diagnostics' }),
  ).toHaveCount(0);
  await expect(activityTab).toBeVisible();
  expect(loadsActivity()).toBe(false);
  await activityTab.click();
  await expect(page).toHaveURL(/log_view=activity/);
  const section = page.getByRole('region', { name: 'Discovery and activity diagnostics' });
  const button = section.getByRole('button', { name: 'Search', exact: true });
  await expect(button).toBeVisible();
  expect(loadsActivity()).toBe(true);
  await button.focus();
  await page.keyboard.press('Enter');
  await expect(section.getByRole('alert')).toBeVisible();
  fail = false;
  await button.click();
  await expect(section.getByRole('alert')).toHaveCount(0);
  for (const width of [1440, 390]) {
    await page.setViewportSize({ width, height: 900 });
    expect(
      await section
        .locator('button')
        .evaluateAll((nodes) => nodes.every((n) => n.classList.contains('nb-btn'))),
    ).toBe(true);
    expect(await section.evaluate((n) => n.scrollWidth <= n.clientWidth + 1)).toBe(true);
  }
  errors.assertNone();
});

test('switches requests in a wide dock and returns from a narrow sheet to the active row', async ({
  page,
}) => {
  await mockRoleSession(page, 'admin', 'admin');
  await mockPublicConfig(page, 'admin');
  const base = fixtures.admin;
  const ids = [base.request.id, 'req_' + 'B'.repeat(21) + 'Q'];
  const model = '[公益]moon-archive/quartz-long-context-长上下文预览版-2026-11';
  const rows = ids.map((id) => ({
    ...base.request,
    id,
    route_kind: 'charity_chat_completions',
    charity_model: model,
    charity_model_id: '31',
  }));
  const reads: string[] = [];
  await page.route('**/admin/api/logs**', async (route) => {
    const url = new URL(route.request().url());
    if (url.pathname === '/admin/api/logs') {
      expect(url.searchParams.get('outcome')).toBe('actual_backoff');
      return route.fulfill({
        json: {
          data: rows,
          next_cursor: null,
          pagination: { page: '2', page_size: 10, total_items: '12', total_pages: '2' },
        },
      });
    }
    const id = url.pathname.split('/').pop();
    if (id && ids.includes(id)) {
      reads.push(id);
      return route.fulfill({
        json: {
          ...base,
          request: rows.find((row) => row.id === id),
          attempt_pagination: { page: '1', page_size: 20, total_items: '2', total_pages: '1' },
        },
      });
    }
    return route.fallback();
  });
  await page.setViewportSize({ width: 1920, height: 900 });
  await page.goto(ADMIN_ORIGIN + '/logs?outcome=actual_backoff&page=2&page_size=10');
  const first = page.locator('[data-inspect-id="' + ids[0] + '"]');
  const second = page.locator('[data-inspect-id="' + ids[1] + '"]');
  await expect(first).toBeVisible();
  expect(reads).toEqual([]);
  await first.click();
  const dock = page.locator('.inspection-dock');
  await expect(dock.getByRole('heading', { name: 'Log details ' + ids[0] })).toBeFocused();
  const name = page.locator('.log-table').getByTitle(model).first();
  const size = await name.evaluate((node) => ({
    width: node.clientWidth,
    lines: node.clientHeight / parseFloat(getComputedStyle(node).lineHeight),
    rem: parseFloat(getComputedStyle(document.documentElement).fontSize),
  }));
  expect(size.width).toBeGreaterThanOrEqual(11 * size.rem);
  expect(size.lines).toBeLessThanOrEqual(2.05);
  // Keep the same viewport and sidebar while narrowing the space allocated to the tool.
  const workspace = page.locator('.inspection-workspace');
  await workspace.evaluate((node) => {
    node.style.maxWidth = '74rem';
  });
  await expect(page.getByRole('dialog', { name: 'Log details ' + ids[0] })).toBeVisible();
  expect(
    await page
      .locator('.inspection-list')
      .evaluate((list) => Math.abs(list.clientWidth - list.parentElement!.clientWidth)),
  ).toBeLessThanOrEqual(1);
  await workspace.evaluate((node) => {
    node.style.removeProperty('max-width');
  });
  await expect(dock.getByRole('heading', { name: 'Log details ' + ids[0] })).toBeFocused();
  await second.click();
  await expect(dock.getByRole('heading', { name: 'Log details ' + ids[1] })).toBeFocused();
  await expect(
    dock.getByRole('button', { name: 'Copy request summary for this page' }),
  ).toBeVisible();
  await page.setViewportSize({ width: 390, height: 844 });
  const sheet = page.getByRole('dialog', { name: 'Log details ' + ids[1] });
  await expect(sheet).toBeVisible();
  await page.keyboard.press('Tab');
  expect(await sheet.evaluate((node) => node.contains(document.activeElement))).toBe(true);
  await page.keyboard.press('Escape');
  await expect(sheet).toHaveCount(0);
  await expect(second).toBeFocused();
  await expect(page).toHaveURL(
    (url) =>
      url.searchParams.get('page') === '2' &&
      url.searchParams.get('outcome') === 'actual_backoff' &&
      !url.searchParams.has('request_id'),
  );
});
