import { mkdir } from 'node:fs/promises';
import { join } from 'node:path';
import { createServer } from 'vite';
import { expect, test, type Page } from './test';
import { ADMIN_ORIGIN, USER_ORIGIN } from './ports';
import { mockPublicConfig, mockRoleSession } from './support';
import type { ProfileView } from '../../src/user/activities/lake-notes/api';

let rules: typeof import('../../src/user/activities/lake-notes/rules');
test.beforeAll(async () => {
  const server = await createServer({
    configFile: false,
    optimizeDeps: { noDiscovery: true, include: [] },
    server: { middlewareMode: true, watch: null },
  });
  try {
    rules = (await server.ssrLoadModule(
      '/src/user/activities/lake-notes/rules/index.ts',
    )) as typeof rules;
  } finally {
    await server.close();
  }
});

async function prepare(page: Page, station: 'admin' | 'user', width: number, theme: string) {
  await page.setViewportSize({ width, height: 900 });
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.addInitScript((theme) => {
    localStorage.setItem('nb.lang', 'en');
    localStorage.setItem('nb.theme', theme);
  }, theme);
  await mockPublicConfig(page, station);
  await mockRoleSession(page, station, station === 'admin' ? 'admin' : 'user');
}
async function picture(page: Page, name: string) {
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  if (process.env.NONBIRI_VISUAL_DIR) {
    await mkdir(process.env.NONBIRI_VISUAL_DIR, { recursive: true });
    await page.screenshot({ path: join(process.env.NONBIRI_VISUAL_DIR, name + '.png') });
  }
}
function deferred() {
  let release!: () => void;
  const promise = new Promise<void>((resolve) => {
    release = resolve;
  });
  return { promise, release };
}
const usage = {
  uncached_input_tokens: '0',
  cache_write_input_tokens: '0',
  cache_read_input_tokens: '0',
  output_tokens: '1',
  total_tokens: '1',
  usage_unknown: false,
  charge: '0',
};
function logRow(station: 'admin' | 'user', index: number) {
  return {
    id: 'req_' + String(index).padStart(21, '0') + 'Q',
    route_kind: 'openai_chat_completions',
    phase: 'handler',
    rejection_stage: null,
    rejection_reason: null,
    request_method: null,
    request_path: null,
    caller_result_class: 'success',
    caller_status: 200,
    caller_error_code: null,
    started_at: 1_800_000_000,
    completed_at: 1_800_000_001,
    usage,
    attempt_count: '0',
    ...(station === 'admin'
      ? { user_id: '7', caller_identity: null, usage_total_mismatch: false }
      : { model: 'previous-query-model' }),
  };
}

