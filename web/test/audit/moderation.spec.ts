import { readFileSync } from 'node:fs';
import { randomUUID } from 'node:crypto';
import { expect, test, type Browser, type BrowserContext } from '@playwright/test';
import adminEn from '../../src/admin/i18n/en.json' with { type: 'json' };
import commonEn from '../../src/shared/i18n/common/en.json' with { type: 'json' };
import commonZh from '../../src/shared/i18n/common/zh.json' with { type: 'json' };

type Cookie = { Name: string; Value: string };
interface Fixture {
  admin_url: string;
  user_url: string;
  admin_cookie: Cookie;
  users: { id: string; level: number; cookie: Cookie }[];
  management: { channel_id: string; disposable_user_id: string };
}
interface ReportCase {
  id: string;
  status: string;
  canonical_base_url: string;
  counts: { targets: string; deleted: string };
  decision: { action: string; reason: string } | null;
}
function fixture(): Fixture {
  return JSON.parse(readFileSync(process.env.NONBIRI_AUDIT_BROWSER_STATE!, 'utf8')) as Fixture;
}
test.skip(
  process.env.NONBIRI_MANAGEMENT_BROWSER !== '1',
  'Requires the optional management fixture.',
);
async function session(browser: Browser, administrator: boolean, narrow = false) {
  const state = fixture();
  const origin = administrator ? state.admin_url : state.user_url;
  const cookie = administrator ? state.admin_cookie : state.users[0].cookie;
  const context = await browser.newContext({
    viewport: narrow ? { width: 390, height: 844 } : { width: 1280, height: 900 },
  });
  await context.addCookies([
    {
      name: cookie.Name,
      value: cookie.Value,
      domain: new URL(origin).hostname,
      path: administrator ? '/admin' : '/api',
      httpOnly: true,
      sameSite: 'Lax',
    },
  ]);
  await context.addInitScript(
    (language) => {
      localStorage.setItem('nb.lang', language);
      localStorage.setItem('nb.theme', 'light');
    },
    narrow ? 'zh' : 'en',
  );
  return context;
}
async function read(context: BrowserContext, origin: string, path: string) {
  const response = await context.request.get(origin + path, { headers: { Origin: origin } });
  expect(response.ok(), path + ': ' + response.status()).toBe(true);
  return response.json();
}
async function create(context: BrowserContext, origin: string, path: string, data: unknown) {
  const response = await context.request.post(origin + path, {
    headers: { Origin: origin, 'Idempotency-Key': randomUUID() },
    data,
  });
  expect(response.ok(), path + ': ' + response.status()).toBe(true);
  return response.json();
}

test('administrator saves report timing and approves matching credential deletion through one dialog', async ({
  browser,
}, info) => {
  const state = fixture();
  const context = await session(browser, true);
  const owner = await session(browser, false);
  try {
    const page = await context.newPage();
    await page.goto(state.admin_url + '/settings');
    await page.getByRole('searchbox').fill('report_pending_ttl_seconds');
    const ttl = page.getByLabel('Pending report TTL', { exact: true });
    await ttl.fill('7200');
    const ttlField = page.locator('.nb-setting').filter({ has: ttl });
    await ttlField.locator('summary').click();
    await expect(ttlField).toContainText('2h');
    await page.getByRole('button', { name: adminEn.admin.settings.saveAll, exact: true }).click();
    await expect
      .poll(
        async () =>
          (await read(context, state.admin_url, '/admin/api/site-config')).values
            .report_pending_ttl_seconds,
      )
      .toBe(7200);
    await page.reload();
    await page.getByRole('searchbox').fill('report_pending_ttl_seconds');
    await expect(ttl).toHaveValue('7200');

    const endpoint = await create(owner, state.user_url, '/api/endpoints', {
      source: 'mainstream',
      channel_id: state.management.channel_id,
      note: 'Reported synthetic service',
      enabled: true,
    });
    const secret = 'synthetic-report-' + randomUUID();
    const key = await create(owner, state.user_url, '/api/endpoints/' + endpoint.id + '/keys', {
      secret,
      note: 'Reported synthetic credential',
      enabled: true,
      force_store_false: false,
      ownership_confirmed: true,
    });
    const baseURL = endpoint.base_url;
    await create(owner, state.user_url, '/api/reports/credential-theft', {
      connector_type: 'openai-compatible',
      base_url: baseURL,
      secret,
      note: 'Synthetic evidence for administrator review',
    });
    let report: ReportCase | undefined;
    await expect
      .poll(
        async () => {
          const reports = await read(context, state.admin_url, '/admin/api/reports');
          report = (reports.data as ReportCase[]).find(
            (entry) => entry.canonical_base_url === baseURL,
          );
          return report?.status;
        },
        { timeout: 40_000 },
      )
      .toBe('pending_review');
    const caseID = report!.id;
    expect(report!.counts.targets).toBe('1');
    await page.goto(state.admin_url + '/reports/' + caseID);
    const copy = adminEn.admin.reports.detail;
    const reason = 'Synthetic review approved for this matching credential';
    await page.getByRole('textbox', { name: copy.reason, exact: true }).fill(reason);
    await page.getByRole('button', { name: copy.approveDeletion, exact: true }).click();
    const dialog = page.getByRole('alertdialog', { name: copy.confirmApproveTitle, exact: true });
    await expect(page.getByRole('alertdialog')).toHaveCount(1);
    await expect(dialog).toContainText(copy.confirmApproveBody);
    await expect(dialog).toContainText(reason);
    await dialog.getByRole('button', { name: commonEn.common.cancel, exact: true }).click();
    expect((await read(context, state.admin_url, '/admin/api/reports/' + caseID)).status).toBe(
      'pending_review',
    );
    await expect(page.getByRole('textbox', { name: copy.reason, exact: true })).toHaveValue(reason);
    await page.getByRole('button', { name: copy.approveDeletion, exact: true }).click();
    await dialog.getByRole('button', { name: copy.approveDeletion, exact: true }).click();
    await expect
      .poll(
        async () => (await read(context, state.admin_url, '/admin/api/reports/' + caseID)).status,
        { timeout: 40_000 },
      )
      .toBe('approved');
    const persisted: ReportCase = await read(
      context,
      state.admin_url,
      '/admin/api/reports/' + caseID,
    );
    expect(persisted.decision).toMatchObject({ action: 'approve', reason });
    expect(persisted.counts.deleted).toBe('1');
    const keys = await read(owner, state.user_url, '/api/endpoints/' + endpoint.id + '/keys');
    expect(keys.data.some((entry: { id: string }) => entry.id === key.id)).toBe(false);
    await page.reload();
    await expect(page.getByText(reason, { exact: true })).toBeVisible();
    await page.screenshot({
      path: info.outputPath('report-approved-en-desktop.png'),
      fullPage: true,
    });
  } finally {
    await owner.close();
    await context.close();
  }
});

