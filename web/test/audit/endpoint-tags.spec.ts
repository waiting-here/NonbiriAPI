import { readFileSync } from 'node:fs';
import { randomUUID } from 'node:crypto';
import { resolve } from 'node:path';
import {
  expect,
  test,
  type Browser,
  type BrowserContext,
  type Locator,
  type Page,
} from '@playwright/test';
import common from '../../src/shared/i18n/common/en.json' with { type: 'json' };

type Cookie = { Name: string; Value: string };
type Steward = 'denied_five' | 'granted_five' | 'six';
type Rule = { pattern: string; action: 'add' | 'remove'; tags: string[] };
type Endpoint = { base_url: string; tags: string[]; manual_tags: string[]; tag_rules: Rule[] };
interface Fixture {
  admin_url: string;
  user_url: string;
  admin_cookie: Cookie;
  management: {
    endpoint_tag_rules: {
      addresses: Record<'root' | 'child' | 'removed' | 'unmatched' | 'future', string>;
      endpoint_ids: Record<'root' | 'child' | 'removed' | 'unmatched', string>;
      pending_donation_id: string;
      pending_key_ids: string[];
      users: Record<Steward, { id: string; level: number; cookie: Cookie }>;
    };
  };
}
type Session = { context: BrowserContext; origin: string; prefix: string; path: string };
const broad: Rule = {
  pattern: '*.public.example.test',
  action: 'add',
  tags: ['mainstream'],
};
const narrow: Rule = {
  pattern: '*.private.public.example.test',
  action: 'remove',
  tags: [],
};
const crudPattern = '*.independent.example.test';
const rulesPath = '/overview/endpoints/tag-rules';
const endpointCopy = common.management.endpoints;
const connectionCopy = common.common.connections;
const labels = {
  rules: 'Wildcard tag rules',
  add: 'Add rule',
  edit: 'Edit rule',
  remove: 'Delete rule',
  save: 'Save rule',
  pattern: 'Domain pattern',
  clear: 'Clear tags',
  sources: 'Tag sources',
  mainstream: 'Mainstream channels and derivatives',
  mysterious: 'Mysterious channel',
  other: 'Other third party',
};

test.skip(
  process.env.NONBIRI_MANAGEMENT_BROWSER !== '1' ||
    process.env.NONBIRI_ENDPOINT_TAG_RULES_BROWSER !== '1',
  'Requires the isolated endpoint-tag-rules management fixture.',
);

function fixture(): Fixture {
  return JSON.parse(readFileSync(process.env.NONBIRI_AUDIT_BROWSER_STATE!, 'utf8')) as Fixture;
}
async function session(browser: Browser, role: 'admin' | Steward): Promise<Session> {
  const state = fixture();
  const origin = role === 'admin' ? state.admin_url : state.user_url;
  const cookie =
    role === 'admin' ? state.admin_cookie : state.management.endpoint_tag_rules.users[role].cookie;
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
  return {
    context,
    origin,
    prefix: role === 'admin' ? '/admin/api' : '/api/steward',
    path: role === 'admin' ? '/endpoints' : '/steward/endpoints',
  };
}
async function read<T>(actor: Session, path: string): Promise<T> {
  const response = await actor.context.request.get(actor.origin + actor.prefix + path, {
    headers: { Origin: actor.origin },
  });
  expect(response.ok(), path + ': ' + response.status()).toBe(true);
  return response.json() as Promise<T>;
}
async function mutate(
  actor: Session,
  method: 'PUT' | 'DELETE' | 'PATCH',
  path: string,
  data: unknown,
  status = 204,
) {
  const response = await actor.context.request.fetch(actor.origin + actor.prefix + path, {
    method,
    headers: { Origin: actor.origin, 'Idempotency-Key': randomUUID() },
    data,
  });
  expect(response.status(), method + ' ' + path).toBe(status);
  return response;
}
async function restoreSeeds(admin: Session) {
  await mutate(admin, 'PUT', rulesPath, broad);
  await mutate(admin, 'PUT', rulesPath, narrow);
}
async function rules(actor: Session) {
  return (await read<{ data: Rule[] }>(actor, rulesPath + '?page=1&page_size=100')).data;
}
async function deleteIfPresent(admin: Session, pattern: string) {
  if ((await rules(admin)).some((rule) => rule.pattern === pattern))
    await mutate(admin, 'DELETE', rulesPath, { pattern });
}
async function endpoint(actor: Session, url: string) {
  const response = await read<{ data: Endpoint[] }>(
    actor,
    '/overview/endpoints?' + new URLSearchParams({ q: url, page: '1', page_size: '100' }),
  );
  const row = response.data.find((item) => item.base_url === url);
  expect(row, 'Endpoint sample ' + url).toBeDefined();
  return row!;
}
async function openRules(page: Page, actor: Session) {
  await page.goto(actor.origin + actor.path + '?q=example.test&page_size=100');
  await page.getByText(labels.rules, { exact: true }).click();
  const panel = page.locator('.endpoint-rules');
  await expect(panel.getByRole('table', { name: labels.rules, exact: true })).toBeVisible();
  return panel;
}
function ruleRow(panel: Locator, pattern: string) {
  return panel.getByRole('row').filter({ hasText: pattern });
}
function endpointRow(page: Page, url: string) {
  return page
    .getByRole('table', { name: endpointCopy.title, exact: true })
    .getByRole('row')
    .filter({ has: page.getByText(url, { exact: true }) });
}
async function saveEditor(page: Page, actor: Session) {
  const response = page.waitForResponse(
    (value) =>
      value.request().method() === 'PUT' &&
      new URL(value.url()).pathname === actor.prefix + rulesPath,
  );
  await page
    .locator('.endpoint-rule-editor')
    .getByRole('button', { name: labels.save, exact: true })
    .click();
  expect((await response).status()).toBe(204);
  await expect(page.locator('.endpoint-rule-editor')).toHaveCount(0);
}
async function deleteRule(page: Page, actor: Session, panel: Locator, pattern: string) {
  const response = page.waitForResponse(
    (value) =>
      value.request().method() === 'DELETE' &&
      new URL(value.url()).pathname === actor.prefix + rulesPath,
  );
  await ruleRow(panel, pattern).getByRole('button', { name: labels.remove, exact: true }).click();
  expect((await response).status()).toBe(204);
  await expect(ruleRow(panel, pattern)).toHaveCount(0);
}
async function noOverflow(page: Page) {
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
}

