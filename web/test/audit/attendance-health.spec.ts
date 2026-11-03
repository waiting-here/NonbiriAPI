import { randomUUID } from 'node:crypto';
import { mkdirSync, readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { test, expect, type Browser, type Locator, type Page } from '@playwright/test';
import type {
  AttendancePage,
  AttendancePeriod,
  SignInResult,
} from '../../src/shared/attendance/api';

test.skip(process.env.NONBIRI_V136_BROWSER !== '1', 'Requires the isolated v1.3.6 fixture.');

type Fixture = {
  admin_url: string;
  user_url: string;
  admin_cookie: { Name: string; Value: string };
  control_url: string;
  control_token: string;
  users: { level: number; cookie: { Name: string; Value: string } }[];
  management: { resource_health: { donation_id: string } };
};
const fixture = () =>
  JSON.parse(readFileSync(process.env.NONBIRI_AUDIT_BROWSER_STATE!, 'utf8')) as Fixture;

async function session(browser: Browser, role: 'owner' | 'admin' | 'steward') {
  const f = fixture();
  const origin = role === 'admin' ? f.admin_url : f.user_url;
  const cookie =
    role === 'admin'
      ? f.admin_cookie
      : f.users.find((u) => u.level === (role === 'owner' ? 1 : 6))!.cookie;
  const context = await browser.newContext({
    viewport: { width: 1440, height: 1000 },
    timezoneId: 'America/New_York',
    reducedMotion: 'reduce',
  });
  await context.addCookies([
    {
      name: cookie.Name,
      value: cookie.Value,
      domain: new URL(origin).hostname,
      path: role === 'admin' ? '/admin' : '/api',
      httpOnly: true,
      sameSite: 'Lax',
    },
  ]);
  await context.addInitScript(() => {
    localStorage.setItem('nb.lang', 'en');
    localStorage.setItem('nb.theme', 'light');
  });
  const page = await context.newPage();
  const errors: string[] = [];
  page.on('pageerror', (error) => errors.push(error.message));
  return { context, page, origin, errors };
}

async function control(page: Page, path: string, data = {}) {
  const f = fixture();
  const response = await page.request.post(f.control_url + '/v136/' + path, {
    headers: { Authorization: 'Bearer ' + f.control_token },
    data,
  });
  expect(response.ok()).toBe(true);
  return response.json();
}

async function screenshots(page: Page, target: Locator, name: string) {
  for (const width of [1440, 390]) {
    await page.setViewportSize({ width, height: 1000 });
    for (const theme of ['light', 'dark']) {
      await page.evaluate((value) => {
        localStorage.setItem('nb.theme', value);
        document.documentElement.dataset.theme = value;
      }, theme);
      await target.scrollIntoViewIfNeeded();
      expect(
        await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1),
      ).toBe(true);
      if (process.env.NONBIRI_VISUAL_DIR) {
        mkdirSync(process.env.NONBIRI_VISUAL_DIR, { recursive: true });
        await target.screenshot({
          path: resolve(process.env.NONBIRI_VISUAL_DIR, `${name}-${width}-${theme}.png`),
        });
      }
    }
  }
}

