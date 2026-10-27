import { mkdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { randomUUID } from 'node:crypto';
import { expect, test } from '@playwright/test';
import en from '../../src/user/i18n/en.json' with { type: 'json' };
import zh from '../../src/user/i18n/zh.json' with { type: 'json' };
import commonEn from '../../src/shared/i18n/common/en.json' with { type: 'json' };
import commonZh from '../../src/shared/i18n/common/zh.json' with { type: 'json' };

test.skip(process.env.NONBIRI_MANAGEMENT_BROWSER !== '1', 'Requires the management fixture.');
interface Fixture {
  user_url: string;
  control_url: string;
  control_token: string;
  users: { cookie: { Name: string; Value: string } }[];
  management: { gemini_endpoint_id: string; gemini_key_id: string };
}
test('Google discovery explains server-region failures and recovers in setup and key models', async ({
  browser,
}, info) => {
  const state = JSON.parse(
    readFileSync(process.env.NONBIRI_AUDIT_BROWSER_STATE!, 'utf8'),
  ) as Fixture;
  const visualDir = process.env.NONBIRI_VISUAL_DIR;
  if (visualDir) mkdirSync(visualDir, { recursive: true });
  const screenshotPath = (name: string) =>
    visualDir ? join(visualDir, name) : info.outputPath(name);
  const endpoint = state.management.gemini_endpoint_id,
    key = state.management.gemini_key_id;
  const prefix = `/api/endpoints/${endpoint}/keys/${key}/models`;
  for (const [language, width, theme] of [
    ['en', 1440, 'light'],
    ['zh', 1440, 'dark'],
    ['zh', 390, 'light'],
    ['en', 390, 'dark'],
  ] as const) {
    const context = await browser.newContext({ viewport: { width, height: 900 } });
    try {
      const cookie = state.users[0].cookie,
        copy = (language === 'zh' ? zh : en).user;
      await context.addCookies([
        {
          name: cookie.Name,
          value: cookie.Value,
          domain: new URL(state.user_url).hostname,
          path: '/api',
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
      const control = async (mode: string) =>
        expect(
          (
            await context.request.post(state.control_url + '/gemini-discovery', {
              headers: { Authorization: 'Bearer ' + state.control_token },
              data: { mode },
            })
          ).ok(),
        ).toBe(true);
      const catalog = async () => {
        const response = await context.request.get(state.user_url + prefix + '?limit=20', {
          headers: { Origin: state.user_url },
        });
        expect(response.ok()).toBe(true);
        const text = await response.text();
        for (const marker of [
          '203.0.113.77',
          'synthetic-private-discovery',
          'synthetic-image-credential',
        ])
          expect(text).not.toContain(marker);
        return JSON.parse(text);
      };
      await control('region');
      expect(
        (
          await context.request.post(state.user_url + prefix + '/refresh', {
            headers: { Origin: state.user_url, 'Idempotency-Key': randomUUID() },
          })
        ).status(),
      ).toBe(202);
      await expect.poll(async () => (await catalog()).evidence.safe_class).toBe('region');
      const page = await context.newPage();
      const c = (language === 'zh' ? commonZh : commonEn).common.connections;
      const keyResponse = await context.request.get(
        state.user_url + `/api/endpoints/${endpoint}/keys?page=1&page_size=20`,
        { headers: { Origin: state.user_url } },
      );
      expect(keyResponse.ok()).toBe(true);
      const keyRows = (await keyResponse.json()).data as {
        id: string;
        discovery_source_id: string | null;
      }[];
      const sourceId = keyRows.find((row) => row.id === key)?.discovery_source_id;
      expect(sourceId).toBeTruthy();
      await page.goto(state.user_url + '/endpoints?discovery_source_id=' + sourceId);
      const reason = copy.core['endpoints.discoveryReasonRegion'];
      const notice = page.locator('.nb-note--bad').filter({ hasText: reason });
      await expect(notice).toBeVisible();
      // Manual planning remains available after the independent directory fails.
      await page.locator('summary').filter({ hasText: c.manualModel }).click();
      await expect(page.getByLabel(c.upstreamModel, { exact: true })).toBeEnabled();
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
        true,
      );
      await notice.screenshot({
        path: screenshotPath(`discovery-setup-${language}-${width}-${theme}.png`),
      });
      await control('success');
      await page.getByRole('button', { name: c.refreshSource, exact: true }).press('Enter');
      await expect(notice).toHaveCount(0);
      await expect.poll(async () => (await catalog()).evidence.state).toBe('succeeded');
      expect((await catalog()).automatic_entries).toHaveLength(2);
      await control('region');
      await page.goto(state.user_url + '/endpoints/' + endpoint);
      await page
        .getByRole('button', { name: copy.core['endpoints.refreshDiscovery'], exact: true })
        .click();
      await expect(notice).toBeVisible();
      if (language === 'en' && width === 1440) {
        await page.getByRole('button', { name: copy.services.viewModels, exact: true }).click();
        const modelsPanel = page.locator('.nb-expandable-panel:not([hidden])');
        await modelsPanel
          .locator('summary')
          .filter({ hasText: copy.services.manualModels })
          .click();
        await modelsPanel
          .getByLabel(copy.core['endpoints.manualModel'], { exact: true })
          .fill('gemini-known-model');
        await modelsPanel
          .getByRole('button', { name: copy.core['endpoints.manualAdd'], exact: true })
          .click();
        await expect
          .poll(async () =>
            (await catalog()).manual_entries.some(
              (row: { upstream_model_id: string }) =>
                row.upstream_model_id === 'gemini-known-model',
            ),
          )
          .toBe(true);
        await modelsPanel
          .getByRole('button', { name: copy.core['common.close'], exact: true })
          .first()
          .click();
        await expect(notice).not.toBeVisible();
        await page.getByRole('button', { name: copy.services.viewModels, exact: true }).click();
        await expect(notice).toBeVisible();
      }
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
        true,
      );
      await notice.screenshot({
        path: screenshotPath(`discovery-key-${language}-${width}-${theme}.png`),
      });
      await page.locator('tr.core-key-card').screenshot({
        path: screenshotPath(`discovery-key-controls-${language}-${width}-${theme}.png`),
      });
    } finally {
      await context.close();
    }
  }
});