test('rule sources explain effective tags and lock manual editing without approving old submissions', async ({
  browser,
}) => {
  const actor = await session(browser, 'admin');
  const data = fixture().management.endpoint_tag_rules;
  try {
    await restoreSeeds(actor);
    const page = await actor.context.newPage();
    const panel = await openRules(page, actor);
    await expect(ruleRow(panel, broad.pattern)).toContainText(labels.mainstream);
    await expect(ruleRow(panel, narrow.pattern)).toContainText(labels.clear);
    const root = endpointRow(page, data.addresses.root);
    const child = endpointRow(page, data.addresses.child);
    const removed = endpointRow(page, data.addresses.removed);
    for (const row of [root, child, removed]) {
      await expect(row.getByRole('checkbox')).toBeDisabled();
      await expect(row).toContainText('Managed by');
    }
    await expect(endpointRow(page, data.addresses.unmatched).getByRole('checkbox')).toBeEnabled();
    await root.getByText(labels.sources, { exact: true }).click();
    await expect(root).toContainText('Original manual tags: ' + labels.mysterious);
    await expect(root.locator('.endpoint-rule-sources')).toContainText(broad.pattern);
    await removed.getByText(labels.sources, { exact: true }).click();
    await expect(removed.locator('.endpoint-rule-sources')).toContainText(narrow.pattern);
    expect(await endpoint(actor, data.addresses.root)).toMatchObject({
      tags: ['mainstream', 'mysterious'],
      manual_tags: ['mysterious'],
      tag_rules: [broad],
    });
    expect(await endpoint(actor, data.addresses.child)).toMatchObject({
      tags: ['mainstream'],
      tag_rules: [broad],
    });
    expect(await endpoint(actor, data.addresses.removed)).toMatchObject({
      tags: [],
      tag_rules: [broad, narrow],
    });
    await mutate(
      actor,
      'PATCH',
      '/overview/endpoints/tags',
      { base_urls: [data.addresses.root], tag: 'other_third_party', add: true },
      409,
    );
    expect((await endpoint(actor, data.addresses.root)).manual_tags).toEqual(['mysterious']);
    expect(
      await read<{ status: string }>(actor, '/donations/' + data.pending_donation_id),
    ).toMatchObject({ status: 'pending' });
    await noOverflow(page);
  } finally {
    await actor.context.close();
  }
});

