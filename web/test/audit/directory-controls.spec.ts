import { randomUUID } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { expect, test, type Browser, type BrowserContext } from '@playwright/test';
import commonEn from '../../src/shared/i18n/common/en.json' with { type: 'json' };
import adminEn from '../../src/admin/i18n/en.json' with { type: 'json' };
import adminZh from '../../src/admin/i18n/zh.json' with { type: 'json' };

test('user browses current charity snapshots with search, pagination and committed changes', async ({
  browser,
}, info) => {
  const state = fixture();
  const admin = await session(browser, 'admin');
  const user = await session(browser, 1);
  try {
    let first: { id: string; revision: string } | undefined;
    for (let index = 1; index <= 21; index++) {
      const model = await write(admin, state.admin_url, '/admin/api/charity-models', {
        provider: 'snapshot-browser',
        model: String(index).padStart(2, '0'),
        enabled: true,
        is_mainstream: false,
        flatten_tool_calls: false,
        allowed_levels: [1, 2, 3, 4, 5, 6],
        pricing: { mode: 'per_request', user_price: '0', donor_reward: '0' },
        discount: { enabled: false, percent: 100, start_at: null, end_at: null },
      });
      first ??= model;
    }
    const page = await user.newPage();
    const loaded = page.waitForResponse((response) =>
      response.url().includes('/api/charity/models?view=catalog'),
    );
    await page.goto(state.user_url + '/charity');
    const response = await loaded;
    expect(response.ok()).toBe(true);
    expect(response.headers()['cache-control']).toContain('no-store');
    await page.getByRole('button', { name: 'Clear filters', exact: true }).click();
    const search = page.getByRole('searchbox', {
      name: 'Search model name or public description',
    });
    await search.fill('snapshot-browser');
    await search.press('Enter');
    await expect(page.getByText('21 items', { exact: true })).toBeVisible();
    await page.getByRole('combobox', { name: 'Items per page', exact: true }).selectOption('10');
    await expect(page.locator('.charity-model-name')).toHaveCount(10);
    const pager = page.getByRole('navigation', { name: 'Pagination', exact: true });
    await pager.getByRole('button', { name: 'Next', exact: true }).click();
    await expect(page.getByText('[公益]snapshot-browser/11', { exact: true })).toBeVisible();
    await pager.getByRole('button', { name: '3', exact: true }).click();
    await expect(page.locator('.charity-model-name')).toHaveCount(1);
    await pager.getByRole('button', { name: 'Previous', exact: true }).click();
    await expect(page.getByText('[公益]snapshot-browser/11', { exact: true })).toBeVisible();
    await page.goBack();
    await expect(page.getByText('[公益]snapshot-browser/21', { exact: true })).toBeVisible();
    const changed = await admin.request.patch(
      state.admin_url + '/admin/api/charity-models/' + first!.id,
      {
        headers: { Origin: state.admin_url, 'Idempotency-Key': randomUUID() },
        data: {
          expected_revision: first!.revision,
          public_description: 'Committed directory update',
        },
      },
    );
    expect(changed.ok()).toBe(true);
    await pager.getByRole('button', { name: '1', exact: true }).click();
    await page.reload();
    await expect(page.getByText('Committed directory update', { exact: true })).toBeVisible();
    const legacy = await user.request.get(state.user_url + '/api/charity/models');
    expect(legacy.ok()).toBe(true);
    expect(legacy.headers()['cache-control']).toContain('no-store');
    expect(Array.isArray((await legacy.json()).models)).toBe(true);
    await page.screenshot({
      path: process.env.NONBIRI_VISUAL_DIR
        ? resolve(process.env.NONBIRI_VISUAL_DIR, 'current-charity-directory.png')
        : info.outputPath('current-charity-directory.png'),
      fullPage: true,
    });
  } finally {
    await admin.close();
    await user.close();
  }
});

