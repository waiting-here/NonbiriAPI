import { expect, test } from './test';
import { ADMIN_ORIGIN, USER_ORIGIN } from './ports';
import { mockJson, mockPublicConfig, mockRoleSession } from './support';
import en from '../../src/user/i18n/en.json' with { type: 'json' };
import zh from '../../src/user/i18n/zh.json' with { type: 'json' };
import adminEn from '../../src/admin/i18n/en.json' with { type: 'json' };
import adminZh from '../../src/admin/i18n/zh.json' with { type: 'json' };

test('anonymous navigation loads only the selected legal page and language and recovers from a failed asset', async ({
  page,
}) => {
  await mockPublicConfig(page, 'user');
  await mockRoleSession(page, 'user', 'anonymous');
  await page.addInitScript(() => localStorage.setItem('nb.lang', 'en'));
  const requests: string[] = [];
  const failures = new Set(['privacy-en', 'privacy-zh']);
  await page.route('**/assets/station-copy-*.json', (route) => {
    const match = /station-copy-(privacy|terms)-(en|zh)-/.exec(route.request().url());
    if (!match) return route.fallback();
    const part = match[1] + '-' + match[2];
    requests.push(part);
    if (failures.delete(part))
      return route.fulfill({ status: 503, body: 'Temporarily unavailable' });
    return route.continue();
  });
  await page.goto(USER_ORIGIN + '/');
  const privacy = page.getByRole('link', { name: en.user.legal.privacy.nav, exact: true }).first();
  await expect(privacy).toBeVisible();
  expect(requests).toEqual([]);
  await privacy.click();
  await expect(page.getByRole('button', { name: 'Retry', exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Retry', exact: true }).click();
  await expect(
    page.getByRole('heading', { name: en.user.legal.privacy.title, exact: true }),
  ).toBeVisible();
  expect(requests).toEqual(['privacy-en', 'privacy-en']);
  await page.getByRole('button', { name: '中文', exact: true }).click();
  await expect.poll(() => requests.filter((item) => item === 'privacy-zh').length).toBe(1);
  await expect(
    page.getByRole('heading', { name: en.user.legal.privacy.title, exact: true }),
  ).toBeVisible();
  await expect(page.locator('html')).toHaveAttribute('lang', 'en');
  await page.getByRole('button', { name: '中文', exact: true }).click();
  await expect(
    page.getByRole('heading', { name: zh.user.legal.privacy.title, exact: true }),
  ).toBeVisible();
  await page.getByRole('link', { name: zh.user.legal.terms.nav, exact: true }).first().click();
  await expect(
    page.getByRole('heading', { name: zh.user.legal.terms.title, exact: true }),
  ).toBeVisible();
  expect(requests).toEqual(['privacy-en', 'privacy-en', 'privacy-zh', 'privacy-zh', 'terms-zh']);
});

test('administrator history defers the snapshot dictionary until retained targets are visible', async ({
  page,
}) => {
  await mockPublicConfig(page, 'admin');
  await mockRoleSession(page, 'admin', 'admin');
  await page.addInitScript(() => localStorage.setItem('nb.lang', 'en'));
  const requests: string[] = [];
  page.on('request', (r) => {
    const match = /station-copy-management-snapshot-(en|zh)-/.exec(r.url());
    if (match) requests.push(match[1]!);
  });
  const person = { id: '7', name: 'Example operator', role: 'admin' };
  const endpoint = '/admin/api/management-operations';
  const pageInfo = { total: '1', page: 1, page_size: 20 };
  await mockJson(page, {
    origin: ADMIN_ORIGIN,
    path: endpoint + '?view=groups&page=1&page_size=20',
    method: 'GET',
    body: {
      view: 'groups',
      groups: [],
      operations: [],
      ...pageInfo,
      total: '0',
      snapshot_max_id: '27',
    },
  });
  await mockJson(page, {
    origin: ADMIN_ORIGIN,
    path: endpoint + '/27',
    method: 'GET',
    body: {
      id: '27',
      event_id: 'event-27',
      created_at: 1700000000000,
      source: 'web',
      executor_kind: 'human',
      actor: person,
      authorizer: person,
      domain: 'users',
      action: 'update',
      outcome: 'succeeded',
      code: '',
      summary: 'Saved operation',
      targets: '1',
      succeeded: '1',
      skipped: '0',
      failed: '0',
    },
  });
  await mockJson(page, {
    origin: ADMIN_ORIGIN,
    path: endpoint + '/27/items?page=1&page_size=20',
    method: 'GET',
    body: {
      ...pageInfo,
      items: [
        {
          ordinal: '1',
          action: 'update',
          target_kind: 'user',
          target_id: '2',
          target_name: 'Saved target',
          outcome: 'succeeded',
          code: '',
          changes: [],
        },
      ],
    },
  });
  await page.goto(ADMIN_ORIGIN + '/management-operations');
  await expect(
    page.getByRole('heading', { name: adminEn.admin.managementHistory.title, exact: true }).first(),
  ).toBeVisible();
  expect(requests).toEqual([]);
  await page.goto(ADMIN_ORIGIN + '/management-operations?mh_detail=27');
  await expect(page.getByText(adminEn.admin.managementHistory.snapshot.legacy)).toBeVisible();
  expect(requests).toEqual(['en']);
  await page.getByRole('button', { name: /Account actions/ }).click();
  await page.getByRole('button', { name: '中文', exact: true }).click();
  await expect(page.getByText(adminZh.admin.managementHistory.snapshot.legacy)).toBeVisible();
  expect(requests).toEqual(['en', 'zh']);
});
