import { expect, test } from './test';
import { ADMIN_ORIGIN } from './ports';
import { collectConsoleViolations, mockPublicConfig, mockRoleSession } from './support';

const scanID = 'scn_BBBBBBBBBBBBBBBBBBBBBQ';
const scan = {
  id: scanID,
  state: 'completed',
  status: 'completed',
  reason: '',
  from: 1_799_900_000,
  to: 1_800_000_000,
  kind: 'users',
  call_kind: 'total',
  model: '',
  candidates: '41',
  scanned: '41',
  scanned_candidates: '41',
  matched: 41,
  rule_count: 0,
  created_at: 1_800_000_000,
  updated_at: 1_800_000_001,
  expires_at: 1_800_086_400,
  filter_revision: 1,
  changed: false,
  coverage: 'complete',
};
const user = (index: number) => ({
  user_id: String(index),
  rpm_committed: 1,
  rpm_denied: 0,
  concurrency_denied: 0,
  peak: 1,
  occupancy_millis: 1000,
  complete_minutes: 1,
  incomplete_minutes: 0,
  high_rpm_minutes: 0,
  high_concurrency_minutes: 0,
  rpm_risk: false,
  concurrency_risk: false,
  risk_scope: 'total',
});

test('user audit task pages restore URL state on desktop and mobile', async ({ page }) => {
  const errors = collectConsoleViolations(page);
  await mockRoleSession(page, 'admin', 'admin');
  await mockPublicConfig(page, 'admin');
  await page.route('**/admin/api/abuse-audit/**', async (route) => {
    const url = new URL(route.request().url());
    if (url.pathname.endsWith('/scans')) {
      await route.fulfill({ json: { items: [scan] } });
      return;
    }
    if (url.pathname.endsWith('/results')) {
      const size = Number(url.searchParams.get('page_size'));
      const requested = Number(url.searchParams.get('page'));
      const pages = Math.ceil(41 / size);
      const current = Math.min(requested, pages);
      const start = (current - 1) * size;
      await route.fulfill({
        json: {
          scan,
          items: Array.from({ length: Math.min(size, 41 - start) }, (_, index) =>
            user(start + index + 1),
          ),
          page: String(current),
          page_size: size,
          total_items: '41',
          total_pages: String(pages),
          coverage: 'complete',
        },
      });
      return;
    }
    await route.fulfill({ status: 404, json: { error: 'unmatched audit fixture' } });
  });
  await page.goto(ADMIN_ORIGIN + '/abuse-audit?audit_tab=users&audit_users_scan=' + scanID);
  const pager = page.getByRole('navigation', { name: 'Pagination', exact: true });
  await expect(pager).toContainText('41 items');
  await pager.getByRole('button', { name: 'Next', exact: true }).click();
  await expect(pager).toContainText('Page 2 of 3');
  await expect(page.getByText('User ID: 21', { exact: true })).toBeVisible();
  await pager.getByRole('button', { name: '3', exact: true }).click();
  await expect(pager).toContainText('Page 3 of 3');
  await page.reload();
  await expect(pager).toContainText('Page 3 of 3');
  await page.goBack();
  await expect(pager).toContainText('Page 2 of 3');
  await pager.locator('select').selectOption('50');
  await expect(pager).toContainText('41 items');
  for (const width of [1440, 390]) {
    await page.setViewportSize({ width, height: 1000 });
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(
      true,
    );
  }
  await errors.assertNone();
});