type Cookie = { Name: string; Value: string };
interface Fixture {
  admin_url: string;
  user_url: string;
  admin_cookie: Cookie;
  users: { id: string; level: number; cookie: Cookie }[];
  request_ids: string[];
  json_body: string;
  management: {
    automatic_donation_id: string;
    pending_donation_id: string;
    old_user_id: string;
    history_discord_id: string;
    request_ids: string[];
  };
}
function fixture(): Fixture {
  return JSON.parse(readFileSync(process.env.NONBIRI_AUDIT_BROWSER_STATE!, 'utf8')) as Fixture;
}
test.skip(process.env.NONBIRI_MANAGEMENT_BROWSER !== '1', 'Requires the management fixture.');

async function session(browser: Browser, role: 'admin' | 1 | 6, locale: 'en' | 'zh' = 'en') {
  const state = fixture();
  const origin = role === 'admin' ? state.admin_url : state.user_url;
  const cookie =
    role === 'admin' ? state.admin_cookie : state.users.find((user) => user.level === role)!.cookie;
  const context = await browser.newContext({ viewport: { width: 1280, height: 900 } });
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
  await context.addInitScript((locale) => {
    localStorage.setItem('nb.lang', locale);
    localStorage.setItem('nb.theme', 'light');
  }, locale);
  return context;
}
async function read(context: BrowserContext, origin: string, path: string) {
  const response = await context.request.get(origin + path, { headers: { Origin: origin } });
  expect(response.ok(), path + ': ' + response.status()).toBe(true);
  return response.json();
}
async function write(context: BrowserContext, origin: string, path: string, data: unknown) {
  const response = await context.request.post(origin + path, {
    headers: { Origin: origin, 'Idempotency-Key': randomUUID() },
    data,
  });
  expect(response.ok(), path + ': ' + response.status()).toBe(true);
  return response.status() === 204 ? null : response.json();
}

test('administrator submits endpoint search, resets it and pages actual shared members', async ({
  browser,
}) => {
  const state = fixture(),
    admin = await session(browser, 'admin'),
    owner = await session(browser, 1),
    steward = await session(browser, 6);
  const baseURL = 'https://directory.example.test/v1';
  try {
    for (const context of [owner, steward])
      await write(context, state.user_url, '/api/endpoints', {
        source: 'custom',
        connector_type: 'openai-compatible',
        base_url: baseURL,
        note: 'Directory shared endpoint',
        enabled: true,
      });
    const page = await admin.newPage();
    await page.goto(state.admin_url + '/endpoints');
    const search = page.getByRole('search');
    await search.getByLabel(adminEn.admin.endpoints.searchAria).fill('directory.example.test');
    await search.getByRole('button', { name: commonEn.common.applyFilter, exact: true }).click();
    await expect(page.locator('.ops-table tbody tr')).toHaveCount(1);
    expect(
      (
        await read(
          admin,
          state.admin_url,
          '/admin/api/overview/endpoints?q=directory.example.test&page=1&page_size=20',
        )
      ).data,
    ).toMatchObject([{ base_url: baseURL, user_count: '2' }]);
    const memberRead = page.waitForResponse(
      (response) =>
        new URL(response.url()).pathname === '/admin/api/overview/endpoints/users' &&
        new URL(response.url()).searchParams.get('page_size') === '20',
    );
    await page
      .getByRole('button', { name: adminEn.admin.endpoints.showUsers, exact: true })
      .click();
    const members = page.locator('.ops-table .ops-table');
    await expect(members.locator('tbody tr')).toHaveCount(2);
    const memberPager = page.getByRole('navigation', { name: 'Pagination', exact: true }).first();
    await expect(memberPager).toHaveText('2 items');
    await expect(memberPager.getByLabel(commonEn.common.pageControls.size)).toHaveCount(0);
    const result = await memberRead;
    expect(result.ok()).toBe(true);
    expect(
      (await result.json()).data.map((row: { user_id: string }) => row.user_id).sort(),
    ).toEqual(
      [
        state.users.find((user) => user.level === 1)!.id,
        state.users.find((user) => user.level === 6)!.id,
      ].sort(),
    );
    await search.getByRole('button', { name: commonEn.common.resetFilter, exact: true }).click();
    await expect(search.getByLabel(adminEn.admin.endpoints.searchAria)).toHaveValue('');
    await expect(members).toHaveCount(0);
    expect(new URL(page.url()).searchParams.has('q')).toBe(false);
    await expect(
      page.locator('.ops-table tbody').getByText(baseURL, { exact: true }),
    ).toBeVisible();
  } finally {
    await admin.close();
    await owner.close();
    await steward.close();
  }
});

