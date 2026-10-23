import { mkdir } from 'node:fs/promises';
import { resolve } from 'node:path';
import { expect, test } from './test';
import { mockPublicConfig, mockRoleSession } from './support';
import { ADMIN_ORIGIN, USER_ORIGIN } from './ports';

const pressure = {
  rpm: { current: 60, limit: 600, peak_24h: 300, peak_all: 900 },
  concurrency: { current: 4, limit: 256, peak_24h: 64, peak_all: 260 },
  observed_at: 1_792_584_000,
  started_at: 1_792_497_600,
  sample_seconds: 5,
};
const emptyLogs = {
  data: [],
  next_cursor: null,
  pagination: { page: '1', page_size: 20, total_items: '0', total_pages: '1' },
};

for (const role of ['admin', 'level5', 'level6'] as const) {
  test(`${role} sees sampled load with bounded bars and retained peak values`, async ({ page }) => {
    const station = role === 'admin' ? 'admin' : 'user';
    const root = role === 'admin' ? '/admin/api/logs' : '/api/steward/logs';
    await mockRoleSession(page, station, role);
    await mockPublicConfig(page, station);
    await page.route(`**${root}?*`, (route) => route.fulfill({ json: emptyLogs }));
    await page.route(`**${root}/pressure`, (route) => route.fulfill({ json: pressure }));
    await page.goto(role === 'admin' ? `${ADMIN_ORIGIN}/logs` : `${USER_ORIGIN}/steward?tab=logs`);
    const section = page.getByRole('region', { name: 'Site load' });
    const rpm = section.getByRole('meter', { name: 'Site RPM' });
    await expect(rpm).toHaveAttribute('aria-valuenow', '60');
    await expect(rpm).toHaveAttribute('aria-valuemax', '600');
    await expect(rpm).toHaveAttribute(
      'aria-valuetext',
      'Current 60, limit 600, 24-hour peak 300, all-time peak 900',
    );
    await expect(rpm.locator('.global-pressure__fill--current')).toHaveAttribute(
      'style',
      'width: 10%;',
    );
    await expect(rpm.locator('.global-pressure__fill--day')).toHaveAttribute(
      'style',
      'width: 50%;',
    );
    await expect(rpm.locator('.global-pressure__fill--all')).toHaveAttribute(
      'style',
      'width: 100%;',
    );
    await expect(section.getByText('All-time peak 900', { exact: true })).toBeVisible();
    await expect(section.getByRole('meter', { name: 'Outbound concurrency' })).toHaveAttribute(
      'aria-valuenow',
      '4',
    );
    if (role === 'admin') {
      const output = process.env.NONBIRI_VISUAL_DIR ?? '../.pi/work/global-pressure/screenshots';
      await mkdir(output, { recursive: true });
      for (const theme of ['light', 'dark']) {
        await page.evaluate((value) => {
          document.documentElement.dataset.theme = value;
        }, theme);
        for (const width of [1440, 390]) {
          await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
          await section.scrollIntoViewIfNeeded();
          expect(
            await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth),
          ).toBe(true);
          expect(
            await section.evaluate((element) => element.scrollWidth <= element.clientWidth),
          ).toBe(true);
          await page.screenshot({
            path: resolve(output, `pressure-${theme}-${width}.png`),
            animations: 'disabled',
          });
        }
      }
    }
  });
}

test('pressure refresh keeps cached values on failure and stops after permission is lost', async ({
  page,
}) => {
  await page.clock.install();
  await mockRoleSession(page, 'admin', 'admin');
  await mockPublicConfig(page, 'admin');
  await page.route('**/admin/api/logs?*', (route) => route.fulfill({ json: emptyLogs }));
  let status = 200;
  let calls = 0;
  await page.route('**/admin/api/logs/pressure', (route) => {
    calls++;
    return route.fulfill({
      status,
      json:
        status === 200
          ? pressure
          : { error: { code: status === 403 ? 'forbidden' : 'unavailable' } },
    });
  });
  await page.goto(`${ADMIN_ORIGIN}/logs`);
  const section = page.getByRole('region', { name: 'Site load' });
  await expect(section.getByRole('meter', { name: 'Site RPM' })).toBeVisible();
  const initialCalls = calls;
  await page.clock.fastForward(14_000);
  expect(calls).toBe(initialCalls);
  status = 503;
  await page.clock.fastForward(1_000);
  await expect(section.getByRole('status')).toHaveText(
    'Refresh is unavailable. The latest observed values are shown.',
  );
  await expect(section.getByRole('meter', { name: 'Site RPM' })).toHaveAttribute(
    'aria-valuenow',
    '60',
  );
  status = 403;
  await page.clock.fastForward(15_000);
  await expect(section).toHaveCount(0);
  const forbiddenCalls = calls;
  await page.clock.fastForward(30_000);
  expect(calls).toBe(forbiddenCalls);
});

test('personal request logs do not load site pressure', async ({ page }) => {
  await mockRoleSession(page, 'user', 'user');
  await mockPublicConfig(page, 'user');
  let pressureCalls = 0;
  await page.route('**/logs/pressure', (route) => {
    pressureCalls++;
    return route.fulfill({ json: pressure });
  });
  await page.route('**/api/logs?*', (route) => route.fulfill({ json: emptyLogs }));
  await page.goto(`${USER_ORIGIN}/logs`);
  await expect(page.getByRole('heading', { name: 'Records', exact: true })).toBeVisible();
  await expect(page.getByRole('region', { name: 'Site load' })).toHaveCount(0);
  expect(pressureCalls).toBe(0);
});
