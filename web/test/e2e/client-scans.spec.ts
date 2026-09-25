import { expect, test } from './test';
import { ADMIN_ORIGIN } from './ports';
import { collectConsoleViolations, mockPublicConfig, mockRoleSession } from './support';

test('completed scans paginate matches, restore history and refresh without rescanning', async ({
  page,
}) => {
  const errors = collectConsoleViolations(page);
  await mockRoleSession(page, 'admin', 'admin');
  await mockPublicConfig(page, 'admin');
  const scan = {
    id: 'scn_AAAAAAAAAAAAAAAAAAAAAA',
    state: 'completed',
    reason: '',
    from: 1800000000,
    to: 1800086400,
    kind: 'total',
    model: '',
    candidates: '257',
    scanned: '257',
    matched: 119,
    rule_count: 1,
    created_at: 1800086400,
    updated_at: 1800086410,
    expires_at: 1800172800,
  };
  let creates = 0;
  await page.route('**/admin/api/abuse-audit/client-scans**', async (route) => {
    const url = new URL(route.request().url());
    if (route.request().method() === 'POST') {
      creates++;
      expect(route.request().postDataJSON().request_token).toMatch(/^[a-f0-9-]{36}$/);
      await route.fulfill({ json: scan });
      return;
    }
    if (url.pathname.endsWith('/results')) {
      const size = Number(url.searchParams.get('page_size'));
      const current = Math.min(Number(url.searchParams.get('page')), Math.ceil(119 / size));
      const count = Math.min(size, 119 - (current - 1) * size);
      await route.fulfill({
        json: {
          scan,
          page: String(current),
          page_size: size,
          total_items: '119',
          total_pages: String(Math.ceil(119 / size)),
          items: Array.from({ length: count }, (_, i) => ({
            log_id: String((current - 1) * size + i + 1),
            user_id: '7',
            request_id: 'matched-request-' + ((current - 1) * size + i + 1),
            call_kind: 'self',
            occurred_at: 1800000010,
            model: 'example/model',
            outcome: 'succeeded',
            dispatched: true,
            error_code: '',
            rejection_reason: '',
            duration_millis: 17,
            response_started: true,
            source: {
              effective_ip: '192.0.2.1',
              ip_quality: 'trusted',
              user_agent: 'Tavo/1.0',
              origin: '',
              referer: '',
              http_referer: '',
              openrouter_title: '',
              legacy_title: '',
              sdk_lang: '',
              sdk_version: '',
              sdk_runtime: '',
              sdk_runtime_version: '',
              quality: {},
            },
            matches: [
              {
                rule_id: 'rsk_example',
                revision: 1,
                name: 'Tavo',
                status: 'suspected',
                evidence_note: '',
                evidence_url: '',
                quality: 'recorded',
                fields: ['user_agent'],
              },
            ],
            match_count: 1,
            matches_truncated: false,
          })),
        },
      });
      return;
    }
    await route.fulfill({ json: { items: creates ? [scan] : [] } });
  });
  await page.goto(ADMIN_ORIGIN + '/abuse-audit?audit_tab=clients');
  await page.getByRole('button', { name: 'Start new scan', exact: true }).click();
  const pager = page.getByRole('navigation', { name: 'Pagination', exact: true });
  await expect(pager).toContainText('Page 1 of 6 · Total: 119');
  await pager.getByRole('button', { name: 'Next', exact: true }).click();
  await expect(pager).toContainText('Page 2 of 6');
  await expect(page.getByText('matched-request-21', { exact: true })).toBeVisible();
  await pager.locator('input').fill('6');
  await pager.locator('input').press('Enter');
  await expect(pager).toContainText('Page 6 of 6');
  await page.reload();
  await expect(pager).toContainText('Page 6 of 6');
  expect(creates).toBe(1);
  await page.goBack();
  await expect(pager).toContainText('Page 2 of 6');
  await pager.getByRole('button', { name: 'Previous', exact: true }).click();
  await expect(pager).toContainText('Page 1 of 6');
  await pager.locator('select').selectOption('50');
  await expect(pager).toContainText('Page 1 of 3');
  await expect(page.getByText('matched-request-50', { exact: true })).toBeAttached();
  for (const width of [1440, 390]) {
    await page.setViewportSize({ width, height: 1000 });
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(
      true,
    );
  }
  await errors.assertNone();
});
