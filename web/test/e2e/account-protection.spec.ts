import { expect, test } from './test';
import { ADMIN_ORIGIN } from './ports';
import { collectConsoleViolations, mockPublicConfig, mockRoleSession } from './support';

async function setup(page: import('@playwright/test').Page) {
  await mockRoleSession(page, 'admin', 'admin');
  await mockPublicConfig(page, 'admin');
  await page.emulateMedia({ reducedMotion: 'reduce' });
}
async function layout(page: import('@playwright/test').Page, name: string) {
  for (const width of [1440, 390, 320]) {
    await page.setViewportSize({ width, height: 1000 });
    await expect
      .poll(() => page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1))
      .toBe(true);
    expect(
      await page
        .locator('main button')
        .evaluateAll(
          (buttons) => buttons.filter((button) => !button.classList.contains('btn')).length,
        ),
    ).toBe(0);
    expect(
      await page.locator('main form input, main form button').evaluateAll(
        (nodes) =>
          nodes.filter((node) => {
            const rect = node.getBoundingClientRect();
            return rect.width > 0 && (rect.left < 0 || rect.right > innerWidth + 1);
          }).length,
      ),
    ).toBe(0);
    await page.screenshot({ path: `../tmp/${name}-${width}.png`, fullPage: true });
  }
}

test('deletion alerts can be filtered and resolved in a selected batch', async ({ page }) => {
  const errors = collectConsoleViolations(page);
  await setup(page);
  let done = false;
  await page.route('**/admin/api/alerts**', async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    if (request.method() === 'POST') {
      expect(url.pathname).toBe('/admin/api/alerts/resolve');
      expect(request.postDataJSON()).toEqual({ ids: ['7'] });
      done = true;
      await route.fulfill({ json: { resolved_count: 1 } });
      return;
    }
    const kind = url.searchParams.get('kind') ?? 'account_deleted';
    const data = done
      ? []
      : [
          {
            id: '7',
            kind,
            message: 'Review',
            ref: null,
            subject_user_id: null,
            created_at: 1800000000,
            resolved: false,
            resolved_at: null,
            ...(kind === 'account_deleted'
              ? {
                  account_deletion: {
                    user_id: '42',
                    discord_id: '123456789012345678',
                    general_balance: '-2500.125',
                    game_balance: '12',
                    donation_credit: '120',
                    sketch_paper: '1',
                    sketch_brush: '0',
                  },
                }
              : {}),
          },
        ];
    await route.fulfill({
      json: {
        data,
        next_cursor: null,
        pagination: {
          page: '1',
          page_size: 20,
          total_items: String(data.length),
          total_pages: '1',
        },
      },
    });
  });
  await page.goto(`${ADMIN_ORIGIN}/alerts`);
  await page.getByLabel('Alert type').selectOption('account_deleted');
  await expect(page.getByText('123456789012345678')).toBeVisible();
  await expect(page.getByText('-2500.125')).toBeVisible();
  await layout(page, 'deletion-alert');
  await page.getByRole('checkbox', { name: 'Select alert 7' }).check();
  await page.getByRole('button', { name: 'Resolve selected (1)' }).click();
  await expect(page.getByText('123456789012345678')).toHaveCount(0);
  expect(done).toBe(true);
  errors.assertNone();
});

test('blacklist add and remove work on desktop and narrow screens', async ({ page }) => {
  const errors = collectConsoleViolations(page);
  await setup(page);
  let listed = false;
  await page.route('**/admin/api/blacklist**', async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    if (request.method() === 'POST') {
      expect(request.headers()['idempotency-key']).toMatch(/^[A-Za-z0-9_-]{22}$/);
      listed = !url.pathname.endsWith('/remove');
      if (listed)
        expect(request.postDataJSON()).toEqual({
          discord_id: '123456789012345678',
          reason: 'Policy review',
        });
      await route.fulfill({ status: 204 });
      return;
    }
    const data = listed
      ? [
          {
            discord_id: '123456789012345678',
            reason: 'Policy review',
            created_at: 1800000000,
            user_id: '42',
          },
        ]
      : [];
    await route.fulfill({
      json: {
        data,
        next_cursor: null,
        pagination: {
          page: '1',
          page_size: 20,
          total_items: String(data.length),
          total_pages: '1',
        },
      },
    });
  });
  await page.goto(`${ADMIN_ORIGIN}/blacklist`);
  await page.getByLabel('Discord ID', { exact: true }).fill('123456789012345678');
  await page.getByLabel('Reason', { exact: true }).fill('Policy review');
  await page.getByRole('button', { name: 'Add and permanently ban' }).click();
  await expect(page.getByRole('link', { name: '42', exact: true })).toBeVisible();
  await layout(page, 'blacklist');
  await page.getByRole('button', { name: 'Remove from blacklist' }).click();
  await expect(page.getByRole('link', { name: '42', exact: true })).toHaveCount(0);
  expect(listed).toBe(false);
  errors.assertNone();
});
