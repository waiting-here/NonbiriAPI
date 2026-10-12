import { resolve } from 'node:path';
import { expect, test, type Page } from './test';
import { collectConsoleViolations, mockPublicConfig, mockRoleSession } from './support';
import { USER_ORIGIN } from './ports';
import { loanActivity } from '../fixtures/loans';

const now = 1_800_000_000;
const pageOf = (data: unknown[]) => ({
  data,
  next_cursor: null,
  pagination: { page: '1', page_size: 20, total_items: String(data.length), total_pages: '1' },
});

async function fixtures(page: Page, design: string) {
  await mockRoleSession(page, 'user', 'level6');
  await mockPublicConfig(page, 'user');
  await page.route('**/api/**', async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path === '/api/appearance')
      return route.fulfill({
        json: {
          selected: design,
          moment: 'cover:h',
          moments: ['cover:permanent', 'cover:h', 'cover:i'],
          unlocked: ['default', 'celestial', 'wandering', 'heart-moment'],
          visible: ['default', 'celestial', 'wandering', 'heart-moment'],
          updated_at: now,
        },
      });
    if (path === '/api/events')
      return route.fulfill({ contentType: 'text/event-stream', body: ': connected\n\n' });
    if (path === '/api/charity/models') {
      const catalog = new URL(route.request().url()).searchParams.has('page');
      return route.fulfill({
        json: catalog
          ? {
              models: [],
              pagination: pageOf([]).pagination,
              donation_intake: 'open',
              server_now: now,
            }
          : { state: 'no_models', models: [], donation_intake: 'open', server_now: now },
      });
    }
    if (path === '/api/donations')
      return route.fulfill({
        json: pageOf(
          ['expired', 'approved', 'deleted'].map((status, i) => ({
            id: String(i + 1),
            status,
            revision: '1',
            description: '捐赠服务 ' + (i + 1),
            review_result:
              status === 'approved' ? { decision: 'approve', reason: '', reviewed_at: now } : null,
            created_at: now,
            updated_at: now,
            discord_public_thanks: false,
            key_count: '1',
            source_count: '1',
            sources: [
              {
                kind: 'custom',
                connector_type: 'openai-compatible',
                base_url: 'https://controlled.example/v1',
              },
            ],
            state_counts: {
              available: status === 'approved' ? '1' : '0',
              pending: '0',
              disabled: '0',
              suspended: '0',
              exhausted: '0',
              expired: status === 'expired' ? '1' : '0',
              ended: status === 'deleted' ? '1' : '0',
            },
          })),
        ),
      });
    if (path === '/api/caller-key')
      return route.fulfill({ json: null, headers: { 'X-Nonbiri-CallerKey-Generation': '0' } });
    if (['/api/models', '/api/endpoints', '/api/announcements'].includes(path))
      return route.fulfill({ json: pageOf([]) });
    if (path.startsWith('/api/checkin'))
      return route.fulfill({
        json: {
          enabled: true,
          mutually_exclusive: true,
          blocked_by_other_checkin: false,
          asset_type: path.endsWith('/game') ? 'game' : 'general',
          checked_in_today: false,
          balance: '1981776.258',
          award_min: '4500',
          award_max: '5500',
          balance_cap: '250000',
        },
      });
    if (path === '/api/home/game-summary')
      return route.fulfill({ json: { continue: [], pending_results: [] } });
    if (path === '/api/activities') return route.fulfill({ json: loanActivity() });
    if (path === '/api/limited-activities') return route.fulfill({ json: [] });
    if (path === '/api/rewards/assets') return route.fulfill({ json: [] });
    if (path === '/api/rewards/history')
      return route.fulfill({ json: { items: [], next_cursor: '' } });
    if (path === '/api/credits/history')
      return route.fulfill({
        json: {
          data: [
            {
              operation_id: 'op_' + 'A'.repeat(22),
              line: 1,
              asset_type: 'general',
              kind: 'checkin_award',
              delta: '4500',
              created_at: now,
              request_id: null,
            },
          ],
          page: '1',
          page_size: 20,
          total: '1',
          total_pages: '1',
          anchor: 'op_' + 'A'.repeat(22),
          current_balance: '1981776.258',
          game_balance: '10000',
          server_now: now,
        },
      });
    if (path === '/api/limited-activities/picture-book/wallet')
      return route.fulfill({
        json: { general: '1981776.258', sketch_paper: '42', sketch_brush: '8' },
      });
    if (path === '/api/steward/maintenance')
      return route.fulfill({ json: { enabled: false, revision: '1' } });
    if (path === '/api/logs')
      return route.fulfill({
        json: pageOf([
          {
            id: 'req_' + 'A'.repeat(22),
            route_kind: 'openai_chat_completions',
            phase: 'handler',
            rejection_stage: null,
            rejection_reason: null,
            request_method: null,
            request_path: null,
            caller_result_class: 'success',
            caller_status: 200,
            caller_error_code: null,
            started_at: now,
            completed_at: now + 1,
            model: 'Saved model',
            attempt_count: '1',
            usage: {
              uncached_input_tokens: '0',
              cache_write_input_tokens: '0',
              cache_read_input_tokens: '0',
              output_tokens: '1',
              total_tokens: '1',
              usage_unknown: false,
              charge: '0',
            },
          },
        ]),
      });
    return route.fallback();
  });
}

async function capture(page: Page, name: string) {
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  if (process.env.NONBIRI_VISUAL_DIR)
    await page.screenshot({ path: resolve(process.env.NONBIRI_VISUAL_DIR, name + '.png') });
}

