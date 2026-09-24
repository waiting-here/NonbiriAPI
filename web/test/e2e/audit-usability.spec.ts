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
        .locator('.audit-page button, .inactivity-editor button')
        .evaluateAll((nodes) => nodes.filter((n) => !n.classList.contains('btn')).length),
    ).toBe(0);
    expect(
      await page
        .locator('main form input, main form select, main form button')
        .evaluateAll((nodes) =>
          nodes
            .filter((n) => {
              const r = n.getBoundingClientRect();
              return r.width > 0 && (r.left < 0 || r.right > innerWidth + 1);
            })
            .map((n) => n.outerHTML),
        ),
    ).toEqual([]);
    await page.evaluate(() => window.scrollTo(0, 0));
    await page.screenshot({ path: `../tmp/${name}-${width}.png`, fullPage: true });
  }
}

test('audit quick ranges, healthy capture and rule patterns work without reloading the page', async ({
  page,
}) => {
  const errors = collectConsoleViolations(page);
  await setup(page);
  let saved: Record<string, unknown> | null = null;
  await page.route('**/admin/api/abuse-audit/**', async (route) => {
    const url = new URL(route.request().url());
    const path = url.pathname.split('/').at(-1);
    if (path === 'client-rules') {
      if (route.request().method() === 'POST') {
        saved = {
          ...route.request().postDataJSON(),
          id: 'rsk_example',
          revision: 1,
          created_at: 1800000000,
          updated_at: 1800000000,
          created_by_role: 'admin',
          updated_by_role: 'admin',
          created_by_user_id: null,
          updated_by_user_id: null,
        };
        await route.fulfill({ json: saved });
        return;
      }
      await route.fulfill({
        json: { items: saved ? [saved] : [], total: saved ? 1 : 0, has_more: false, next: '' },
      });
      return;
    }
    if (path === 'access-summary') {
      await route.fulfill({
        json: {
          authenticated_events: 0,
          anonymous_events: 1,
          model_list_events: 0,
          generation_requests: 0,
          model_generation_ratio: null,
          coverage: { capture_started_at: 1800000000, dropped: 0, last_gap_at: null },
          paths: [],
        },
      });
      return;
    }
    if (path === 'access-events') {
      await route.fulfill({ json: { data: [], next_cursor: null } });
      return;
    }
    expect(url.searchParams.has('to')).toBe(false);
    expect(url.searchParams.has('from')).toBe(false);
    await route.fulfill({
      json: {
        items: [],
        has_more: false,
        next: '',
        from: 1800000000 - 86400,
        to: 1800000000,
        coverage: 'observed_minutes',
        scanned: 0,
      },
    });
  });
  await page.goto(ADMIN_ORIGIN + '/abuse-audit');
  await expect(page.getByText('No entries on this page', { exact: true })).toBeVisible();
  await page.getByLabel(/^Time range/).selectOption('168');
  await page.getByRole('button', { name: 'Apply filters', exact: true }).click();
  await page.getByRole('button', { name: 'Access events', exact: true }).click();
  await expect(page.getByText('No capture gaps recorded', { exact: true })).toBeVisible();
  await expect(page.getByRole('alert')).toHaveCount(0);
  await page.getByRole('button', { name: 'Client rules', exact: true }).click();
  await page.getByRole('button', { name: 'New rule', exact: true }).click();
  for (const [name, field, value] of [
    ['Tavo', 'user_agent', 'Tavo/'],
    ['New API', 'openrouter_title', 'New API'],
    ['One API', 'legacy_title', 'One API'],
  ]) {
    await page.getByRole('button', { name, exact: true }).click();
    await expect(page.getByLabel(/^Field/)).toHaveValue(field);
    await expect(page.getByLabel('Match value', { exact: true })).toHaveValue(value);
    expect(saved).toBeNull();
  }
  await page
    .getByRole('button', { name: 'Source website and application title', exact: true })
    .click();
  await page.getByLabel('Rule name', { exact: true }).fill('Example relay');
  await page.getByLabel('Match value', { exact: true }).nth(0).fill('https://example.test');
  await page.getByLabel('Match value', { exact: true }).nth(1).fill('Example application');
  await layout(page, 'audit-rule');
  await page.getByRole('button', { name: 'Save', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Example relay', exact: true })).toBeVisible();
  expect(saved).toMatchObject({
    conditions: [
      { field: 'http_referer', operator: 'equals', value: 'https://example.test' },
      { field: 'openrouter_title', operator: 'equals', value: 'Example application' },
    ],
  });
  await page.evaluate(() => {
    localStorage.setItem('nb.lang', 'zh');
    localStorage.setItem('nb.theme', 'dark');
  });
  await page.reload();
  await expect(page.getByRole('heading', { name: '防滥用审计', exact: true })).toBeVisible();
  await layout(page, 'audit-zh-dark');
  await errors.assertNone();
});

test('inactivity settings validate, preview exact human-readable amounts and save the returned revision', async ({
  page,
}) => {
  const errors = collectConsoleViolations(page);
  await setup(page);
  let configuration = {
    enabled: false,
    revision: '1',
    updated_at: 0,
    decay_grace_until: 0,
    protection_grace_until: 0,
    decay: {
      enabled: false,
      inactive_days: null as number | null,
      interval_days: null as number | null,
      assets: { general: null, game: null },
    },
    protection: { enabled: false, inactive_days: null as number | null },
  };
  let previews = 0,
    saves = 0;
  await page.route('**/admin/api/inactivity-policy**', async (route) => {
    if (route.request().method() !== 'GET') {
      const body = route.request().postDataJSON();
      expect(body.expected_revision).toBe(configuration.revision);
      expect(body.policy.decay.assets.general).toEqual({
        mode: 'percent',
        value: '125',
        floor: '12345',
      });
      if (new URL(route.request().url()).pathname.endsWith('/preview')) {
        previews++;
        await route.fulfill({
          json: {
            data: [],
            next_cursor: null,
            as_of: 1800000000,
            configuration: { ...configuration, ...body.policy },
          },
        });
        return;
      }
      saves++;
      configuration = { ...configuration, ...body.policy, revision: String(saves + 1) };
    }
    await route.fulfill({ json: configuration });
  });
  await page.goto(ADMIN_ORIGIN + '/inactivity-policy');
  await expect(page.getByLabel('Inactive days', { exact: true })).toHaveCount(0);
  await layout(page, 'inactivity-default');
  await page.getByLabel('Enable inactivity policy', { exact: true }).check();
  await page.getByLabel('Enable credit decay', { exact: true }).check();
  await page.getByRole('button', { name: 'Preview accounts', exact: true }).click();
  expect(previews).toBe(0);
  await page.getByLabel('Inactive days', { exact: true }).fill('30');
  await page.getByLabel('Interval (days)', { exact: true }).fill('7');
  await page
    .getByRole('group', { name: 'General credits', exact: true })
    .getByLabel('Decay this currency')
    .check();
  await page.getByLabel('Decay per period (%)', { exact: true }).fill('1.25');
  await page.getByLabel('Balance floor (credits)', { exact: true }).fill('12.345');
  await page.getByRole('button', { name: 'Preview accounts', exact: true }).click();
  await expect(
    page.getByRole('heading', { name: 'Candidate policy preview', exact: true }),
  ).toBeVisible();
  expect(saves).toBe(0);
  await layout(page, 'inactivity-configured');
  await page.getByRole('button', { name: 'Save policy', exact: true }).click();
  await expect(page.getByText('Policy saved.', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Save policy', exact: true }).click();
  await expect.poll(() => saves).toBe(2);
  await page.evaluate(() => {
    localStorage.setItem('nb.lang', 'zh');
    localStorage.setItem('nb.theme', 'dark');
  });
  await page.reload();
  await expect(page.getByRole('heading', { name: '低活跃政策', exact: true })).toBeVisible();
  await layout(page, 'inactivity-zh-dark');
  await errors.assertNone();
});
