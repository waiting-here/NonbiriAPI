import { numberedResponse } from './numbered-fixtures';
import { ADMIN_ORIGIN, USER_ORIGIN } from './ports';
import {
  collectConsoleViolations,
  mockJson,
  mockPublicConfig,
  mockRoleSession,
  userSession,
} from './support';
import { expect, test } from './test';

test('user management separates identifiers and copies Discord IDs exactly at desktop and mobile sizes', async ({
  page,
  context,
}) => {
  const errors = collectConsoleViolations(page);
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.addInitScript(() => localStorage.setItem('nb.lang', 'en'));
  await mockRoleSession(page, 'admin', 'admin');
  await mockPublicConfig(page, 'admin');
  await mockJson(page, {
    origin: ADMIN_ORIGIN,
    method: 'GET',
    path: '/admin/api/maintenance',
    body: { enabled: false, revision: '1' },
  });
  await context.grantPermissions(['clipboard-read', 'clipboard-write'], { origin: ADMIN_ORIGIN });
  const user = {
    ...Object.fromEntries(
      Object.entries(userSession('user').user).filter(
        ([key]) =>
          ![
            'avatar',
            'effective_level',
            'level_display_name',
            'charity_profile_public',
            'automatic_restrictions',
          ].includes(key),
      ),
    ),
    id: '7',
    discord_id: '1234567890123456789',
    is_admin: false,
    discord_gate_policy: 'inherit',
    banned_reason: '',
    level: { manual: null, automatic: 1, effective: 1, display_name: 'Lv1' },
    revision: '1',
  };
  await mockJson(page, {
    origin: ADMIN_ORIGIN,
    method: 'GET',
    path: '/admin/api/users?account_state=all&page=1&page_size=20',
    body: numberedResponse([user], '1', 20),
  });
  await page.setViewportSize({ width: 1935, height: 1000 });
  await page.goto(`${ADMIN_ORIGIN}/users`);
  const table = page.locator('.nb-md__list .nb-table');
  await expect(table.getByRole('columnheader', { name: 'Username', exact: true })).toBeVisible();
  const identity = table.locator('tbody tr td').first();
  await expect(identity).toContainText('fixture-user');
  await expect(identity).toContainText('#7');
  await expect(identity).toContainText('1234…6789');
  await mockJson(page, {
    origin: ADMIN_ORIGIN,
    method: 'GET',
    path: '/admin/api/users/7',
    body: user,
  });
  await identity.getByRole('button', { name: 'fixture-user', exact: true }).click();
  await page.getByRole('button', { name: 'Copy Discord ID', exact: true }).click();
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(user.discord_id);
  for (const width of [320, 390, 1440, 1935]) {
    await page.setViewportSize({ width, height: 1000 });
    await expect
      .poll(() => page.evaluate(() => document.documentElement.scrollWidth <= innerWidth))
      .toBe(true);
    await expect(
      page.locator('.nb-md__detail').getByText(user.discord_id, { exact: true }),
    ).toBeVisible();
    const box = await page
      .locator('.nb-md__detail')
      .getByText(user.discord_id, { exact: true })
      .boundingBox();
    expect(box!.x).toBeGreaterThanOrEqual(0);
    expect(box!.x + box!.width).toBeLessThanOrEqual(width);
  }
  errors.assertNone();
});

