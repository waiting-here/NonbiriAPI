import { mkdirSync, readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { randomUUID } from 'node:crypto';
import { expect, test, type Browser, type BrowserContext } from '@playwright/test';
import copy from '../../src/shared/i18n/common/en.json' with { type: 'json' };
import type { ManagedResource } from '../../src/shared/operations/managedResources';

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

test('resource inventory is read only for default level 5 and adjustable through the admin form', async ({
  browser,
}) => {
  const state = fixture();
  const owner = state.users.find((user) => user.level === 1)!;
  const admin = await session(browser, state, 'admin');
  const steward = await session(browser, state, 5);
  const senior = await session(browser, state, 6);
  const adminPath = `/admin/api/users/${owner.id}/resources`;
  const stewardPath = `/api/steward/users/${owner.id}/resources`;
  let adjusted = false;
  const original = (
    (await get(admin, state.admin_url, adminPath)).resources as ManagedResource[]
  ).find((r) => r.id === 'lake:coins')!;
  try {
    const visitor = await steward.newPage();
    await visitor.goto(`${state.user_url}/steward?tab=users&user=${owner.id}`);
    await visitor.getByRole('tab', { name: copy.management.resources.title, exact: true }).click();
    await expect(
      visitor.getByText(copy.management.resources.readOnly, { exact: true }),
    ).toBeVisible();
    await expect(
      visitor.getByRole('button', { name: copy.management.resources.adjust, exact: true }),
    ).toHaveCount(0);
    const intent = {
      asset: 'lake:coins',
      direction: 'increase',
      quantity: '7',
      expected_quantity: original.quantity,
      reason: 'Resource browser verification',
    };
    const denied = await steward.request.patch(state.user_url + stewardPath, {
      headers: { Origin: state.user_url, 'Idempotency-Key': randomUUID() },
      data: intent,
    });
    expect(denied.status()).toBe(403);
    expect((await denied.json()).error.code).toBe('steward_permission_denied');
    const page = await admin.newPage();
    await page.goto(`${state.admin_url}/users?user=${owner.id}`);
    await page.getByRole('tab', { name: copy.management.resources.title, exact: true }).click();
    const resources = page.getByRole('region', {
      name: copy.management.resources.title,
      exact: true,
    });
    await resources
      .getByLabel(copy.management.resources.search, { exact: true })
      .fill('Lake coins');
    await resources
      .getByRole('button', { name: copy.management.resources.adjust, exact: true })
      .click();
    await resources.getByLabel(copy.management.resources.quantity, { exact: true }).fill('7');
    await resources
      .getByLabel(copy.management.resources.reason, { exact: true })
      .fill(intent.reason);
    await resources
      .getByRole('button', { name: copy.management.resources.review, exact: true })
      .click();
    const dialog = page.getByRole('alertdialog', { name: copy.management.resources.confirmTitle });
    await expect(dialog).toContainText(`${original.quantity} → ${BigInt(original.quantity) + 7n}`);
    await dialog
      .getByRole('button', { name: copy.management.resources.confirm, exact: true })
      .click();
    await expect(resources.getByText(copy.common.outcome.saved, { exact: true })).toBeVisible();
    adjusted = true;
    const current = (
      (await get(admin, state.admin_url, adminPath)).resources as ManagedResource[]
    ).find((r) => r.id === original.id)!;
    expect(current.quantity).toBe((BigInt(original.quantity) + 7n).toString());
    const visual = process.env.NONBIRI_VISUAL_DIR;
    if (visual) {
      mkdirSync(visual, { recursive: true });
      for (const theme of ['light', 'dark'])
        for (const width of [1440, 390]) {
          await page.setViewportSize({ width, height: 900 });
          await page.evaluate((value) => {
            document.documentElement.dataset.theme = value;
          }, theme);
          await page
            .getByRole('heading', { name: copy.management.resources.title, exact: true })
            .scrollIntoViewIfNeeded();
          expect(
            await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth),
          ).toBe(true);
          await resources.screenshot({
            path: resolve(visual, `resources-${theme}-${width}.png`),
            animations: 'disabled',
          });
        }
    }
  } finally {
    try {
      if (adjusted) {
        const restored = await senior.request.patch(state.user_url + stewardPath, {
          headers: { Origin: state.user_url, 'Idempotency-Key': randomUUID() },
          data: {
            asset: original.id,
            direction: 'decrease',
            quantity: '7',
            expected_quantity: (BigInt(original.quantity) + 7n).toString(),
            reason: 'Restore browser fixture inventory',
          },
        });
        expect.soft(restored.ok(), `Restore resources: HTTP ${restored.status()}`).toBe(true);
      }
    } finally {
      await Promise.all([admin.close(), steward.close(), senior.close()]);
    }
  }
});

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
      page.getByRole('navigation', { name: copy.management.permissions.categories }),
    ).toBeVisible();
    await page
      .getByRole('button', { name: new RegExp(copy.management.permissions.groups.safety.title) })
      .click();
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
            .getByRole('navigation', { name: copy.management.permissions.categories })
            .scrollIntoViewIfNeeded();
          expect(
            await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth),
          ).toBe(true);
          await page.screenshot({
            path: resolve(visual, `permissions-${theme}-${width}.png`),
            animations: 'disabled',
          });
          await page
            .getByRole('region', {
              name: copy.management.permissions.groups.safety.title,
              exact: true,
            })
            .screenshot({
              path: resolve(visual, `permission-controls-${theme}-${width}.png`),
              animations: 'disabled',
            });
        }
      }
      await page
        .getByRole('button', { name: copy.management.permissions.limitsTitle, exact: true })
        .click();
      await page
        .getByRole('heading', { name: copy.management.permissions.limitsTitle })
        .scrollIntoViewIfNeeded();
      await page.screenshot({
        path: resolve(visual, 'permission-limits-mobile.png'),
        animations: 'disabled',
      });
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
    expect(Object.keys(full.permissions).sort()).toEqual(Object.keys(original.permissions).sort());
    expect(Object.values(full.permissions).every((value) => value)).toBe(true);
    expect(full.permissions).toMatchObject({
      'ai.chat': true,
      'ai.review.read': true,
      'ai.memory.edit': true,
      'risk.configure': true,
    });
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
          donation_scope: original.donation_scope,
          permissions: original.permissions,
          limits: original.limits,
        },
      });
      expect.soft(restored.ok(), `Restore permission policy: HTTP ${restored.status()}`).toBe(true);
    } finally {
      await Promise.all([admin.close(), steward.close(), senior.close()]);
    }
  }
});