test('administrator deletes a disposable account after correcting a fresh password in the named dialog', async ({
  browser,
}, info) => {
  const state = fixture();
  const context = await session(browser, true, true);
  try {
    const id = state.management.disposable_user_id;
    const user = await read(context, state.admin_url, '/admin/api/users/' + id);
    const page = await context.newPage();
    const copy = commonZh.management.users;
    await page.goto(state.admin_url + '/users?user=' + id);
    await expect(page.getByRole('heading', { name: user.username, exact: true })).toBeVisible();
    await page.getByRole('button', { name: copy.delete, exact: true }).click();
    const dialog = page.getByRole('alertdialog', { name: copy.deleteTitle, exact: true });
    await expect(page.getByRole('alertdialog')).toHaveCount(1);
    await expect(dialog).toContainText(
      copy.deleteRefreshOnlyBody.replace('{{user}}', user.username),
    );
    await dialog.getByRole('button', { name: commonZh.common.cancel, exact: true }).click();
    expect((await read(context, state.admin_url, '/admin/api/users/' + id)).id).toBe(id);
    await page.getByRole('button', { name: copy.delete, exact: true }).click();
    const password = dialog.getByLabel(copy.elevatedPassword, { exact: true });
    await expect(dialog.locator('input')).toHaveCount(1);
    await password.fill('synthetic-wrong-password');
    const rejected = page.waitForResponse(
      (response) =>
        response.url().endsWith('/admin/api/auth/elevate') &&
        response.request().method() === 'POST',
    );
    await dialog.getByRole('button', { name: copy.deleteConfirm, exact: true }).click();
    expect((await rejected).status()).toBe(403);
    await expect(dialog.getByRole('alert')).toBeVisible();
    await expect(dialog).toContainText(user.username);
    await expect(password).toHaveValue('');
    expect((await read(context, state.admin_url, '/admin/api/users/' + id)).id).toBe(id);
    await password.fill('correct horse battery staple');
    const deleted = page.waitForResponse(
      (response) =>
        response.url().endsWith('/admin/api/users/' + id) &&
        response.request().method() === 'DELETE',
    );
    await dialog.getByRole('button', { name: copy.deleteConfirm, exact: true }).click();
    expect((await deleted).status()).toBe(204);
    await expect(dialog).toHaveCount(0);
    const absent = await context.request.get(state.admin_url + '/admin/api/users/' + id, {
      headers: { Origin: state.admin_url },
    });
    expect(absent.status()).toBe(404);
    const history = await read(
      context,
      state.admin_url,
      '/admin/api/users?account_state=deleted&user_id=' + id,
    );
    expect(history.data).toHaveLength(1);
    expect(history.data[0].deleted.former_user_id).toBe(id);
    await page.goto(state.admin_url + '/users?account_state=deleted&user_id=' + id);
    await expect(
      page.getByRole('cell', { name: copy.userId + ' ' + id, exact: true }),
    ).toBeVisible();
    await page.screenshot({
      path: info.outputPath('account-deleted-zh-narrow.png'),
      fullPage: true,
    });
  } finally {
    await context.close();
  }
});
