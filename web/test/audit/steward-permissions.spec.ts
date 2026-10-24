import { mkdirSync, readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { randomUUID } from 'node:crypto';
import { expect, test, type Browser, type BrowserContext } from '@playwright/test';
import copy from '../../src/shared/i18n/common/en.json' with { type: 'json' };

interface Fixture {
  admin_url: string;
  user_url: string;
  admin_cookie: { Name: string; Value: string };
  users: { id: string; level: number; cookie: { Name: string; Value: string } }[];
}
interface Policy {
  revision: string;
  donation_scope: 'none' | 'standard' | 'enhanced' | 'full';
  permissions: Record<string, boolean>;
  limits: Record<string, string>;
}
const fixture = () =>
  JSON.parse(readFileSync(process.env.NONBIRI_AUDIT_BROWSER_STATE!, 'utf8')) as Fixture;
test.skip(process.env.NONBIRI_MANAGEMENT_BROWSER !== '1', 'Requires management fixtures.');

async function session(browser: Browser, state: Fixture, role: 'admin' | 5 | 6) {
  const origin = role === 'admin' ? state.admin_url : state.user_url;
  const cookie =
    role === 'admin' ? state.admin_cookie : state.users.find((user) => user.level === role)!.cookie;
  const context = await browser.newContext({ viewport: { width: 1440, height: 900 } });
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
  return context;
}
async function get(context: BrowserContext, origin: string, path: string) {
  const response = await context.request.get(origin + path, { headers: { Origin: origin } });
  expect(response.ok()).toBe(true);
  return response.json();
}

test('admin and level 6 configure actual level-5 permissions while the shared UI retains hierarchy', async ({
  browser,
}) => {
  const state = fixture();
  const five = state.users.find((user) => user.level === 5)!;
  const six = state.users.find((user) => user.level === 6)!;
  const owner = state.users.find((user) => user.level === 1)!;
  const admin = await session(browser, state, 'admin');
  const steward = await session(browser, state, 5);
  const senior = await session(browser, state, 6);
  const path = `/admin/api/users/${five.id}/steward-permissions`;
  const original = (await get(admin, state.admin_url, path)) as Policy;
  try {
    const page = await admin.newPage();
    const errors: string[] = [];
    page.on('pageerror', (error) => errors.push(error.message));
    await page.goto(`${state.admin_url}/users?user=${five.id}`);
    await page.getByRole('tab', { name: copy.management.permissions.title, exact: true }).click();
    await expect(
      page.getByRole('table', { name: copy.management.permissions.title }),
    ).toBeVisible();
    const visual = process.env.NONBIRI_VISUAL_DIR;
    if (visual) {
      mkdirSync(visual, { recursive: true });
      for (const theme of ['light', 'dark']) {
        await page.evaluate((value) => {
          document.documentElement.dataset.theme = value;
        }, theme);
        for (const width of [1440, 390]) {
          await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
          await page
            .getByRole('heading', { name: copy.management.permissions.title, exact: true })
            .scrollIntoViewIfNeeded();
          expect(
            await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth),
          ).toBe(true);
          await page.screenshot({ path: resolve(visual, `permissions-${theme}-${width}.png`) });
        }
      }
      await page
        .getByRole('heading', { name: copy.management.permissions.limitsTitle })
        .scrollIntoViewIfNeeded();
      await page.screenshot({ path: resolve(visual, 'permission-limits-mobile.png') });
    }
    await page.getByRole('button', { name: copy.management.permissions.none, exact: true }).click();
    await page.getByRole('button', { name: copy.management.permissions.save, exact: true }).click();
    await expect(page.getByText(copy.common.outcome.saved, { exact: true })).toBeVisible();
    const noGrants = (await get(admin, state.admin_url, path)) as Policy;
    expect(noGrants.donation_scope).toBe('none');
    expect(Object.values(noGrants.permissions).every((value) => !value)).toBe(true);
    const visitor = await steward.newPage();
    await visitor.goto(`${state.user_url}/steward?tab=users`);
    await expect(
      visitor.getByText(copy.management.permissions.denied, { exact: true }),
    ).toBeVisible();
    await expect(visitor.getByRole('tab', { name: 'Users', exact: true })).toBeVisible();
    const me = await get(steward, state.user_url, '/api/me');
    expect(me.user.effective_level).toBe(5);
    const denied = await steward.request.get(state.user_url + '/api/steward/users');
    expect(denied.status()).toBe(403);
    expect((await denied.json()).error.code).toBe('steward_permission_denied');

    await page.getByRole('button', { name: copy.management.permissions.all, exact: true }).click();
    await page.getByRole('button', { name: copy.management.permissions.save, exact: true }).click();
    await expect(page.getByText(copy.common.outcome.saved, { exact: true })).toBeVisible();
    const full = (await get(admin, state.admin_url, path)) as Policy;
    expect(full.donation_scope).toBe('full');
    expect(Object.keys(full.permissions)).toHaveLength(48);
    await visitor.goto(`${state.user_url}/steward/endpoints`);
    await expect(
      visitor.getByRole('heading', { name: copy.management.endpoints.title, exact: true }),
    ).toBeVisible();
    await expect(
      visitor.getByRole('button', { name: copy.management.endpoints.addTag, exact: true }),
    ).toBeVisible();
    await visitor.goto(`${state.user_url}/steward?tab=users&user=${owner.id}`);
    await visitor
      .getByRole('tab', { name: copy.management.permissions.history, exact: true })
      .click();
    await expect(
      visitor.getByRole('heading', { name: copy.common.donationHistory.title, exact: true }),
    ).toBeVisible();
    await expect(
      visitor.getByText('The server returned an invalid response.', { exact: true }),
    ).toHaveCount(0);
    await expect(
      visitor.getByRole('tab', { name: copy.management.permissions.title, exact: true }),
    ).toHaveCount(0);
    const higher = await get(steward, state.user_url, `/api/steward/users/${six.id}`);
    const higherEdit = await steward.request.patch(
      `${state.user_url}/api/steward/users/${six.id}`,
      {
        headers: { Origin: state.user_url, 'Idempotency-Key': randomUUID() },
        data: { mode: 'profile', expected_revision: higher.revision, light_rpm_limit: '1' },
      },
    );
    expect(higherEdit.status()).toBe(403);
    const forbiddenDelegate = await steward.request.get(
      `${state.user_url}/api/steward/users/${five.id}/steward-permissions`,
    );
    expect(forbiddenDelegate.status()).toBe(403);
    await page
      .getByRole('button', { name: copy.management.permissions.defaults, exact: true })
      .click();
    const scope = page.getByRole('slider', {
      name: copy.management.permissions.scope.title,
      exact: true,
    });
    await expect(scope).toHaveValue('1');
    await scope.focus();
    await scope.press('ArrowRight');
    await expect(scope).toHaveValue('2');
    await page.getByRole('button', { name: copy.management.permissions.save, exact: true }).click();
    await expect(page.getByText(copy.common.outcome.saved, { exact: true })).toBeVisible();
    expect(((await get(admin, state.admin_url, path)) as Policy).donation_scope).toBe('enhanced');
    const seniorPolicy = (await get(
      senior,
      state.user_url,
      `/api/steward/users/${five.id}/steward-permissions`,
    )) as Policy;
    const restore = await senior.request.put(
      `${state.user_url}/api/steward/users/${five.id}/steward-permissions`,
      {
        headers: { Origin: state.user_url, 'Idempotency-Key': randomUUID() },
        data: {
          expected_revision: seniorPolicy.revision,
          donation_scope: original.donation_scope,
          permissions: original.permissions,
          limits: original.limits,
        },
      },
    );
    expect(restore.ok()).toBe(true);
    expect(errors).toEqual([]);
  } finally {
    try {
      const current = (await get(admin, state.admin_url, path)) as Policy;
      const restored = await admin.request.put(state.admin_url + path, {
        headers: { Origin: state.admin_url, 'Idempotency-Key': randomUUID() },
        data: {
          expected_revision: current.revision,
          permissions: original.permissions,
          limits: original.limits,
        },
      });
      expect(restored.ok()).toBe(true);
    } finally {
      await Promise.all([admin.close(), steward.close(), senior.close()]);
    }
  }
});
