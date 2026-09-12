import { mkdirSync } from 'node:fs';
import { join } from 'node:path';
import { expect, test } from './test';
import { ADMIN_ORIGIN, USER_ORIGIN } from './ports';
import { mockPublicConfig, mockRoleSession } from './support';
import type { FailureResetRef } from '../../src/shared/operations/failureReset';
import en from '../../src/shared/i18n/common/en.json' with { type: 'json' };
import zh from '../../src/shared/i18n/common/zh.json' with { type: 'json' };

for (const scenario of [
  { role: 'admin', language: 'zh', theme: 'light', width: 1280 },
  { role: 'steward', language: 'en', theme: 'dark', width: 390 },
] as const) {
  test(`failure reset selection and retry: ${scenario.role}`, async ({ page }) => {
    const station = scenario.role === 'admin' ? 'admin' : 'user';
    const origin = station === 'admin' ? ADMIN_ORIGIN : USER_ORIGIN;
    const api = station === 'admin' ? '/admin/api' : '/api/steward';
    const copy = (scenario.language === 'zh' ? zh : en).common.failureReset;
    await page.setViewportSize({ width: scenario.width, height: 900 });
    await page.addInitScript(({ language, theme }) => {
      localStorage.setItem('nb.lang', language);
      localStorage.setItem('nb.theme', theme);
    }, scenario);
    await mockPublicConfig(page, station);
    await mockRoleSession(page, station, station === 'admin' ? 'admin' : 'level5');
    const requests: { path: string; key: string | null; items?: FailureResetRef[] }[] = [];
    let failed = false;
    await page.route('**/*', async (route) => {
      const request = route.request();
      const url = new URL(request.url()),
        path = url.pathname;
      if (url.origin !== origin || !path.startsWith(api + '/donation')) {
        await route.fallback();
        return;
      }
      if (path === api + '/donations/badge') {
        await route.fulfill({ json: { pending_count: '0', server_now: 1_800_000_000 } });
        return;
      }
      if (path === api + '/donations') {
        await route.fulfill({
          json: {
            data: [
              {
                id: '1',
                status: 'approved',
                revision: '7',
                description: 'Shared resource',
                created_at: 1800000000,
                updated_at: 1800000000,
                review_result: { decision: 'approve', reason: 'Approved', reviewed_at: 1800000000 },
                reviewer: { user_id: '9', role: 'admin' },
                owner: { user_id: '7', discord_id: null, display_name: 'Member' },
                key_count: '101',
                source_count: '1',
                sources: [
                  {
                    kind: 'custom',
                    connector_type: 'openai-compatible',
                    base_url: 'https://example.test/v1',
                  },
                ],
                state_counts: {
                  available: '101',
                  pending: '0',
                  disabled: '0',
                  suspended: '0',
                  exhausted: '0',
                  expired: '0',
                  ended: '0',
                },
                handling: {
                  state: 'pending',
                  revision: '1',
                  processed_at: null,
                  processed_by_role: null,
                  closed_at: null,
                  closed_reason: null,
                },
              },
            ],
            next_cursor: null,
            pagination: { page: '1', page_size: 20, total_items: '1', total_pages: '1' },
          },
        });
        return;
      }
      const body = request.postDataJSON();
      requests.push({ path, key: request.headers()['idempotency-key'] ?? null, items: body.items });
      await new Promise((resolve) => setTimeout(resolve, 80));
      if (path.endsWith('/selection')) {
        const start = body.cursor ? 101 : 1;
        await route.fulfill({
          json: {
            items: Array.from({ length: start === 1 ? 100 : 1 }, (_, i) => ({
              donation_id: '1',
              key_id: String(start + i),
              expected_revision: '7',
            })),
            next_cursor: start === 1 ? 'next' : null,
          },
        });
        return;
      }
      if (!failed) {
        failed = true;
        await route.fulfill({
          status: 500,
          json: { error: { code: 'internal', message: 'Retry this request' } },
        });
        return;
      }
      const items: FailureResetRef[] = body.items;
      const revision = String(BigInt(items[0].expected_revision) + BigInt(items.length));
      await route.fulfill({
        json: {
          results: items.map((item) => ({
            donation_id: item.donation_id,
            key_id: item.key_id,
            status: 'reset',
            revision,
          })),
          counts: { processed: String(items.length), reset: String(items.length), skipped: '0' },
        },
      });
    });
    const errors: string[] = [];
    page.on('pageerror', (error) => errors.push(error.message));
    await page.goto(origin + (station === 'admin' ? '/charity' : '/steward?tab=charity'));
    const control = page.locator('.failure-reset-control').first();
    await control.locator('summary').click();
    await control.getByRole('button', { name: copy.all, exact: true }).click();
    await expect(control.getByRole('button', { name: copy.resume, exact: true })).toBeVisible();
    expect(requests.map((r) => r.path.endsWith('/selection'))).toEqual([true, true, false]);
    await control.getByRole('button', { name: copy.resume, exact: true }).click();
    await expect(control.getByRole('status')).toContainText(
      scenario.language === 'zh' ? '重置 101 项' : '101 reset',
    );
    expect(requests).toHaveLength(5);
    expect(requests[3]).toEqual(requests[2]);
    expect(requests[4].items?.[0].expected_revision).toBe('107');
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
      true,
    );
    if (process.env.NONBIRI_RESET_SCREENSHOTS) {
      mkdirSync(process.env.NONBIRI_RESET_SCREENSHOTS, { recursive: true });
      await page.evaluate(() => window.scrollTo(0, 0));
      await page.screenshot({
        path: join(process.env.NONBIRI_RESET_SCREENSHOTS, `reset-${scenario.role}.png`),
        fullPage: true,
      });
    }
    expect(errors).toEqual([]);
  });
}
