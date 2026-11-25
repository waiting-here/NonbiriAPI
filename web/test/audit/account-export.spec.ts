import { randomUUID } from 'node:crypto';
import { readFile } from 'node:fs/promises';
import { expect, test } from '@playwright/test';

interface Fixture {
  user_url: string;
  users: { id: string; cookie: { Name: string; Value: string } }[];
  management: {
    account_export: { user_id: string; elevated_token: string };
    model_quotas: { donation_id: string; key_id: string; upstream_models: string[] };
  };
}

test.skip(
  process.env.NONBIRI_MANAGEMENT_BROWSER !== '1',
  'Requires the optional management fixture.',
);

test('account page downloads the current export with personal session-header settings and donated model usage', async ({
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
    const filename = 'nonbiriapi-account-export-v39.json';
    const [, download] = await Promise.all([
      page
        .waitForResponse(
          (response) =>
            response.request().method() === 'POST' &&
            new URL(response.url()).pathname === '/api/account/export',
        )
        .then((response) => {
          expect(response.status()).toBe(200);
          expect(response.headers()['content-disposition']).toBe(
            `attachment; filename="${filename}"`,
          );
        }),
      page.waitForEvent('download'),
      page.goto(state.user_url + '/account'),
    ]);
    expect(download.suggestedFilename()).toBe(filename);
    const exported = JSON.parse(await readFile((await download.path())!, 'utf8')) as {
      schema_version: number;
      donations: {
        id: string;
        keys: { id: string; model_limits: { upstream_model_id: string }[] }[];
      }[];
      user: { id: string };
      models: { id: string; session_headers: unknown }[];
    };
    expect(exported.schema_version).toBe(39);
    const quota = state.management.model_quotas;
    const donated = exported.donations.find((donation) => donation.id === quota.donation_id);
    const key = donated?.keys.find((key) => key.id === quota.key_id);
    expect(
      key?.model_limits.find((limit) => limit.upstream_model_id === quota.upstream_models[0]),
    ).toMatchObject({
      used: { credits: '1.125', calls: '2', tokens: '5' },
      reserved: { credits: '0', calls: '0' },
      historical_costs_partial: false,
    });
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
