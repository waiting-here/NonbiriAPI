import { readFileSync } from 'node:fs';
import { randomUUID } from 'node:crypto';
import { resolve } from 'node:path';
import {
  expect,
  test,
  type Browser,
  type BrowserContext,
  type Page,
  type TestInfo,
} from '@playwright/test';
import common from '../../src/shared/i18n/common/en.json' with { type: 'json' };

type Cookie = { Name: string; Value: string };
type Identity = { connector_type: string; base_url: string };
type Rule = {
  kind: 'code' | 'message';
  value: string;
  period: 'auto' | 'five_hour' | 'day' | 'week' | 'month';
};
type Policy = Identity & { revision: string; rules: Rule[] };
interface Fixture {
  admin_url: string;
  user_url: string;
  admin_cookie: Cookie;
  users: { id: string; level: number; cookie: Cookie }[];
  management: { endpoint_policy: Identity; worker_origin: string };
}
const copy = common.common.endpointPolicies;
function fixture(): Fixture {
  return JSON.parse(readFileSync(process.env.NONBIRI_AUDIT_BROWSER_STATE!, 'utf8')) as Fixture;
}
test.skip(
  process.env.NONBIRI_MANAGEMENT_BROWSER !== '1',
  'Requires the isolated management fixture.',
);
async function session(browser: Browser, role: 'admin' | 1 | 5 | 6) {
  const state = fixture();
  const origin = role === 'admin' ? state.admin_url : state.user_url;
  const cookie =
    role === 'admin' ? state.admin_cookie : state.users.find((item) => item.level === role)!.cookie;
  const context = await browser.newContext({
    viewport: { width: 1440, height: 900 },
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
  return { context, origin, prefix: role === 'admin' ? '/admin/api' : '/api/steward' };
}
async function read<T>(context: BrowserContext, origin: string, path: string): Promise<T> {
  const response = await context.request.get(origin + path, { headers: { Origin: origin } });
  expect(response.ok(), path + ': ' + response.status()).toBe(true);
  return response.json();
}
const targetQuery = (target: Identity) => new URLSearchParams(target).toString();
async function policy(context: BrowserContext, origin: string, prefix: string) {
  return read<Policy>(
    context,
    origin,
    `${prefix}/endpoint-backoff-policy?${targetQuery(fixture().management.endpoint_policy)}`,
  );
}
async function save(context: BrowserContext, origin: string, prefix: string, rules: Rule[]) {
  const before = await policy(context, origin, prefix);
  const response = await context.request.put(origin + prefix + '/endpoint-backoff-policy', {
    headers: { Origin: origin, 'Idempotency-Key': randomUUID() },
    data: { ...fixture().management.endpoint_policy, expected_revision: before.revision, rules },
  });
  expect(response.ok(), 'Synthetic policy setup: ' + response.status()).toBe(true);
  return response.json() as Promise<Policy>;
}
async function visuals(page: Page, info: TestInfo, name: string, focus: () => Promise<void>) {
  for (const theme of ['light', 'dark']) {
    await page.evaluate((value) => {
      document.documentElement.dataset.theme = value;
    }, theme);
    for (const width of [1440, 390]) {
      await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
      await focus();
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
        true,
      );
      await page.screenshot({
        path: process.env.NONBIRI_VISUAL_DIR
          ? resolve(process.env.NONBIRI_VISUAL_DIR, `${name}-${theme}-${width}.png`)
          : info.outputPath(`${name}-${theme}-${width}.png`),
        animations: 'disabled',
      });
    }
  }
  await page.setViewportSize({ width: 1440, height: 900 });
}

test('endpoint list keeps numbered pagination, draft and focus through route inspection', async ({
  browser,
}, info) => {
  const { context, origin, prefix } = await session(browser, 'admin');
  try {
    const list = await read<{ data: Identity[]; pagination: { total_items: string } }>(
      context,
      origin,
      prefix + '/endpoint-backoff-policies?page=2&page_size=10',
    );
    expect(BigInt(list.pagination.total_items)).toBeGreaterThanOrEqual(21n);
    const target = list.data[0]!;
    const page = await context.newPage();
    await page.goto(origin + '/endpoint-policies?page=2&page_size=10');
    const rulesPanel = page.getByRole('tabpanel', { name: copy.rulesTab, exact: true });
    const routePanel = page.getByRole('tabpanel', { name: copy.routesTab, exact: true });
    const row = page.getByRole('row').filter({ hasText: target.base_url });
    const trigger = row.getByRole('button', { name: copy.edit, exact: true });
    await expect(trigger).toBeVisible();
    await visuals(page, info, 'endpoint-policy-list', async () => {
      await rulesPanel.getByLabel(common.common.pageControls.jump).scrollIntoViewIfNeeded();
    });
    await trigger.click();
    await expect(page.getByRole('heading', { name: copy.editRules, exact: true })).toBeVisible();
    if (await page.getByRole('heading', { name: copy.noRules, exact: true }).isVisible())
      await page.getByRole('button', { name: copy.addRule, exact: true }).click();
    const value = page.getByLabel(copy.valueLabel.replace('{{number}}', '1'), { exact: true });
    await expect(value).toBeVisible();
    await value.fill('retained_endpoint_draft');
    await page.getByRole('tab', { name: copy.routesTab, exact: true }).click();
    await expect(page.getByRole('table', { name: copy.loadedRoutes, exact: true })).toBeVisible();
    await expect(page.getByText(copy.routesHelp, { exact: true })).toBeVisible();
    const loaded = await read<{ mode: string; worker_origin: string }>(
      context,
      origin,
      prefix + '/egress/cloudflare-routes',
    );
    expect(loaded.mode).toBe('worker');
    expect(loaded.worker_origin).toBe(fixture().management.worker_origin);
    expect(new URL(loaded.worker_origin).origin).toBe(loaded.worker_origin);
    await expect(page.getByText(loaded.worker_origin, { exact: true })).toBeVisible();
    await expect(page.getByText(copy.proxyMode.worker, { exact: true })).toBeVisible();
    await expect(page.getByText(copy.legacyInactive, { exact: true })).toBeVisible();
    await routePanel.getByLabel(common.common.pageControls.size).selectOption('10');
    await routePanel.getByLabel(common.common.pageControls.jump).fill('3');
    await routePanel.getByLabel(common.common.pageControls.jump).press('Enter');
    await expect(routePanel.getByLabel(common.common.pageControls.jump)).toHaveValue('3');
    await routePanel.getByRole('button', { name: common.common.previous, exact: true }).click();
    await expect(routePanel.getByLabel(common.common.pageControls.jump)).toHaveValue('2');
    await visuals(page, info, 'endpoint-loaded-routes', async () => {
      await page
        .getByRole('table', { name: copy.loadedRoutes, exact: true })
        .scrollIntoViewIfNeeded();
    });
    await page.getByRole('tab', { name: copy.rulesTab, exact: true }).click();
    await expect(value).toHaveValue('retained_endpoint_draft');
    await page.getByRole('tab', { name: copy.routesTab, exact: true }).click();
    await expect(routePanel.getByLabel(common.common.pageControls.jump)).toHaveValue('2');
    await page.getByRole('tab', { name: copy.rulesTab, exact: true }).click();
    await page.getByRole('button', { name: copy.backList, exact: true }).click();
    await expect(rulesPanel.getByLabel(common.common.pageControls.jump)).toHaveValue('2');
    await expect(trigger).toBeFocused();
    await trigger.click();
    await expect(value).toHaveValue('retained_endpoint_draft');
    await page.getByRole('button', { name: common.common.cancel, exact: true }).click();
    await page.getByRole('button', { name: copy.backList, exact: true }).click();
    await rulesPanel.getByLabel(common.common.pageControls.size).selectOption('20');
    await expect(rulesPanel.getByLabel(common.common.pageControls.jump)).toHaveValue('1');
    await rulesPanel.getByLabel(common.common.pageControls.jump).fill('2');
    await rulesPanel.getByLabel(common.common.pageControls.jump).press('Enter');
    await expect(page).toHaveURL(/page=2/);
    await expect(page).toHaveURL(/page_size=20/);
  } finally {
    await context.close();
  }
});

for (const role of ['admin', 6] as const) {
  test(`${role} edits ordered shared rules, preserves a conflicting draft and clears the policy`, async ({
    browser,
  }, info) => {
    const { context, origin, prefix } = await session(browser, role);
    try {
      await save(context, origin, prefix, [
        { kind: 'code', value: 'old_quota', period: 'auto' },
        { kind: 'message', value: 'old quota message', period: 'day' },
      ]);
      const page = await context.newPage();
      await page.goto(
        origin + '/endpoint-policies?' + targetQuery(fixture().management.endpoint_policy),
      );
      const first = page.getByLabel(copy.valueLabel.replace('{{number}}', '1'), { exact: true });
      await expect(first).toHaveValue('old_quota');
      await first.fill('insufficient_credits');
      await page
        .getByLabel(copy.valueLabel.replace('{{number}}', '2'), { exact: true })
        .fill('weekly quota exhausted');
      await page
        .getByLabel(copy.periodLabel.replace('{{number}}', '2'), { exact: true })
        .selectOption('week');
      await page
        .getByRole('button', { name: copy.moveUpLabel.replace('{{number}}', '2'), exact: true })
        .click();
      await expect(first).toHaveValue('weekly quota exhausted');
      await page.getByRole('button', { name: common.common.save, exact: true }).click();
      await expect(page.getByRole('button', { name: common.common.save, exact: true })).toHaveCount(
        0,
      );
      expect((await policy(context, origin, prefix)).rules).toEqual([
        { kind: 'message', value: 'weekly quota exhausted', period: 'week' },
        { kind: 'code', value: 'insufficient_credits', period: 'auto' },
      ]);
      await first.fill('my retained quota draft');
      const changed = await save(context, origin, prefix, [
        { kind: 'message', value: 'changed elsewhere', period: 'month' },
      ]);
      await page.getByRole('button', { name: common.common.save, exact: true }).click();
      const load = page.getByRole('button', { name: common.common.outcome.reload, exact: true });
      await expect(load).toBeVisible();
      await expect(first).toHaveValue('my retained quota draft');
      await visuals(page, info, `endpoint-${role}-conflict`, async () => {
        await load.scrollIntoViewIfNeeded();
      });
      await load.click();
      await expect(first).toBeEnabled();
      await expect(first).toHaveValue('my retained quota draft');
      await page.getByRole('button', { name: common.common.save, exact: true }).click();
      await expect(page.getByRole('button', { name: common.common.save, exact: true })).toHaveCount(
        0,
      );
      const result = await policy(context, origin, prefix);
      expect(BigInt(result.revision)).toBeGreaterThan(BigInt(changed.revision));
      expect(result.rules[0]?.value).toBe('my retained quota draft');
      await page
        .getByRole('button', { name: copy.removeLabel.replace('{{number}}', '2'), exact: true })
        .click();
      await page
        .getByRole('button', { name: copy.removeLabel.replace('{{number}}', '1'), exact: true })
        .click();
      await page.getByRole('button', { name: common.common.save, exact: true }).click();
      await expect(page.getByRole('button', { name: common.common.save, exact: true })).toHaveCount(
        0,
      );
      expect((await policy(context, origin, prefix)).rules).toEqual([]);
      await expect(page.getByRole('heading', { name: copy.noRules, exact: true })).toBeVisible();
    } finally {
      await context.close();
    }
  });
}

test('a lost policy response retries the original operation without a second revision change', async ({
  browser,
}) => {
  const { context, origin, prefix } = await session(browser, 'admin');
  try {
    const initial = await save(context, origin, prefix, [
      { kind: 'code', value: 'before_retry', period: 'auto' },
    ]);
    const page = await context.newPage();
    await page.goto(
      origin + '/endpoint-policies?' + targetQuery(fixture().management.endpoint_policy),
    );
    const value = page.getByLabel(copy.valueLabel.replace('{{number}}', '1'), { exact: true });
    await expect(value).toHaveValue('before_retry');
    await value.fill('after_retry');
    const writes: { body: unknown; key: string | undefined }[] = [];
    await page.route('**/endpoint-backoff-policy', async (route) => {
      if (route.request().method() !== 'PUT') return route.continue();
      writes.push({
        body: route.request().postDataJSON(),
        key: route.request().headers()['idempotency-key'],
      });
      if (writes.length === 1) {
        const response = await route.fetch();
        expect(response.ok()).toBe(true);
        await route.abort('failed');
      } else await route.continue();
    });
    await page.getByRole('button', { name: common.common.save, exact: true }).click();
    await page
      .getByRole('button', { name: common.common.donationReview.retrySame, exact: true })
      .click();
    await expect(
      page.getByRole('button', { name: common.common.donationReview.retrySame, exact: true }),
    ).toHaveCount(0);
    expect(writes).toHaveLength(2);
    expect(writes[0]).toEqual(writes[1]);
    const result = await policy(context, origin, prefix);
    expect(BigInt(result.revision)).toBe(BigInt(initial.revision) + 1n);
    expect(result.rules[0]?.value).toBe('after_retry');
  } finally {
    await context.close();
  }
});

test('level 5 and ordinary users cannot see the entry or read endpoint policies and routes', async ({
  browser,
}) => {
  for (const role of [5, 1] as const) {
    const { context, origin, prefix } = await session(browser, role);
    try {
      for (const path of [
        '/endpoint-backoff-policies',
        '/endpoint-backoff-options',
        '/egress/cloudflare-routes',
      ]) {
        const response = await context.request.get(origin + prefix + path, {
          headers: { Origin: origin },
        });
        expect(response.status()).toBe(403);
      }
      const page = await context.newPage();
      await page.goto(origin + '/steward?tab=charity');
      await expect(page.getByRole('link', { name: copy.title, exact: true })).toHaveCount(0);
      await page.goto(origin + '/endpoint-policies');
      await expect(page.getByRole('alert')).toContainText(copy.accessDenied);
      await expect(page.getByRole('tab', { name: copy.rulesTab, exact: true })).toHaveCount(0);
    } finally {
      await context.close();
    }
  }
});
