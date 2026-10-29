import { mkdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { randomUUID } from 'node:crypto';
import { expect, test, type BrowserContext } from '@playwright/test';
import en from '../../src/shared/i18n/common/en.json' with { type: 'json' };
import zh from '../../src/shared/i18n/common/zh.json' with { type: 'json' };

test.skip(process.env.NONBIRI_MANAGEMENT_BROWSER !== '1', 'Requires the management fixture.');
interface Fixture {
  user_url: string;
  admin_url: string;
  control_url: string;
  control_token: string;
  admin_cookie: { Name: string; Value: string };
  users: { level: number; cookie: { Name: string; Value: string } }[];
  management: { endpoint_id: string; discovery_key_id: string };
}
for (const [language, width, theme] of [
  ['en', 1440, 'light'],
  ['zh', 1440, 'dark'],
  ['zh', 390, 'light'],
  ['en', 390, 'dark'],
] as const) {
  test(`model discovery logs distinguish acceptance, failure and recovery (${language}, ${width}, ${theme})`, async ({
    browser,
  }, info) => {
    const state = JSON.parse(
      readFileSync(process.env.NONBIRI_AUDIT_BROWSER_STATE!, 'utf8'),
    ) as Fixture;
    const copy = (language === 'zh' ? zh : en).common.operations.logs;
    const failureCopy = (language === 'zh' ? zh : en).common.failure;
    const diagnosticLabel = (language === 'zh' ? zh : en).logs.diag;
    const visualDir = process.env.NONBIRI_VISUAL_DIR;
    if (visualDir) mkdirSync(visualDir, { recursive: true });
    const contexts: BrowserContext[] = [];
    const session = async (role: 'user' | 'steward' | 'admin') => {
      const origin = role === 'admin' ? state.admin_url : state.user_url;
      const cookie =
        role === 'admin'
          ? state.admin_cookie
          : role === 'user'
            ? state.users[0].cookie
            : state.users.find((u) => u.level === 5)!.cookie;
      const context = await browser.newContext({ viewport: { width, height: 900 } });
      contexts.push(context);
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
      await context.addInitScript(
        ({ language, theme }) => {
          localStorage.setItem('nb.lang', language);
          localStorage.setItem('nb.theme', theme);
        },
        { language, theme },
      );
      return context;
    };
    try {
      const owner = await session('user');
      const prefix = `/api/endpoints/${state.management.endpoint_id}/keys/${state.management.discovery_key_id}/models`;
      const read = async (path: string) => {
        const response = await owner.request.get(state.user_url + path, {
          headers: { Origin: state.user_url },
        });
        expect(response.status(), path).toBe(200);
        return response.json();
      };
      const logs = () => read('/api/logs?page=1&page_size=100');
      const refresh = async (mode: string) => {
        const previous = new Set((await logs()).data.map((row: { id: string }) => row.id));
        expect(
          (
            await owner.request.post(state.control_url + '/openai-discovery', {
              headers: { Authorization: 'Bearer ' + state.control_token },
              data: { mode },
            })
          ).ok(),
        ).toBe(true);
        expect(
          (
            await owner.request.post(state.user_url + prefix + '/refresh', {
              headers: { Origin: state.user_url, 'Idempotency-Key': randomUUID() },
            })
          ).status(),
        ).toBe(202);
        await expect
          .poll(async () => (await read(prefix + '?limit=20')).evidence.state)
          .toBe(mode === 'success' ? 'succeeded' : 'failed');
        let row: { id: string; caller_result_class: string; caller_status: number } | undefined;
        await expect
          .poll(async () => {
            row = (await logs()).data.find((item: { id: string }) => !previous.has(item.id));
            return row?.caller_status;
          })
          .toBe(202);
        expect(row?.caller_result_class).toBe('success');
        return row!.id;
      };
      const requestID = await refresh('disconnect');
      const result = await read('/api/logs/' + requestID + '?attempt_page=1&attempt_page_size=20');
      expect(result.attempts.data[0]).toMatchObject({
        result_kind: 'synthetic',
        diag: 'model discovery connection closed before a complete response was received',
      });
      for (const role of ['user', 'steward', 'admin'] as const) {
        const context = role === 'user' ? owner : await session(role);
        const page = await context.newPage();
        const origin = role === 'admin' ? state.admin_url : state.user_url;
        await page.goto(
          origin +
            (role === 'steward' ? '/steward?tab=logs&' : '/logs?') +
            'request_id=' +
            requestID,
        );
        const panel = page.locator('.nb-expandable-panel:not([hidden])');
        const accepted = panel.getByRole('status').filter({ hasText: copy.discoveryAccepted });
        await expect(accepted).toBeVisible();
        await expect(accepted).toContainText(copy.discoveryAcceptedHint);
        const attempts = panel
          .locator('details')
          .filter({ has: page.locator('summary', { hasText: copy.attempts }) });
        await expect(attempts).toHaveAttribute('open');
        await expect(
          attempts.getByText(copy.discoveryNoResponseHint, { exact: true }),
        ).toBeVisible();
        await expect(attempts.locator('.log-attempt-diagnostic')).toHaveText(
          result.attempts.data[0].diag,
        );
        await expect(attempts.locator('.log-attempt-diagnostic')).not.toBeVisible();
        await attempts.locator('summary').filter({ hasText: diagnosticLabel }).click();
        await expect(attempts.locator('.log-attempt-diagnostic')).toBeVisible();
        const failure = attempts.getByRole('region', { name: failureCopy.title, exact: true });
        await expect(failure).toBeVisible();
        await failure.locator('summary').filter({ hasText: failureCopy.technical }).click();
        await expect(
          failure.getByRole('heading', { name: failureCopy.causes, exact: true }),
        ).toBeVisible();
        await accepted.scrollIntoViewIfNeeded();
        const name = `discovery-log-${role}-${language}-${width}-${theme}`;
        await page.screenshot({
          path: visualDir
            ? join(visualDir, name + '-accepted.png')
            : info.outputPath(name + '-accepted.png'),
        });
        await attempts.scrollIntoViewIfNeeded();
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
          true,
        );
        await page.screenshot({
          path: visualDir
            ? join(visualDir, name + '-attempt.png')
            : info.outputPath(name + '-attempt.png'),
        });
        await page.close();
      }
      const invalidID = await refresh('invalid');
      const invalid = await read('/api/logs/' + invalidID + '?attempt_page=1&attempt_page_size=20');
      expect(invalid.attempts.data[0]).toMatchObject({
        result_kind: 'response',
        status_code: 200,
        diag: 'invalid upstream models response',
      });
      const page = await owner.newPage();
      await page.goto(state.user_url + '/logs?request_id=' + invalidID);
      await expect(page.locator('.log-attempt-diagnostic')).toHaveText(
        'invalid upstream models response',
      );
      await page.locator('summary').filter({ hasText: diagnosticLabel }).click();
      await expect(page.locator('.log-attempt-diagnostic')).toBeVisible();
      await expect(
        page
          .locator('.nb-expandable-panel')
          .getByRole('status')
          .filter({ hasText: copy.discoveryAccepted }),
      ).toBeVisible();
      const successID = await refresh('success');
      const catalog = await read(prefix + '?limit=20');
      expect(catalog.automatic_entries.length).toBeGreaterThan(0);
      const success = await read('/api/logs/' + successID + '?attempt_page=1&attempt_page_size=20');
      expect(success.attempts.data[0]).toMatchObject({
        result_kind: 'response',
        status_code: 200,
        diag: null,
      });
    } finally {
      for (const context of contexts) await context.close();
    }
  });
}