test('administrator labels and filters non-commercial aggregation gateways in both languages', async ({
  browser,
}) => {
  const state = fixture();
  const owner = await session(browser, 1);
  const baseURL = 'https://gateway-classification.example.test/v1';
  try {
    await write(owner, state.user_url, '/api/endpoints', {
      source: 'custom',
      connector_type: 'openai-compatible',
      base_url: baseURL,
      note: 'Classification fixture',
      enabled: true,
    });
  } finally {
    await owner.close();
  }
  for (const locale of ['en', 'zh'] as const) {
    const context = await session(browser, 'admin', locale);
    const words = (locale === 'en' ? adminEn : adminZh).admin.endpoints;
    try {
      const page = await context.newPage();
      await page.goto(state.admin_url + '/endpoints?q=gateway-classification.example.test');
      await expect(page.locator('.ops-table tbody tr')).toHaveCount(1);
      const batch = page.locator('fieldset.endpoint-batch');
      await page
        .getByRole('checkbox', {
          name: words.selectEndpoint.replace('{{url}}', baseURL),
          exact: true,
        })
        .check();
      await batch.getByRole('combobox', { name: words.tags }).selectOption('noncommercial_gateway');
      const add = batch.getByRole('button', { name: words.addTag, exact: true });
      await add.focus();
      await page.keyboard.press('Enter');
      await expect(page.getByRole('status')).toContainText(words.tagsSaved);
      await page
        .getByRole('search')
        .getByRole('combobox', { name: words.tags })
        .selectOption('noncommercial_gateway');
      await expect(page.locator('.ops-table tbody tr')).toHaveCount(1);
      await expect(page.locator('.ops-table tbody')).toContainText(words.noncommercial_gateway);
      expect(new URL(page.url()).searchParams.get('tag')).toBe('noncommercial_gateway');
      if (process.env.NONBIRI_VISUAL_DIR) {
        for (const theme of ['light', 'dark'])
          for (const width of [1440, 390]) {
            await page.evaluate((theme) => {
              document.documentElement.dataset.theme = theme;
              document.documentElement.style.colorScheme = theme;
            }, theme);
            await page.setViewportSize({ width, height: 900 });
            await expect
              .poll(() =>
                page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
              )
              .toBe(true);
            await page.screenshot({
              path: resolve(
                process.env.NONBIRI_VISUAL_DIR,
                `endpoint-tag-${locale}-${theme}-${width}.png`,
              ),
              animations: 'disabled',
            });
          }
      }
      await page
        .getByRole('checkbox', {
          name: words.selectEndpoint.replace('{{url}}', baseURL),
          exact: true,
        })
        .check();
      await batch.getByRole('combobox', { name: words.tags }).selectOption('noncommercial_gateway');
      await batch.getByRole('button', { name: words.removeTag, exact: true }).click();
      await expect(page.locator('.ops-table tbody tr')).toHaveCount(0);
      const result = await read(
        context,
        state.admin_url,
        '/admin/api/overview/endpoints?q=gateway-classification.example.test&tag=untagged&page=1&page_size=20',
      );
      expect(result.data).toMatchObject([{ base_url: baseURL, tags: [] }]);
    } finally {
      await context.close();
    }
  }
});