test('owner, admin and steward release cooldowns and show consistent station times', async ({
  browser,
}) => {
  const f = fixture();
  for (const role of ['owner', 'admin', 'steward'] as const) {
    const { context, page, origin, errors } = await session(browser, role);
    try {
      await control(page, 'health');
      const id = f.management.resource_health.donation_id;
      const path =
        role === 'owner'
          ? `/charity/donations/${id}`
          : role === 'admin'
            ? `/charity?donation_id=${id}`
            : `/steward?tab=charity&donation_id=${id}`;
      await page.goto(origin + path);
      const health = page.getByRole('region', { name: 'Service health', exact: true }).first();
      const controls = page.locator('.donation-key-use-controls').first();
      await expect(health).toContainText('10 consecutive failures');
      await controls.getByRole('button', { name: 'Release rate limit', exact: true }).click();
      await expect(health).not.toContainText('Rate limit wait until');
      await expect(health).toContainText('10 consecutive failures');
      await expect(health).toContainText('Paused after consecutive failures');

      const { until } = await control(page, 'health');
      await page.reload();
      await expect(health).toContainText('Rate limit wait until');
      const expected = new Intl.DateTimeFormat('en-US', {
        year: 'numeric',
        month: '2-digit',
        day: '2-digit',
        hour: '2-digit',
        minute: '2-digit',
        timeZone: role === 'owner' ? 'America/New_York' : 'UTC',
      }).format(new Date((until + (role === 'owner' ? 0 : 480 * 60)) * 1000));
      await expect(health).toContainText('Rate limit wait until ' + expected);
      await controls
        .getByRole('button', { name: 'Reset failures and resume services', exact: true })
        .click();
      await expect(controls).toContainText('Rate-limit wait retained until ' + expected);
      await expect(controls).toContainText('Smart backoff retained until ' + expected);
      await expect(health).not.toContainText('10 consecutive failures');
      await expect(health).toContainText('Rate limit wait until ' + expected);
      await screenshots(page, controls.locator('..'), 'health-' + role);
      await controls.getByRole('button', { name: 'Release rate limit', exact: true }).click();
      await expect(health).not.toContainText('Rate limit wait until');
      expect(errors).toEqual([]);
    } finally {
      await context.close();
    }
  }
});