test('admin creates, edits and deletes a domain rule while original manual tags return', async ({
  browser,
}) => {
  const actor = await session(browser, 'admin');
  const url = fixture().management.endpoint_tag_rules.addresses.unmatched;
  try {
    await restoreSeeds(actor);
    await deleteIfPresent(actor, crudPattern);
    await mutate(actor, 'PATCH', '/overview/endpoints/tags', {
      base_urls: [url],
      tag: 'mysterious',
      add: true,
    });
    const page = await actor.context.newPage();
    const panel = await openRules(page, actor);
    await panel.getByRole('button', { name: labels.add, exact: true }).click();
    const editor = page.locator('.endpoint-rule-editor');
    await editor.getByRole('textbox', { name: labels.pattern, exact: true }).fill(crudPattern);
    await editor.getByRole('checkbox', { name: labels.other, exact: true }).check();
    await saveEditor(page, actor);
    await expect(ruleRow(panel, crudPattern)).toContainText(labels.other);
    expect((await endpoint(actor, url)).tags).toEqual(['mysterious', 'other_third_party']);
    await expect(endpointRow(page, url).getByRole('checkbox')).toBeDisabled();
    await ruleRow(panel, crudPattern)
      .getByRole('button', { name: labels.edit, exact: true })
      .click();
    await expect(
      editor.getByRole('textbox', { name: labels.pattern, exact: true }),
    ).toHaveAttribute('readonly', '');
    await editor.getByRole('radio', { name: labels.clear, exact: true }).check();
    await saveEditor(page, actor);
    await expect(ruleRow(panel, crudPattern)).toContainText(labels.clear);
    expect(await endpoint(actor, url)).toMatchObject({ tags: [], manual_tags: ['mysterious'] });
    await deleteRule(page, actor, panel, crudPattern);
    expect(await endpoint(actor, url)).toMatchObject({
      tags: ['mysterious'],
      manual_tags: ['mysterious'],
      tag_rules: [],
    });
    await expect(endpointRow(page, url).getByRole('checkbox')).toBeEnabled();
    await noOverflow(page);
  } finally {
    await deleteIfPresent(actor, crudPattern);
    await mutate(actor, 'PATCH', '/overview/endpoints/tags', {
      base_urls: [url],
      tag: 'mysterious',
      add: false,
    });
    await restoreSeeds(actor);
    await actor.context.close();
  }
});

for (const role of ['granted_five', 'six'] as const) {
  test(`${role} writes rules through the shared endpoint page`, async ({ browser }) => {
    const admin = await session(browser, 'admin');
    const actor = await session(browser, role);
    const pattern =
      role === 'six' ? '*.six.endpoint-rules.example.test' : '*.five.endpoint-rules.example.test';
    try {
      await restoreSeeds(admin);
      await deleteIfPresent(admin, pattern);
      const page = await actor.context.newPage();
      const panel = await openRules(page, actor);
      await panel.getByRole('button', { name: labels.add, exact: true }).click();
      const editor = page.locator('.endpoint-rule-editor');
      await editor.getByRole('textbox', { name: labels.pattern, exact: true }).fill(pattern);
      await editor.getByRole('checkbox', { name: labels.other, exact: true }).check();
      await saveEditor(page, actor);
      expect(await rules(actor)).toContainEqual({
        pattern,
        action: 'add',
        tags: ['other_third_party'],
      });
      await deleteRule(page, actor, panel, pattern);
      expect((await rules(actor)).some((rule) => rule.pattern === pattern)).toBe(false);
      await noOverflow(page);
    } finally {
      await deleteIfPresent(admin, pattern);
      await restoreSeeds(admin);
      await actor.context.close();
      await admin.context.close();
    }
  });
}

test('level five without tag permission reads rule sources but cannot bypass the read-only page', async ({
  browser,
}) => {
  const admin = await session(browser, 'admin');
  const actor = await session(browser, 'denied_five');
  const data = fixture().management.endpoint_tag_rules;
  try {
    await restoreSeeds(admin);
    const page = await actor.context.newPage();
    const panel = await openRules(page, actor);
    await expect(ruleRow(panel, broad.pattern)).toContainText(labels.mainstream);
    await expect(page.getByText(endpointCopy.readOnly, { exact: true })).toBeVisible();
    for (const name of [labels.add, labels.edit, labels.remove])
      await expect(panel.getByRole('button', { name, exact: true })).toHaveCount(0);
    await expect(endpointRow(page, data.addresses.root).getByRole('checkbox')).toHaveCount(0);
    await mutate(actor, 'PUT', rulesPath, { ...broad, tags: ['other_third_party'] }, 403);
    await mutate(actor, 'DELETE', rulesPath, { pattern: broad.pattern }, 403);
    await mutate(
      actor,
      'PATCH',
      '/overview/endpoints/tags',
      { base_urls: [data.addresses.unmatched], tag: 'other_third_party', add: true },
      403,
    );
    expect(await rules(admin)).toEqual(expect.arrayContaining([broad, narrow]));
    expect((await endpoint(admin, data.addresses.unmatched)).tags).toEqual([]);
  } finally {
    await actor.context.close();
    await admin.context.close();
  }
});

