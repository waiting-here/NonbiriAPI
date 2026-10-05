import { randomUUID } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { expect, test, type Browser, type BrowserContext } from '@playwright/test';
import commonEn from '../../src/shared/i18n/common/en.json' with { type: 'json' };
import adminEn from '../../src/admin/i18n/en.json' with { type: 'json' };

type Cookie = { Name: string; Value: string };
interface Fixture {
  admin_url: string;
  user_url: string;
  admin_cookie: Cookie;
  users: { id: string; level: number; cookie: Cookie }[];
  request_ids: string[];
  json_body: string;
  management: { automatic_donation_id: string; pending_donation_id: string };
}
function fixture(): Fixture {
  return JSON.parse(readFileSync(process.env.NONBIRI_AUDIT_BROWSER_STATE!, 'utf8')) as Fixture;
}
test.skip(process.env.NONBIRI_MANAGEMENT_BROWSER !== '1', 'Requires the management fixture.');

async function session(browser: Browser, role: 'admin' | 1 | 6) {
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
  await context.addInitScript(() => {
    localStorage.setItem('nb.lang', 'en');
    localStorage.setItem('nb.theme', 'light');
  });
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
    await page
      .getByRole('button', { name: commonEn.common.blacklist.applySearch, exact: true })
      .click();
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

test('administrator applies log filters, exports them and downloads retained original error bytes', async ({
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
    await event.getByRole('button', { name: 'Load original body', exact: true }).click();
    await expect(event.locator('pre')).toHaveText(state.json_body);
    const rawDownload = page.waitForEvent('download');
    await event.getByRole('button', { name: 'Download original', exact: true }).click();
    const raw = await rawDownload;
    expect(raw.suggestedFilename()).toBe('upstream-error-1.bin');
    expect(readFileSync((await raw.path())!, 'utf8')).toBe(state.json_body);
  } finally {
    await admin.close();
  }
});