for (const locale of ['en', 'zh'] as const) {
  for (const role of ['user', 'level5', 'level6'] as const) {
    test(`user shell keeps desktop navigation in one row and mobile drawer controls ${locale}/${role}`, async ({
      page,
    }) => {
      const guard = collectConsoleViolations(page);
      await page.emulateMedia({ reducedMotion: 'reduce' });
      await page.addInitScript((language) => {
        localStorage.setItem('nb.lang', language);
        localStorage.setItem('nb.theme', language === 'zh' ? 'dark' : 'light');
      }, locale);
      await mockPublicConfig(page, 'user');
      await mockRoleSession(page, 'user', role);
      const displayName = 'Example account with a long display name';
      const session = userSession(role);
      session.user.username = displayName;
      await mockJson(page, {
        origin: USER_ORIGIN,
        method: 'GET',
        path: '/api/session',
        body: session,
      });
      await page.route(`${USER_ORIGIN}/api/endpoints?**`, (route) =>
        route.fulfill({ json: numberedResponse([], '1', 20) }),
      );
      await page.setViewportSize({ width: 1440, height: 900 });
      await page.goto(`${USER_ORIGIN}/endpoints`);
      const nav = page.locator('#user-navigation');
      const toggle = page.locator('.nb-menu-button');
      const account = page.locator('.nb-account-trigger');
      await expect(account).toBeVisible();
      for (const width of [1440, 1280, 1152, 1151, 1024, 768, 390]) {
        await page.setViewportSize({ width, height: width < 1024 ? 1024 : 900 });
        await expect
          .poll(() => page.evaluate(() => document.documentElement.scrollWidth <= innerWidth))
          .toBe(true);
        if (process.env.NONBIRI_VISUAL_DIR)
          await page.screenshot({
            path: `${process.env.NONBIRI_VISUAL_DIR}/shell-${role}-${locale}-${width}.png`,
          });
        if (width >= 1024) {
          await expect(toggle).toBeHidden();
          await expect(nav).toBeVisible();
          const boxes = await nav.locator('li').evaluateAll((nodes) =>
            nodes.map((node) => {
              const box = node.getBoundingClientRect();
              return { top: box.top, left: box.left, right: box.right };
            }),
          );
          expect(new Set(boxes.map((box) => Math.round(box.top))).size).toBe(1);
          const brand = (await page.locator('.nb-user-header .nb-brand').boundingBox())!;
          const actions = (await page.locator('.nb-user-header__actions').boundingBox())!;
          expect(
            boxes[0].left,
            JSON.stringify({ width, boxes, brand, actions }),
          ).toBeGreaterThanOrEqual(brand.x + brand.width);
          expect(boxes.at(-1)!.right).toBeLessThanOrEqual(actions.x);
        } else {
          await expect(toggle).toBeVisible();
          await expect(nav).toBeHidden();
          const clippedName = (await account.locator('.nb-account-trigger__name').boundingBox())!;
          expect(clippedName.width).toBeLessThanOrEqual(1);
          await account.click();
          await expect(page.locator('.nb-account-menu')).toBeVisible();
          await page.keyboard.press('Escape');
          await expect(account).toBeFocused();
          await toggle.click();
          await expect(nav).toBeVisible();
          await expect(nav.locator('a').first()).toBeFocused();
          const tops = await nav
            .locator('li')
            .evaluateAll((nodes) => nodes.map((node) => node.getBoundingClientRect().top));
          expect(new Set(tops).size).toBe(tops.length);
          await page.keyboard.press('Escape');
          await expect(nav).toBeHidden();
          await expect(toggle).toBeFocused();
          const actions = page.locator('[data-component="page-header"] .nb-page-header__actions');
          const buttons = actions.locator('button, a');
          if (width < 640 && (await buttons.count()) >= 2) {
            const first = (await buttons.nth(0).boundingBox())!;
            const second = (await buttons.nth(1).boundingBox())!;
            expect(Math.abs(first.y - second.y)).toBeLessThan(1);
            expect(Math.abs(first.width - second.width)).toBeLessThan(1);
          }
        }
      }
      await toggle.click();
      await expect(nav).toBeVisible();
      await page.setViewportSize({ width: 1024, height: 900 });
      await expect(toggle).toBeHidden();
      await expect(toggle).toHaveAttribute('aria-expanded', 'false');
      expect(await page.evaluate(() => document.documentElement.style.overflow)).not.toBe('hidden');
      guard.assertNone();
    });
  }
}
for (const locale of ['en', 'zh'] as const) {
  test(`empty announcements and single-page charity keep compact pagers ${locale}`, async ({
    page,
  }) => {
    const guard = collectConsoleViolations(page);
    await page.addInitScript((language) => {
      localStorage.setItem('nb.lang', language);
      localStorage.setItem('nb.theme', language === 'zh' ? 'dark' : 'light');
    }, locale);
    await mockRoleSession(page, 'user', 'user');
    await mockPublicConfig(page, 'user');
    await page.route(`${USER_ORIGIN}/api/announcements?**`, (route) =>
      route.fulfill({ json: numberedResponse([], '1', 20) }),
    );
    await page.route(`${USER_ORIGIN}/api/donations?**`, (route) =>
      route.fulfill({ json: numberedResponse([], '1', 20) }),
    );
    const model = {
      model_types: ['chat_completions', 'embeddings'],
      id: '1',
      provider: 'Example',
      model: 'chat',
      full_name: '[公益]Example/chat',
      pricing: {
        mode: 'per_request',
        user_price_milli: '1000',
        discounted_user_price_milli: '1000',
        user_prices_milli: null,
        discounted_user_prices_milli: null,
      },
      discount: { enabled: false, percent: 100, start_at: 0, end_at: 0 },
    };
    await page.route(`${USER_ORIGIN}/api/charity/models**`, (route) => {
      const catalog = new URL(route.request().url()).searchParams.get('view') === 'catalog';
      return route.fulfill({
        json: catalog
          ? {
              models: [
                {
                  ...model,
                  public_description: '',
                  enabled: true,
                  allowed_levels: [1, 2, 3, 4, 5],
                  level_allowed: true,
                  availability: 'available',
                  currently_available: true,
                },
              ],
              pagination: numberedResponse([model], '1', 20).pagination,
              donation_intake: 'closed',
              server_now: 1_800_000_000,
            }
          : {
              state: 'available',
              models: [model],
              donation_intake: 'closed',
              server_now: 1_800_000_000,
            },
      });
    });
    for (const width of [1440, 768, 390]) {
      await page.setViewportSize({ width, height: width === 1440 ? 900 : 1024 });
      await page.goto(`${USER_ORIGIN}/announcements`);
      await expect(page.locator('.empty-state')).toBeVisible();
      await expect(page.locator('.page-pagination')).toHaveCount(0);
      if (process.env.NONBIRI_VISUAL_DIR)
        await page.screenshot({
          path: `${process.env.NONBIRI_VISUAL_DIR}/announcements-empty-${locale}-${width}.png`,
        });
      await page.goto(`${USER_ORIGIN}/charity`);
      await expect(page.getByText('[公益]Example/chat', { exact: true }).first()).toBeVisible();
      const pager = page.locator('.page-pagination');
      await expect(pager).toHaveCount(1);
      await expect(pager.locator('button, input, select')).toHaveCount(0);
      await expect
        .poll(() => page.evaluate(() => document.documentElement.scrollWidth <= innerWidth))
        .toBe(true);
      if (process.env.NONBIRI_VISUAL_DIR)
        await page.screenshot({
          path: `${process.env.NONBIRI_VISUAL_DIR}/charity-one-page-${locale}-${width}.png`,
        });
    }
    guard.assertNone();
  });
}