test('a separate pending donation needs manual approval and keeps its receipt outside both panes', async ({
  browser,
}) => {
  const actor = await session(browser, 'admin');
  const data = fixture().management.endpoint_tag_rules;
  const donationPath = '/donations/' + data.pending_donation_id;
  try {
    await restoreSeeds(actor);
    const before = await read<{ status: string; keys: { id: string }[] }>(actor, donationPath);
    expect(before.status).toBe('pending');
    expect(before.keys.map((key) => key.id).sort()).toEqual([...data.pending_key_ids].sort());
    const pending = await read<{
      data: {
        id: string;
        donation_key_id: string;
        revision: string;
        pending: { expected_review_revision: string } | null;
      }[];
    }>(actor, donationPath + '/uses?page=1&page_size=100');
    expect(pending.data.length).toBeGreaterThan(0);
    expect(pending.data.every((use) => use.pending !== null)).toBe(true);
    expect([...new Set(pending.data.map((use) => use.donation_key_id))].sort()).toEqual(
      [...data.pending_key_ids].sort(),
    );
    const page = await actor.context.newPage();
    const errors: string[] = [];
    page.on('pageerror', (error) => errors.push(error.message));
    await page.goto(
      actor.origin +
        '/charity?' +
        new URLSearchParams({
          charity_section: 'donations',
          q: 'Endpoint rules approval layout sample',
          donation_id: data.pending_donation_id,
        }),
    );
    const summary = page
      .locator('summary')
      .filter({
        has: page.getByText(connectionCopy.donationMaterials, { exact: true }),
      })
      .first();
    if ((await summary.locator('..').getAttribute('open')) === null) await summary.click();
    await page
      .getByLabel(connectionCopy.reviewReason, { exact: true })
      .fill('Synthetic endpoint-rule manual approval');
    const reviewed = page.waitForResponse(
      (response) =>
        response.request().method() === 'POST' &&
        new URL(response.url()).pathname === actor.prefix + donationPath + '/pending-review',
    );
    await page.getByRole('button', { name: connectionCopy.approveAllUses, exact: true }).click();
    const response = await reviewed;
    expect(response.status()).toBe(200);
    expect(response.request().postDataJSON().uses).toEqual(
      pending.data.map((use) => ({
        use_id: use.id,
        expected_revision: use.revision,
        expected_review_revision: use.pending!.expected_review_revision,
      })),
    );
    expect(await response.json()).toMatchObject({
      id: data.pending_donation_id,
      status: 'approved',
    });
    const container = page.locator('.charity-donations');
    const panes = container.locator(':scope > .charity-donations__panes');
    const receipt = container.locator(':scope > .nb-stack').filter({
      has: page.locator('ul[role="status"]').filter({ hasText: connectionCopy.reviewSaved }),
    });
    await expect(receipt).toBeVisible();
    await expect(receipt.locator('ul > li')).toHaveCount(data.pending_key_ids.length);
    for (const theme of ['light', 'dark']) {
      await page.evaluate((value) => {
        document.documentElement.dataset.theme = value;
      }, theme);
      for (const width of [1440, 900, 390]) {
        await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
        await receipt.scrollIntoViewIfNeeded();
        await expect(panes.locator(':scope > *')).toHaveCount(2);
        expect(
          await receipt.evaluate((node) => node.closest('.charity-donations__panes') === null),
        ).toBe(true);
        const receiptBox = await receipt.boundingBox();
        const panesBox = await panes.boundingBox();
        expect(Math.round(receiptBox!.y + receiptBox!.height)).toBeLessThanOrEqual(
          Math.round(panesBox!.y),
        );
        await noOverflow(page);
        await panes
          .getByRole('button', { name: common.common.operations.charity.returnToList, exact: true })
          .click();
        await expect(panes.locator(':scope > *')).toHaveCount(1);
        await expect(receipt).toBeVisible();
        await panes
          .getByRole('button', { name: common.common.operations.charity.openReview, exact: true })
          .click();
        await expect(panes.locator(':scope > *')).toHaveCount(2);
        await expect(
          page.getByRole('heading', { name: common.common.donationReview.title, exact: true }),
        ).toBeVisible();
        await expect(receipt).toBeVisible();
        await noOverflow(page);
        if (
          process.env.NONBIRI_VISUAL_DIR &&
          ((theme === 'light' && width === 1440) || (theme === 'dark' && width === 390))
        ) {
          await receipt.scrollIntoViewIfNeeded();
          await page.screenshot({
            path: resolve(
              process.env.NONBIRI_VISUAL_DIR,
              `endpoint-tag-review-${theme}-${width}.png`,
            ),
            animations: 'disabled',
          });
        }
      }
    }
    expect(
      await read<{ status: string; first_approval_origin: string }>(actor, donationPath),
    ).toMatchObject({
      status: 'approved',
      first_approval_origin: 'manual',
    });
    expect(errors).toEqual([]);
  } finally {
    await actor.context.close();
  }
});
