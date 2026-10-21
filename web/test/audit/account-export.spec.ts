import { randomUUID } from 'node:crypto';
import { readFile } from 'node:fs/promises';
import { expect, test } from '@playwright/test';

interface Fixture {
  user_url: string;
  users: { id: string; cookie: { Name: string; Value: string } }[];
  management: { account_export: { user_id: string; elevated_token: string } };
}

test.skip(
  process.env.NONBIRI_MANAGEMENT_BROWSER !== '1',
  'Requires the optional management fixture.',
);

test('account page downloads the current export with personal session-header settings', async ({
  browser,
}) => {
  const state = JSON.parse(
    await readFile(process.env.NONBIRI_AUDIT_BROWSER_STATE!, 'utf8'),
  ) as Fixture;
  const account = state.management.account_export;
  const user = state.users.find((candidate) => candidate.id === account.user_id)!;
  const context = await browser.newContext();
  try {
    await context.addCookies([
      {
        name: user.cookie.Name,
        value: user.cookie.Value,
        domain: new URL(state.user_url).hostname,
        path: '/api',
        httpOnly: true,
        sameSite: 'Lax',
      },
      {
        name: 'nb_elevated',
        value: account.elevated_token,
        domain: new URL(state.user_url).hostname,
        path: '/',
        httpOnly: false,
        sameSite: 'Lax',
      },
    ]);
    const sessionHeaders = [
      { name: 'x-message', idle_seconds: 0, format: 'opencode_message' },
      { name: 'x-session', idle_seconds: 300, format: 'uuid_v4' },
    ];
    const created = await context.request.post(state.user_url + '/api/models', {
      headers: { Origin: state.user_url, 'Idempotency-Key': randomUUID() },
      data: { provider: 'export', model: 'session-headers', session_headers: sessionHeaders },
    });
    expect(created.status()).toBe(201);
    const model = (await created.json()) as { id: string };
    await context.addInitScript((accountId) => {
      localStorage.setItem('nb.lang', 'en');
      sessionStorage.setItem('nb.pending.elevation', 'export');
      sessionStorage.setItem('nb.pending.elevation.account', accountId);
    }, account.user_id);
    const page = await context.newPage();
    const errors: string[] = [];
    page.on('pageerror', (error) => errors.push(error.message));
    const responsePending = page.waitForResponse(
      (response) =>
        response.request().method() === 'POST' &&
        new URL(response.url()).pathname === '/api/account/export',
    );
    const downloadPending = page.waitForEvent('download');
    await page.goto(state.user_url + '/account');
    const response = await responsePending;
    expect(response.status()).toBe(200);
    const filename = 'nonbiriapi-account-export-v27.json';
    expect(response.headers()['content-disposition']).toBe(`attachment; filename="${filename}"`);
    const download = await downloadPending;
    expect(download.suggestedFilename()).toBe(filename);
    const exported = JSON.parse(await readFile((await download.path())!, 'utf8')) as {
      schema_version: number;
      user: { id: string };
      models: { id: string; session_headers: unknown }[];
    };
    expect(exported.schema_version).toBe(27);
    expect(exported.user.id).toBe(account.user_id);
    expect(exported.models.find((entry) => entry.id === model.id)?.session_headers).toEqual(
      sessionHeaders,
    );
    expect(await page.evaluate(() => document.cookie)).not.toContain('nb_elevated');
    expect(await page.evaluate(() => sessionStorage.getItem('nb.pending.elevation'))).toBeNull();
    expect(
      await page.evaluate(() => sessionStorage.getItem('nb.pending.elevation.account')),
    ).toBeNull();
    expect(errors).toEqual([]);
  } finally {
    await context.close();
  }
});