for (const width of [1440, 390])
  for (const theme of ['light', 'dark']) {
    for (const station of ['admin', 'user'] as const) {
      test(`${station} query loading, retained errors and reduced motion at ${width} ${theme}`, async ({
        page,
      }) => {
        await prepare(page, station, width, theme);
        await page.clock.install();
        const initial = deferred(),
          replacement = deferred();
        const origin = station === 'admin' ? ADMIN_ORIGIN : USER_ORIGIN;
        const path = station === 'admin' ? '/admin/api/logs' : '/api/logs';
        let reads = 0;
        await page.route(origin + path + '?**', async (route) => {
          reads++;
          const current = new URL(route.request().url()).searchParams.get('page') ?? '1';
          if (current === '1') {
            await initial.promise;
            await route.fulfill({
              json: {
                data: Array.from({ length: 20 }, (_, i) => logRow(station, i + 1)),
                next_cursor: null,
                pagination: { page: '1', page_size: 20, total_items: '21', total_pages: '2' },
              },
            });
          } else {
            await replacement.promise;
            if (reads > 2) {
              await route.fulfill({
                json: {
                  data: [logRow(station, 21)],
                  next_cursor: null,
                  pagination: { page: '2', page_size: 20, total_items: '21', total_pages: '2' },
                },
              });
              return;
            }
            await route.fulfill({
              status: 503,
              json: {
                error: {
                  code: 'service_unavailable',
                  message: 'The query did not finish. Try again.',
                },
              },
            });
          }
        });
        try {
          await page.goto(origin + '/logs?from=&to=');
          await expect(page.locator('.log-panel .nb-state[role="status"]')).toBeVisible();
          await page.clock.fastForward(8_100);
          await expect(page.locator('.log-panel .nb-state[role="status"]')).toContainText(
            'still running',
          );
          initial.release();
          await expect(page.locator('.log-panel .nb-table tbody tr')).toHaveCount(20);
          await page.getByRole('button', { name: 'Next', exact: true }).click();
          const feedback = page.locator('.log-panel .nb-query-feedback');
          await expect(feedback).toContainText('Updating results');
          await expect(page.locator('.log-panel > [aria-busy="true"]')).toBeVisible();
          await expect(page.getByRole('button', { name: 'Next', exact: true })).toBeDisabled();
          await expect(page.locator('.log-panel .nb-table tbody tr')).toHaveCount(20);
          await page.clock.fastForward(8_100);
          await expect(feedback).toContainText('still running');
          await expect(feedback.locator('.nb-query-feedback__indicator')).toHaveCSS(
            'animation-name',
            'none',
          );
          await feedback.scrollIntoViewIfNeeded();
          await picture(page, `logs-${station}-${width}-${theme}-pending`);
          replacement.release();
          await expect(feedback).toContainText('query failed');
          await expect(page.locator('.log-panel .nb-table tbody tr')).toHaveCount(20);
          await expect(page.locator('.log-panel [role="alert"]')).toBeVisible();
          await picture(page, `logs-${station}-${width}-${theme}-failed`);
          expect(reads).toBe(2);
          await page
            .locator('.log-panel [role="alert"]')
            .getByRole('button', { name: 'Retry', exact: true })
            .click();
          await expect(page.locator('.log-panel .nb-table tbody tr')).toHaveCount(1);
          await expect(feedback).toHaveCount(0);
          expect(reads).toBe(3);
        } finally {
          initial.release();
          replacement.release();
        }
      });
    }
    test(`lake exchange separates balances and allowance at ${width} ${theme}`, async ({
      page,
    }) => {
      await prepare(page, 'user', width, theme);
      const view: ProfileView = {
        readonly: false,
        revision: '1',
        rules_id: rules.RULES_ID,
        profile: { ...rules.initialProfile(), coins: '5' },
        wallet: { general_milli: '1000', game_milli: '0' },
        cast: null,
        settings: {
          enabled: true,
          revision: '1',
          exchanges: {
            coins_to_general: {
              enabled: true,
              source_amount: '10',
              target_amount: '1000',
              daily_source_limit: '100',
            },
            general_to_coins: { enabled: false, source_amount: '', target_amount: '' },
            coins_to_game: { enabled: false, source_amount: '', target_amount: '' },
            game_to_coins: { enabled: false, source_amount: '', target_amount: '' },
          },
        },
      };
      await page.route(USER_ORIGIN + '/api/games/lake-notes/**', async (route) => {
        const path = new URL(route.request().url()).pathname;
        if (path.endsWith('/profile')) return route.fulfill({ json: view });
        if (path.endsWith('/exchange/quote'))
          return route.fulfill({
            json: {
              direction: 'coins_to_general',
              quantity: route.request().postDataJSON().quantity,
              source_amount: '30',
              target_amount: '3000',
              source_lot: '10',
              target_lot: '1000',
              profile_revision: '1',
              settings_revision: '1',
              coins: '5',
              wallet: view.wallet,
              daily_source_remaining: '100',
              daily_reset_at: 1_800_000_000,
            },
          });
        if (path.endsWith('/exchange'))
          return route.fulfill({
            status: 403,
            json: {
              error: {
                code: 'insufficient_credits',
                message: 'There is not enough available balance.',
              },
            },
          });
        return route.fallback();
      });
      await page.goto(USER_ORIGIN + '/games/lake-notes');
      const exchange = page
        .locator('.card')
        .filter({ has: page.getByRole('heading', { name: 'Exchange coins and site credits' }) });
      await expect(exchange.locator('.lake-source-balance')).toHaveText(
        'Available balance: 5 Coins',
      );
      const quantity = exchange.getByRole('textbox', { name: 'Number of batches' });
      await quantity.fill('3');
      await exchange.getByRole('button', { name: 'Preview exchange' }).click();
      await expect(exchange.locator('.lake-daily-allowance')).toContainText(
        'Remaining daily exchange allowance: 100 Coins',
      );
      await exchange.getByRole('button', { name: 'Confirm exchange' }).click();
      await expect(exchange.getByRole('alert')).toContainText('not enough available balance');
      await expect(quantity).toHaveValue('3');
      await exchange.scrollIntoViewIfNeeded();
      await picture(page, `lake-exchange-${width}-${theme}`);
    });
  }