async function readableText(page: Page, adaptive: boolean) {
  const minimum = await page.evaluate((adaptive) => {
    const root = document.documentElement;
    const original = root.style.getPropertyValue('--nb-moment-hue');
    const probe = document.createElement('span');
    document.body.append(probe);
    const canvas = document.createElement('canvas');
    canvas.width = canvas.height = 1;
    const context = canvas.getContext('2d', { willReadFrequently: true })!;
    const color = (variable: string) => {
      probe.style.color = 'var(' + variable + ')';
      context.clearRect(0, 0, 1, 1);
      context.fillStyle = getComputedStyle(probe).color;
      context.fillRect(0, 0, 1, 1);
      return [...context.getImageData(0, 0, 1, 1).data];
    };
    const luminance = (channels: number[]) =>
      channels
        .slice(0, 3)
        .map((n) => n / 255)
        .map((n) => (n <= 0.04045 ? n / 12.92 : ((n + 0.055) / 1.055) ** 2.4))
        .reduce((sum, n, i) => sum + n * [0.2126, 0.7152, 0.0722][i], 0);
    let minimum = Infinity;
    for (const hue of adaptive ? Array.from({ length: 12 }, (_, i) => i * 30) : [null]) {
      if (hue !== null) root.style.setProperty('--nb-moment-hue', hue + 'deg');
      for (const variable of ['--nb-appearance-glass-edge', '--nb-appearance-glass-core']) {
        const surface = color(variable);
        for (const artwork of [0, 255]) {
          const background = luminance(
            surface
              .slice(0, 3)
              .map((n) => (n * surface[3]) / 255 + artwork * (1 - surface[3] / 255)),
          );
          for (const token of [
            '--nb-color-text',
            '--nb-color-text-muted',
            '--nb-color-text-subtle',
            '--nb-color-accent',
          ]) {
            const foreground = luminance(color(token));
            minimum = Math.min(
              minimum,
              (Math.max(background, foreground) + 0.05) / (Math.min(background, foreground) + 0.05),
            );
          }
        }
      }
    }
    probe.remove();
    if (original) root.style.setProperty('--nb-moment-hue', original);
    else root.style.removeProperty('--nb-moment-hue');
    return minimum;
  }, adaptive);
  expect(minimum).toBeGreaterThanOrEqual(4.5);
}

for (const design of ['celestial', 'wandering', 'heart-moment'])
  for (const mode of ['light', 'dark'])
    for (const width of [1440, 390])
      test(
        'readable appearance surfaces ' + design + ' ' + mode + ' ' + width,
        async ({ page }) => {
          test.setTimeout(90_000);
          const guard = collectConsoleViolations(page);
          await page.setViewportSize({ width, height: 900 });
          await page.emulateMedia({ reducedMotion: 'reduce' });
          await page.addInitScript((mode) => {
            localStorage.setItem('nb.lang', 'zh');
            localStorage.setItem('nb.theme', mode);
          }, mode);
          await fixtures(page, design);
          const suffix = design + '-' + mode + '-' + width;
          await page.goto(USER_ORIGIN + '/charity?tab=donations');
          const list = page.locator('.economy-donation-list');
          await expect(list.getByText('捐赠服务 1', { exact: true })).toBeVisible();
          await expect(list).not.toHaveCSS('background-image', 'none');
          if (width === 1440) await readableText(page, design === 'heart-moment');
          await expect(page.locator('.economy-owner-filters')).not.toHaveCSS(
            'background-color',
            'rgba(0, 0, 0, 0)',
          );
          await expect(page.locator('.economy-donation-summary').first()).toHaveCSS('opacity', '1');
          await capture(page, 'donations-' + suffix);
          await list.locator('summary').first().click();
          await list.locator('summary').first().scrollIntoViewIfNeeded();
          await capture(page, 'donation-details-' + suffix);
          await list.locator('.nb-more > summary').first().click();
          await expect(page.getByRole('menu')).toBeVisible();
          await capture(page, 'donation-menu-' + suffix);
          await page.keyboard.press('Escape');

          for (const [path, selector, label] of [
            ['/', '.core-checkin-card', 'home'],
            ['/keys', '.nb-panel', 'keys'],
            ['/endpoints', '.nb-panel', 'services'],
            ['/models', '.nb-page-header', 'models'],
            ['/account', '.core-account-preferences', 'account'],
            ['/activities', '.loan-card', 'activities'],
            ['/rewards', '.reward-form', 'rewards'],
            ['/credits', '.credit-history__table tbody tr', 'credits'],
            ['/logs', '.nb-table-wrap', 'logs'],
            ['/steward?tab=maintenance', '.nb-panel--danger', 'steward'],
            ['/privacy', '.legal-layout__body', 'privacy'],
          ]) {
            await page.goto(USER_ORIGIN + path);
            await expect(page.locator(selector).first()).toBeVisible();
            if (path === '/') {
              await expect(page.locator('.core-checkin-card').first()).not.toHaveCSS(
                'background-image',
                'none',
              );
            }
            await capture(page, label + '-' + suffix);
          }
          await page.goto(USER_ORIGIN + '/account');
          if (design === 'heart-moment') {
            await expect
              .poll(() =>
                page.evaluate(() =>
                  document.documentElement.style.getPropertyValue('--nb-moment-hue'),
                ),
              )
              .not.toBe('');
          }
          const appearance = page.getByRole('button', { name: '外观', exact: true });
          if (width < 1024) await page.locator('button[aria-controls="user-navigation"]').click();
          await appearance.click();
          await expect(page.getByRole('alertdialog')).toBeVisible();
          await capture(page, 'appearance-dialog-' + suffix);
          guard.assertNone();
        },
      );
