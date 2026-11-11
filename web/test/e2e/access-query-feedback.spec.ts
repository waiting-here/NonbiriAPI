import { mkdir } from 'node:fs/promises';
import { join } from 'node:path';
import { expect, test, type Page } from './test';
import { ADMIN_ORIGIN, USER_ORIGIN } from './ports';
import { mockPublicConfig, mockRoleSession } from './support';

function deferred() {
  let release!: () => void;
  const promise = new Promise<void>((resolve) => {
    release = resolve;
  });
  return { promise, release };
}

async function picture(page: Page, name: string) {
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  if (process.env.NONBIRI_VISUAL_DIR) {
    await mkdir(process.env.NONBIRI_VISUAL_DIR, { recursive: true });
    await page.screenshot({
      path: join(process.env.NONBIRI_VISUAL_DIR, name + '.png'),
      fullPage: true,
    });
  }
}

for (const variant of [
  { role: 'admin', station: 'admin', width: 1440, theme: 'light' },
  { role: 'level6', station: 'user', width: 390, theme: 'dark' },
] as const) {
  test(`access summary and event queries show separate progress and recover for ${variant.role}`, async ({
    page,
  }) => {
    const errors: string[] = [];
    page.on('pageerror', (error) => errors.push(error.message));
    await page.setViewportSize({ width: variant.width, height: 900 });
    await page.emulateMedia({ reducedMotion: 'reduce' });
    await page.addInitScript((theme) => {
      localStorage.setItem('nb.lang', 'en');
      localStorage.setItem('nb.theme', theme);
    }, variant.theme);
    await mockPublicConfig(page, variant.station);
    await mockRoleSession(page, variant.station, variant.role);
    await page.clock.install();
    const summaryGate = deferred(),
      eventsGate = deferred(),
      retryGate = deferred();
    let summaryReads = 0,
      eventReads = 0;
    const summary = {
      authenticated_events: 1,
      anonymous_events: 0,
      model_list_events: 1,
      generation_requests: 2,
      model_generation_ratio: 0.5,
      coverage: { capture_started_at: 1799913600, dropped: 0, last_gap_at: null },
      paths: [],
    };
    const origin = variant.station === 'admin' ? ADMIN_ORIGIN : USER_ORIGIN;
    const api =
      variant.station === 'admin' ? '/admin/api/abuse-audit/' : '/api/steward/abuse-audit/';
    await page.route(origin + api + 'access-summary?**', async (route) => {
      summaryReads++;
      if (summaryReads === 2) {
        await summaryGate.promise;
        await route.fulfill({
          status: 503,
          json: { error: { code: 'service_unavailable', message: 'Access summary timed out' } },
        });
        return;
      }
      if (summaryReads === 3) await retryGate.promise;
      await route.fulfill({ json: summary });
    });
    await page.route(origin + api + 'access-events?**', async (route) => {
      eventReads++;
      if (eventReads === 2) await eventsGate.promise;
      await route.fulfill({
        json: {
          data: [
            {
              id: 'aev_AAAAAAAAAAAAAAAAAAAAAA',
              user_id: '6',
              caller_key_generation: '3',
              path_kind: 'models',
              method: 'GET',
              http_status: 200,
              response_kind: 'api_json',
              source: { effective_ip: '192.0.2.72', ip_quality: 'direct_peer' },
              occurred_at: 1799999999,
            },
          ],
          page: '1',
          page_size: 20,
          total_items: '1',
          total_pages: '1',
          watermark: '1',
          changed: false,
          from: 1799913600,
          to: 1800000000,
        },
      });
    });
    try {
      const path = variant.station === 'admin' ? '/abuse-audit?' : '/steward?tab=risk&';
      await page.goto(
        origin +
          path +
          'audit_tab=access&audit_access_from=1799913600&audit_access_to=1800000000&audit_access_watermark=1&audit_access_total=1',
      );
      const audit = page.locator('.audit-page');
      const row = audit.getByText('GET · models · 200 · api_json', { exact: true });
      await expect(row).toBeVisible();
      await expect(audit.getByText('No capture gaps recorded', { exact: true })).toBeVisible();
      // Reconnecting refreshes the same authorized selection while retaining
      // its results. Changing filters instead mounts an initial LoadingState.
      await page.context().setOffline(true);
      await page.context().setOffline(false);
      const feedback = audit.locator('.nb-query-feedback');
      await expect(feedback).toHaveCount(2);
      await expect(feedback.first()).toContainText('Updating results');
      await page.clock.fastForward(8_100);
      await expect(feedback.first()).toContainText('still running');
      await expect(feedback.last()).toContainText('still running');
      await expect(feedback.first().locator('.nb-query-feedback__indicator')).toHaveCSS(
        'animation-name',
        'none',
      );
      await picture(page, `access-${variant.role}-pending`);
      summaryGate.release();
      await expect(audit.getByRole('alert')).toBeVisible();
      await expect(row).toBeVisible();
      await expect(feedback).toHaveCount(1);
      eventsGate.release();
      await expect(feedback).toHaveCount(0);
      await picture(page, `access-${variant.role}-summary-failed`);
      await audit.getByRole('button', { name: 'Retry', exact: true }).click();
      await expect(audit.getByRole('alert')).toHaveCount(0);
      await expect(feedback).toHaveCount(1);
      await expect(feedback).toContainText('Updating results');
      retryGate.release();
      await expect(feedback).toHaveCount(0);
      await expect(audit.getByRole('alert')).toHaveCount(0);
      await expect(row).toBeVisible();
      await picture(page, `access-${variant.role}-recovered`);
      expect(summaryReads).toBe(3);
      expect(eventReads).toBe(2);
      expect(errors).toEqual([]);
    } finally {
      summaryGate.release();
      eventsGate.release();
      retryGate.release();
    }
  });
}