test('administrator submits user, blacklist and announcement list filters', async ({ browser }) => {
  const state = fixture(),
    admin = await session(browser, 'admin');
  const discordID = '100000000000000999',
    title = 'Directory filter announcement';
  try {
    const member = await read(
      admin,
      state.admin_url,
      '/admin/api/users/' + state.users.find((user) => user.level === 6)!.id,
    );
    await write(admin, state.admin_url, '/admin/api/blacklist', {
      discord_id: discordID,
      reason: 'Synthetic directory filter',
    });
    const notice = await write(admin, state.admin_url, '/admin/api/announcements', {
      title_zh: '',
      body_zh: '',
      title_en: title,
      body_en: 'Synthetic list filter',
      severity: 'info',
      pinned: false,
      dismissible: true,
      expires_at: null,
    });
    const page = await admin.newPage();
    await page.goto(state.admin_url + '/users');
    await page.getByLabel(commonEn.management.users.searchAria).fill(member.username);
    await page.getByRole('button', { name: commonEn.common.applyFilter, exact: true }).click();
    await expect(
      page.locator('.nb-table tbody').getByText(member.username, { exact: true }),
    ).toBeVisible();
    const userQuery = new URL(page.url()).searchParams.get('q');
    expect(userQuery).toBe(member.username);
    expect(
      (
        await read(
          admin,
          state.admin_url,
          '/admin/api/users?q=' + encodeURIComponent(userQuery!) + '&page=1&page_size=20',
        )
      ).data.some((row: { id: string }) => row.id === member.id),
    ).toBe(true);
    await page.goto(state.admin_url + '/blacklist');
    await page.getByLabel(commonEn.common.blacklist.search).fill('Synthetic directory filter');
    await page.getByText(commonEn.management.users.exactFilters, { exact: true }).click();
    await page.getByLabel(commonEn.common.blacklist.actorKind).selectOption('admin');
    await page.getByRole('button', { name: commonEn.common.applyFilter, exact: true }).click();
    await expect(
      page.locator('.nb-table tbody').getByText(discordID, { exact: true }),
    ).toBeVisible();
    expect(
      (
        await read(
          admin,
          state.admin_url,
          '/admin/api/blacklist?q=Synthetic%20directory%20filter&actor_kind=admin&page=1&page_size=20',
        )
      ).data,
    ).toMatchObject([{ discord_id: discordID, first_actor_kind: 'admin' }]);
    await page.goto(state.admin_url + '/announcements');
    await page.getByLabel(commonEn.management.announcements.stateLabel).selectOption('draft');
    await expect(page.locator('.ops-table tbody').getByText(title, { exact: true })).toBeVisible();
    expect(
      (
        await read(
          admin,
          state.admin_url,
          '/admin/api/announcements?state=draft&page=1&page_size=20',
        )
      ).data.some((row: { id: string }) => row.id === notice.id),
    ).toBe(true);
    await page.getByLabel(commonEn.management.announcements.stateLabel).selectOption('published');
    await expect(page.locator('.ops-table tbody').getByText(title, { exact: true })).toHaveCount(0);
    expect(
      (await read(admin, state.admin_url, '/admin/api/announcements/' + notice.id)).state,
    ).toBe('draft');
  } finally {
    await admin.close();
  }
});