test('admin configures sparse periods and the seventh independent sign-in automatically awards', async ({
  browser,
}) => {
  const admin = await session(browser, 'admin');
  const user = await session(browser, 'owner');
  try {
    await admin.page.goto(admin.origin + '/activities');
    await admin.page.getByText('Master enabled', { exact: true }).click();
    await expect(
      admin.page.getByRole('switch', { name: 'Master enabled', exact: true }),
    ).toBeChecked();
    const [enabled] = await Promise.all([
      admin.page.waitForResponse(
        (r) => r.url().endsWith('/admin/api/activities/config') && r.request().method() === 'PATCH',
      ),
      admin.page.getByRole('button', { name: 'Save activity settings', exact: true }).click(),
    ]);
    expect(enabled.ok()).toBe(true);
    const management = admin.page.getByRole('region', { name: 'Sign-in activities', exact: true });
    await expect(management.getByText('No sign-in activities', { exact: true })).toBeVisible();
    await management.getByRole('button', { name: 'Create activity', exact: true }).click();
    const dialog = admin.page.getByRole('dialog');
    const title = 'Slow afternoons · Seven days together';
    await dialog.getByRole('textbox', { name: 'Activity title' }).fill(title);
    await dialog
      .getByRole('textbox', { name: 'Description', exact: true })
      .fill('A daily moment with our five companions. **Rewards arrive with your sign-in.**');
    await dialog.getByRole('spinbutton', { name: 'Required days' }).fill('8');
    await dialog.getByRole('button', { name: 'Save activity' }).click();
    await expect(dialog).toContainText('the date range must cover the highest reward day');
    await expect(dialog.getByRole('textbox', { name: 'Activity title' })).toHaveValue(title);
    await dialog.getByRole('spinbutton', { name: 'Required days' }).fill('1');
    await dialog.getByRole('button', { name: 'Add milestone' }).click();
    await dialog.getByRole('spinbutton', { name: 'Required days' }).nth(1).fill('7');
    await screenshots(admin.page, dialog, 'attendance-editor');
    const created = admin.page.waitForResponse(
      (r) => r.url().endsWith('/admin/api/attendance') && r.request().method() === 'POST' && r.ok(),
    );
    await dialog.getByRole('button', { name: 'Save activity' }).click();
    const period = (await (await created).json()) as AttendancePeriod;
    await expect(dialog).not.toBeVisible();
    const second = await admin.page.request.post(admin.origin + '/admin/api/attendance', {
      headers: { Origin: admin.origin, 'Idempotency-Key': randomUUID() },
      data: {
        title: 'A steady little streak',
        description: 'Missed days restart the streak. Earned rewards stay yours.',
        start_date: period.start_date,
        end_date: period.end_date,
        mode: 'consecutive',
        published: true,
        tiers: [
          { days: 1, rewards: [{ asset: 'game', quantity: '2' }] },
          { days: 3, rewards: [{ asset: 'general', quantity: '5' }] },
        ],
      },
    });
    expect(second.ok()).toBe(true);
    await user.page.goto(user.origin + '/activities');
    let first = user.page.getByRole('region', { name: title, exact: true });
    await expect(first.getByRole('button', { name: 'Not started' })).toBeDisabled();
    await control(user.page, 'advance', { seconds: 86400 });
    let firstDate = '';
    let firstKey = '';
    for (let day = 1; day <= 7; day++) {
      if (day > 1) await control(user.page, 'advance', { seconds: 86400 });
      await user.page.reload();
      first = user.page.getByRole('region', { name: title, exact: true });
      const [raw] = await Promise.all([
        user.page.waitForResponse(
          (r) =>
            r.url().endsWith(`/api/attendance/${period.id}/sign-in`) &&
            r.request().method() === 'POST',
        ),
        first.getByRole('button', { name: 'Sign in today', exact: true }).click(),
      ]);
      expect(raw.ok()).toBe(true);
      const result = (await raw.json()) as SignInResult;
      expect(result.period.total_days).toBe(day);
      expect(result.awards.map((award) => award.days)).toEqual(day === 1 || day === 7 ? [day] : []);
      await expect(first.getByRole('button', { name: 'Signed in today' })).toBeDisabled();
      const other = user.page.getByRole('region', { name: 'A steady little streak', exact: true });
      await expect(other.getByRole('button', { name: 'Sign in today' })).toBeEnabled();
      if (day === 1 || day === 3) {
        const otherResponse = user.page.waitForResponse(
          (r) =>
            r.url().includes('/api/attendance/') &&
            r.url().endsWith('/sign-in') &&
            r.request().method() === 'POST',
        );
        await other.getByRole('button', { name: 'Sign in today' }).click();
        const otherResult = (await (await otherResponse).json()) as SignInResult;
        expect(otherResult.period.current_streak).toBe(1);
        expect(otherResult.awards.map((award) => award.days)).toEqual(day === 1 ? [1] : []);
      }
      if (day === 1) {
        firstDate = result.site_date;
        firstKey = raw.request().headers()['idempotency-key'];
      }
    }
    const replay = await user.page.request.post(
      user.origin + `/api/attendance/${period.id}/sign-in`,
      {
        headers: { Origin: user.origin, 'Idempotency-Key': firstKey },
        data: { site_date: firstDate },
      },
    );
    expect(replay.ok()).toBe(true);
    const replayed = (await replay.json()) as SignInResult;
    expect(replayed.site_date).toBe(firstDate);
    expect(replayed.period.total_days).toBe(7);
    expect(replayed.awards.map((award) => award.days)).toEqual([1]);
    const section = user.page.getByRole('region', { name: 'Sign-in activities', exact: true });
    await expect(section).toContainText('UTC+08:00');
    await expect(first.locator('.attendance-tier.is-awarded')).toHaveCount(2);
    await expect(first.getByRole('button', { name: /Claim/ })).toHaveCount(0);
    await screenshots(user.page, section, 'attendance-user');
    await admin.page.reload();
    await management
      .getByRole('region', { name: title, exact: true })
      .getByRole('button', { name: 'Edit activity' })
      .click();
    await expect(dialog.getByLabel('Start date')).toBeDisabled();
    await expect(dialog.getByRole('textbox', { name: 'Activity title' })).toBeEnabled();
    const listing = await user.page.request.get(
      user.origin + '/api/attendance?page=1&page_size=20',
    );
    const list = (await listing.json()) as AttendancePage;
    expect(list.items).toHaveLength(2);
    expect(list.items.every((entry) => entry.status === 'active')).toBe(true);
    expect(admin.errors).toEqual([]);
    expect(user.errors).toEqual([]);
  } finally {
    await admin.context.close();
    await user.context.close();
  }
});
