import { readFile } from 'node:fs/promises';
import { expect, test } from './test';
import { USER_ORIGIN } from './ports';
import {
  collectConsoleViolations,
  mockJson,
  mockPublicConfig,
  mockRoleSession,
  userSession,
} from './support';

for (const locale of ['en', 'zh'] as const) {
  test(`account export downloads v9 after the one-shot confirmation in ${locale}`, async ({
    page,
  }) => {
    const guard = collectConsoleViolations(page);
    await page.setViewportSize({ width: locale === 'zh' ? 390 : 1440, height: 900 });
    await mockRoleSession(page, 'user', 'user');
    await mockPublicConfig(page, 'user');
    await mockJson(page, {
      origin: USER_ORIGIN,
      method: 'GET',
      path: '/api/me',
      body: userSession('user'),
    });
    await page.addInitScript((language) => {
      localStorage.setItem('nb.lang', language);
      sessionStorage.setItem('nb.pending.elevation', 'export');
      sessionStorage.setItem('nb.pending.elevation.account', '1');
      document.cookie = 'nb_elevated=synthetic_export_capability; Path=/; SameSite=Lax';
    }, locale);
    const exportedDocument = {
      schema_version: 9,
      generated_at: 1_700_000_000,
      user: { id: '1' },
      endpoints: [],
      catalog_pairs: [],
      models: [],
      caller_key: null,
      usage: {},
      log_summary: {},
      issues: [],
      credit_ledger: [],
      checkins: [],
      game_onboarding: [],
      game_onboarding_holds: [],
      loans: [],
      game_rankings: { statistics_start: 1_700_000_000, totals: [], events: [] },
      penalties: [],
      welfare_claims: [],
      thursday: [],
      donations: [],
      charity: {},
      fishing: {},
      linklink: {},
      rps: {},
      bidding: {},
      likes: {},
      blackjack: {},
      randomness: [],
    };
    let requests = 0;
    await page.route(`${USER_ORIGIN}/api/account/export`, async (route) => {
      requests++;
      expect(route.request().method()).toBe('POST');
      expect(route.request().headers()['x-elevated-token']).toBe('synthetic_export_capability');
      await route.fulfill({
        headers: {
          'content-type': 'application/json',
          'content-disposition': 'attachment; filename="nonbiriapi-account-export-v9.json"',
          'cache-control': 'no-store',
        },
        body: JSON.stringify(exportedDocument),
      });
    });
    await page.goto(`${USER_ORIGIN}/account`);
    const dialog = page.getByRole('alertdialog');
    await expect(dialog).toBeVisible();
    expect(requests).toBe(0);
    const pending = page.waitForEvent('download');
    await dialog
      .getByRole('button', { name: locale === 'zh' ? '创建导出' : 'Create export', exact: true })
      .click();
    const download = await pending;
    expect(download.suggestedFilename()).toBe('nonbiriapi-account-export-v9.json');
    expect(JSON.parse(await readFile((await download.path())!, 'utf8'))).toEqual(exportedDocument);
    expect(requests).toBe(1);
    expect(await page.evaluate(() => document.cookie)).not.toContain('nb_elevated');
    expect(await page.evaluate(() => sessionStorage.getItem('nb.pending.elevation'))).toBeNull();
    guard.assertNone();
  });
}