for (const width of [1440, 390])
  for (const theme of ['light', 'dark']) {
    test(`audit ledger keeps results after a delayed timeout at ${width} ${theme}`, async ({
      page,
    }) => {
      await prepare(page, 'admin', width, theme);
      await page.clock.install();
      const pending = deferred();
      const metrics = {
        issued: '1000',
        reclaimed: '0',
        user_income: '1000',
        user_expense: '0',
        internal_transfer: '0',
        operations: '1',
      };
      await page.route(ADMIN_ORIGIN + '/admin/api/economy-audit/**', async (route) => {
        const url = new URL(route.request().url());
        const metadata = {
          asset: url.searchParams.get('asset') ?? 'general',
          from: Number(url.searchParams.get('from')),
          to: Number(url.searchParams.get('to')),
          unit: 'milliunits',
          scale: '1000',
          offset_minutes: 480,
          ledger_seq: '1',
          projected_seq: '1',
          snapshot_at: 1_800_000_000,
          coverage: {
            status: 'complete',
            first_ledger_seq: '1',
            first_occurred_at: 1,
            unclassified_operations: '0',
            opening_known: true,
          },
        };
        if (url.pathname.endsWith('/summary'))
          return route.fulfill({
            json: {
              metadata,
              flows: metrics,
              inventory: {
                user_available: '1000',
                frozen: '0',
                pools: '0',
                platform: '0',
                negative_users: '0',
                negative_frozen: '0',
                negative_pools: '0',
                negative_platform: '0',
                net: '1000',
              },
              reconciliation: {
                status: 'matched',
                scope: 'retained_ledger',
                inventory_net: '1000',
                ledger_net: '1000',
                interval_opening_net: '0',
                interval_closing_net: '1000',
                interval_net_change: '1000',
              },
            },
          });
        if (url.pathname.endsWith('/series'))
          return route.fulfill({ json: { metadata, bucket: 'hour', data: [] } });
        if (url.pathname.endsWith('/operations')) {
          if (url.searchParams.has('cursor')) {
            await pending.promise;
            return route.fulfill({
              status: 503,
              json: {
                error: { code: 'service_unavailable', message: 'The query did not finish.' },
              },
            });
          }
          return route.fulfill({
            json: {
              metadata,
              anchor_seq: '1',
              next_cursor: 'next-page',
              data: [
                {
                  id: 'op_' + 'A'.repeat(22),
                  ledger_seq: '1',
                  kind: 'checkin_award',
                  source_type: 'operation',
                  source_id: 'previous-source',
                  created_at: 1_800_000_000,
                  classification: { channel: 'checkin', behavior: 'award', known: true },
                  entries: [
                    { asset: 'general', account_kind: 'user', user_id: null, delta: '1000' },
                  ],
                },
              ],
            },
          });
        }
        return route.fallback();
      });
      try {
        await page.goto(ADMIN_ORIGIN + '/economy-audit');
        await expect(page.getByRole('heading', { name: 'Current stock' })).toBeVisible();
        await page.getByRole('tab', { name: 'Ledger', exact: true }).click();
        const operation = page
          .locator('.economy-audit details')
          .filter({ hasText: 'previous-source' });
        await operation.locator('summary').first().click();
        await expect(page.getByText('previous-source', { exact: true })).toBeVisible();
        await page.getByRole('button', { name: 'Next', exact: true }).click();
        const feedback = page.locator('.economy-audit .nb-query-feedback');
        await expect(feedback).toContainText('Updating results');
        await expect(page.getByRole('button', { name: 'Next', exact: true })).toBeDisabled();
        await page.clock.fastForward(8_100);
        await expect(feedback).toContainText('still running');
        await expect(page.getByText('previous-source', { exact: true })).toBeVisible();
        await feedback.scrollIntoViewIfNeeded();
        await picture(page, `audit-ledger-${width}-${theme}-pending`);
        pending.release();
        await expect(
          page.getByRole('heading', { name: 'Accounting audit unavailable' }),
        ).toBeVisible();
        await expect(feedback).toContainText('query failed');
        await expect(page.getByText('previous-source', { exact: true })).toBeVisible();
        await expect(page.getByRole('button', { name: 'Next', exact: true })).toBeDisabled();
        await expect(page.getByRole('button', { name: 'Previous', exact: true })).toBeEnabled();
        await picture(page, `audit-ledger-${width}-${theme}-failed`);
      } finally {
        pending.release();
      }
    });
  }