test('administrator filters donations and manages manual candidates', async ({ browser }) => {
  const state = fixture(),
    admin = await session(browser, 'admin');
  const copy = commonEn.common,
    donationID = state.management.automatic_donation_id;
  try {
    const donation = await read(admin, state.admin_url, '/admin/api/donations/' + donationID);
    const keyID = donation.keys[0].id,
      modelPath = '/admin/api/donations/' + donationID + '/keys/' + keyID + '/models';
    const page = await admin.newPage();
    await page.goto(state.admin_url + '/charity');
    await page.getByLabel(copy.donationHandling.search).fill(donation.description);
    await page
      .getByRole('button', { name: copy.donationHandling.applySearch, exact: true })
      .click();
    const filterDisclosure = page.locator('.charity-donations .nb-filter__more summary');
    if (await filterDisclosure.isVisible()) await filterDisclosure.click();
    await page.getByLabel(copy.donationHandling.filter).selectOption('pending');
    await expect(
      page.locator('.nb-table tbody').getByText(donation.description, { exact: true }),
    ).toBeVisible();
    expect(
      (
        await read(
          admin,
          state.admin_url,
          '/admin/api/donations?handling=pending&q=' +
            encodeURIComponent(donation.description) +
            '&page=1&page_size=20',
        )
      ).data.some((row: { id: string }) => row.id === donationID),
    ).toBe(true);
    await page.goto(state.admin_url + '/charity?donation_id=' + donationID);
    await page.getByRole('button', { name: copy.keyModels.title, exact: true }).click();
    const candidates = page
      .locator('section.ops-subcard')
      .filter({
        has: page.getByRole('heading', { name: copy.manualCandidates.title, exact: true }),
      })
      .last();
    await candidates.getByLabel(copy.manualCandidates.names).fill(`browser-binding-one
browser-binding-two
browser-manual-delete`);
    await candidates.getByRole('button', { name: copy.manualCandidates.add, exact: true }).click();
    await expect(
      candidates.getByRole('button', {
        name: copy.manualCandidates.remove.replace('{{name}}', 'browser-manual-delete'),
        exact: true,
      }),
    ).toBeVisible();
    expect(
      (await read(admin, state.admin_url, modelPath + '?page=1&page_size=20')).candidates.map(
        (row: { upstream_model_id: string }) => row.upstream_model_id,
      ),
    ).toEqual(
      expect.arrayContaining([
        'browser-binding-one',
        'browser-binding-two',
        'browser-manual-delete',
      ]),
    );
    await candidates.getByLabel(copy.manualCandidates.search).fill('browser-manual-delete');
    await candidates
      .getByRole('button', { name: copy.manualCandidates.searchAction, exact: true })
      .click();
    await expect(candidates.locator('li')).toHaveCount(1);
    const removing = page.waitForResponse(
      (response) =>
        response.request().method() === 'DELETE' &&
        new URL(response.url()).pathname.startsWith(modelPath + '/manual/'),
    );
    await candidates
      .getByRole('button', {
        name: copy.manualCandidates.remove.replace('{{name}}', 'browser-manual-delete'),
        exact: true,
      })
      .click();
    const removal = await removing;
    expect(removal.ok(), 'Manual removal status: ' + removal.status()).toBe(true);
    const catalog = await read(admin, state.admin_url, modelPath + '?page=1&page_size=20');
    expect(
      catalog.candidates.map((row: { upstream_model_id: string }) => row.upstream_model_id),
    ).not.toContain('browser-manual-delete');
    await expect(candidates.getByText(copy.manualCandidates.empty, { exact: true })).toBeVisible();
  } finally {
    await admin.close();
  }
});

test('administrator saves changed binding order, removes a connection and deletes its model', async ({
  browser,
}) => {
  const state = fixture(),
    admin = await session(browser, 'admin'),
    copy = commonEn.common;
  try {
    const donation = await read(
      admin,
      state.admin_url,
      '/admin/api/donations/' + state.management.automatic_donation_id,
    );
    const keyID = donation.keys[0].id;
    const modelsPath = '/admin/api/donations/' + donation.id + '/keys/' + keyID + '/models';
    const catalog = await read(admin, state.admin_url, modelsPath + '?page=1&page_size=20');
    await write(admin, state.admin_url, modelsPath + '/manual', {
      entries: ['browser-order-one', 'browser-order-two'],
      expected_manual_catalog_revision: catalog.manual_catalog_revision,
    });
    const page = await admin.newPage();
    const model = await write(admin, state.admin_url, '/admin/api/charity-models', {
      provider: 'browser',
      model: 'directory-order',
      enabled: true,
      is_mainstream: false,
      transport_rule: 'passthrough',
      flatten_tool_calls: false,
      pricing: { mode: 'per_request', user_price: '0', donor_reward: '0' },
      discount: { enabled: false, percent: 100, start_at: null, end_at: null },
    });
    const bindingsPath = '/admin/api/charity-models/' + model.id + '/bindings';
    const empty = await read(admin, state.admin_url, bindingsPath);
    const added = await write(admin, state.admin_url, bindingsPath + '/batch', {
      expected_binding_revision: empty.binding_revision,
      selections: ['browser-order-one', 'browser-order-two'].map((upstream_model_id) => ({
        donation_key_id: keyID,
        upstream_model_id,
      })),
    });
    await page.goto(state.admin_url + '/charity?charity_section=models&charity_model=' + model.id);
    const ordered = page.locator('section.card').filter({
      has: page.getByRole('heading', {
        name: copy.operations.charity.orderedBindings,
        exact: true,
      }),
    });
    const first = ordered.locator('tbody tr').first();
    await expect(first).toContainText('browser-order-one');
    await first
      .getByRole('button', { name: copy.operations.charity.moveDown, exact: true })
      .click();
    await ordered
      .getByRole('button', { name: copy.operations.charity.saveOrder, exact: true })
      .click();
    await expect(ordered.locator('tbody tr').first()).toContainText('browser-order-two');
    expect(
      (await read(admin, state.admin_url, bindingsPath)).bindings.map(
        (row: { id: string }) => row.id,
      ),
    ).toEqual(added.bindings.map((row: { id: string }) => row.id).reverse());
    await ordered
      .locator('tbody tr')
      .first()
      .getByRole('button', { name: copy.operations.charity.remove, exact: true })
      .click();
    await expect(ordered.locator('tbody tr')).toHaveCount(1);
    expect(
      (await read(admin, state.admin_url, bindingsPath)).bindings.map(
        (row: { upstream_model_id: string }) => row.upstream_model_id,
      ),
    ).toEqual(['browser-order-one']);
    await page
      .getByRole('button', { name: copy.operations.charity.deleteModel, exact: true })
      .click();
    const confirmation = page.getByRole('alertdialog');
    await expect(confirmation).toHaveCount(1);
    await confirmation
      .getByRole('button', { name: copy.operations.charity.deleteModelConfirm, exact: true })
      .click();
    await expect(confirmation).toHaveCount(0);
    const removed = await admin.request.get(
      state.admin_url + '/admin/api/charity-models/' + model.id,
    );
    expect(removed.status()).toBe(404);
  } finally {
    await admin.close();
  }
});

