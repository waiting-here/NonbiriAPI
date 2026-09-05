import { expect, test } from './test';
import {
  collectConsoleViolations,
  installLoopbackNetworkBoundary,
  mockPublicConfig,
  mockRoleSession,
} from './support';
import { USER_ORIGIN } from './ports';

test('credit history filters, jumps between stable pages and fits a mobile viewport', async ({
  page,
}) => {
  await installLoopbackNetworkBoundary(page.context());
  const consoleErrors = collectConsoleViolations(page);
  await mockRoleSession(page, 'user', 'user');
  await mockPublicConfig(page, 'user');
  const anchor = `op_${'A'.repeat(22)}`;
  const requestID = `req_${'B'.repeat(21)}A`;
  const queries: URLSearchParams[] = [];
  await page.route('**/api/credits/history**', async (route) => {
    const params = new URL(route.request().url()).searchParams;
    queries.push(params);
    const size = Number(params.get('page_size') ?? 20);
    const total = params.has('category') ? 1 : 55;
    const current = Math.min(Number(params.get('page') ?? 1), Math.ceil(total / size));
    const offset = (current - 1) * size;
    const data = Array.from({ length: Math.min(size, total - offset) }, (_, i) => ({
      operation_id: `op_${(offset + i).toString(16).padStart(21, '0')}A`,
      line: 1,
      kind: params.has('category') ? 'charity_settle' : i === 1 ? 'donor_reward' : 'checkin_award',
      delta: params.has('category') ? '-0.007' : '2.5',
      created_at: 1_800_000_000 - offset - i,
      request_id: params.has('category') ? requestID : null,
    }));
    await route.fulfill({
      json: {
        data,
        page: String(current),
        page_size: size,
        total: String(total),
        total_pages: String(Math.ceil(total / size)),
        anchor,
        current_balance: '9000000000000.007',
        server_now: 1_800_000_001,
      },
    });
  });
  await page.goto(`${USER_ORIGIN}/credits`);
  await expect(page.getByRole('heading', { name: 'Nonbiri credit history' })).toBeVisible();
  await expect(page.locator('.credit-history__table tbody tr')).toHaveCount(20);
  await expect(page.getByText('9000000000000.007', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Next', exact: true }).click();
  await expect.poll(() => queries.at(-1)?.get('page')).toBe('2');
  expect(queries.at(-1)?.get('anchor')).toBe(anchor);
  await page.getByRole('textbox', { name: 'Go to page', exact: true }).fill('3');
  await page.getByRole('button', { name: 'Go', exact: true }).click();
  await expect(page.locator('.credit-history__table tbody tr')).toHaveCount(15);
  await page.getByRole('combobox', { name: 'Rows per page' }).selectOption('50');
  await expect(page.locator('.credit-history__table tbody tr')).toHaveCount(50);
  await page.getByRole('combobox', { name: 'Reason', exact: true }).selectOption('charity');
  await page.getByRole('combobox', { name: 'Money in / out' }).selectOption('expense');
  await page.getByRole('button', { name: 'Apply filters' }).click();
  await expect(page.locator('.credit-history__table tbody tr')).toHaveCount(1);
  expect(queries.at(-1)?.has('anchor')).toBe(false);
  expect(queries.at(-1)?.get('direction')).toBe('expense');
  await expect(page.getByRole('link', { name: 'View request' })).toHaveAttribute(
    'href',
    `/logs?request_id=${requestID}`,
  );
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.screenshot({ path: '../tmp/credit-history-desktop.png', fullPage: true });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.screenshot({ path: '../tmp/credit-history-mobile.png', fullPage: true });
  const overflowing = await page.locator('body *').evaluateAll((nodes) =>
    nodes
      .filter((node) => node.getBoundingClientRect().right > window.innerWidth + 1)
      .slice(0, 12)
      .map((node) => ({
        tag: node.tagName,
        class: node.className,
        right: node.getBoundingClientRect().right,
      })),
  );
  await expect
    .poll(() => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), {
      message: JSON.stringify(overflowing),
    })
    .toBe(true);
  await expect
    .poll(() =>
      page
        .locator('.credit-history__table')
        .evaluate((table) => table.scrollWidth <= table.clientWidth),
    )
    .toBe(true);
  await page.screenshot({ path: '../tmp/credit-history-mobile.png', fullPage: true });
  await page.getByRole('button', { name: 'Clear filters' }).click();
  await expect(page.locator('.credit-history__table tbody tr')).toHaveCount(50);
  await expect(page.getByRole('link', { name: 'View request' })).toHaveCount(0);
  consoleErrors.assertNone();
});