test('administrator filters retained log origins by exact Discord ID and account intersection', async ({
  browser,
}) => {
  const state = fixture(),
    admin = await session(browser, 'admin');
  try {
    const page = await admin.newPage();
    await page.goto(state.admin_url + '/logs');
    const rows = page.locator('.log-table tbody tr');
    await expect(rows.first()).toBeVisible();
    const filters = page.getByTestId('log-filters');
    const discord = filters.getByRole('textbox', {
      name: commonEn.logs.callerDiscordId,
      exact: true,
    });
    const account = filters.getByLabel(commonEn.common.userId, { exact: true });
    await expect(discord).toBeVisible();
    await expect(
      filters.getByRole('textbox', {
        name: commonEn.common.operations.logs.charityModelFilter,
        exact: true,
      }),
    ).toBeVisible();
    await expect(
      filters.getByRole('textbox', { name: commonEn.logs.upstreamModel, exact: true }),
    ).toBeHidden();

    const waitForLogs = (expected: Record<string, string>) =>
      page.waitForResponse((response) => {
        const url = new URL(response.url());
        return (
          response.request().method() === 'GET' &&
          url.origin === state.admin_url &&
          url.pathname === '/admin/api/logs' &&
          ['discord_id', 'user_id'].every(
            (key) => url.searchParams.get(key) === (expected[key] ?? null),
          )
        );
      });
    const apply = async (expected: Record<string, string>) => {
      const pending = waitForLogs(expected);
      await filters.getByRole('button', { name: commonEn.common.applyFilter, exact: true }).click();
      const response = await pending;
      expect(response.status()).toBe(200);
      expect(response.headers()['cache-control']).toContain('no-store');
      return response.json() as Promise<{
        data: {
          id: string;
          origin_discord_id: string;
          origin_user_id: string;
          origin_deleted: boolean;
        }[];
        pagination: { total_items: string };
      }>;
    };

    const discordID = state.management.history_discord_id;
    await discord.fill(discordID);
    const exact = await apply({ discord_id: discordID });
    expect(exact.data.map((row) => row.id).sort()).toEqual(
      [...state.management.request_ids].sort(),
    );
    expect(exact.data.every((row) => row.origin_discord_id === discordID)).toBe(true);
    expect(exact.data.some((row) => row.origin_deleted)).toBe(true);
    expect(exact.pagination.total_items).toBe(String(state.management.request_ids.length));
    await expect(rows).toHaveCount(state.management.request_ids.length);
    expect(new URL(page.url()).searchParams.get('discord_id')).toBe(discordID);
    await expect(discord).toHaveValue(discordID);

    await account.fill(state.management.old_user_id);
    const historical = await apply({
      discord_id: discordID,
      user_id: state.management.old_user_id,
    });
    expect(historical.data).toMatchObject([
      {
        id: state.management.request_ids[0],
        origin_user_id: state.management.old_user_id,
        origin_discord_id: discordID,
        origin_deleted: true,
      },
    ]);
    await expect(rows).toHaveCount(1);

    const unrelated = state.users.find((user) => user.level === 1)!.id;
    await account.fill(unrelated);
    const empty = await apply({ discord_id: discordID, user_id: unrelated });
    expect(empty.data).toEqual([]);
    expect(empty.pagination.total_items).toBe('0');
    await expect(rows).toHaveCount(0);
    await expect(
      page.getByRole('heading', { name: adminEn.admin.logs.noLogs, exact: true }),
    ).toBeVisible();

    const reset = waitForLogs({});
    await filters.getByRole('button', { name: commonEn.common.resetFilter, exact: true }).click();
    expect((await reset).status()).toBe(200);
    await expect(discord).toHaveValue('');
    await expect(account).toHaveValue('');
    await expect(rows.first()).toBeVisible();
    expect(new URL(page.url()).searchParams.has('discord_id')).toBe(false);
    expect(new URL(page.url()).searchParams.has('user_id')).toBe(false);
  } finally {
    await admin.close();
  }
});

test('administrator applies log filters, exports them and downloads retained diagnostic text', async ({
  browser,
}) => {
  const state = fixture(),
    admin = await session(browser, 'admin');
  try {
    const page = await admin.newPage();
    await page.goto(state.admin_url + '/logs');
    const filters = page.getByTestId('log-filters');
    await filters.locator('.nb-filter__more > summary').click();
    await filters.getByLabel(commonEn.common.status, { exact: true }).fill('503');
    await filters.getByRole('button', { name: commonEn.common.applyFilter, exact: true }).click();
    await expect(page).toHaveURL(/status=503/);
    const logs = await read(
      admin,
      state.admin_url,
      '/admin/api/logs?status=503&page=1&page_size=20',
    );
    expect(logs.data.length).toBeGreaterThan(0);
    expect(logs.data.every((row: { caller_status: number }) => row.caller_status === 503)).toBe(
      true,
    );
    await page.locator('.log-export > summary').click();
    const exportDownload = page.waitForEvent('download');
    await page
      .getByRole('link', {
        name: commonEn.common.operations.logs.presentation.exportCsv,
        exact: true,
      })
      .click();
    const csv = await exportDownload;
    expect(readFileSync((await csv.path())!, 'utf8')).toContain('caller_status');
    await filters.getByRole('button', { name: commonEn.common.resetFilter, exact: true }).click();
    await expect(filters.getByLabel(commonEn.common.status, { exact: true })).toHaveValue('');
    await page.goto(state.admin_url + '/logs?request_id=' + state.request_ids[0]);
    const detail = page.locator('.nb-expandable-panel:not([hidden])');
    await detail.locator('summary').filter({ hasText: 'Service call attempts' }).click();
    await detail.getByRole('button', { name: 'Upstream error details', exact: true }).click();
    const event = detail
      .locator('details')
      .filter({ has: page.locator(':scope > summary', { hasText: 'Error event 1' }) })
      .first();
    await event.locator('summary').first().click();
    await event.getByRole('button', { name: 'Load error body', exact: true }).click();
    await expect(event.locator('pre')).toHaveText(state.json_body);
    const rawDownload = page.waitForEvent('download');
    await event.getByRole('button', { name: 'Download error body', exact: true }).click();
    const raw = await rawDownload;
    expect(raw.suggestedFilename()).toBe('upstream-error-1.bin');
    expect(readFileSync((await raw.path())!, 'utf8')).toBe(state.json_body);
  } finally {
    await admin.close();
  }
});
