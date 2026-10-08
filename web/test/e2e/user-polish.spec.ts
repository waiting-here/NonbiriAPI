import { mkdir, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { expect, test } from './test';
import { USER_ORIGIN } from './ports';
import { collectConsoleViolations, mockPublicConfig, mockRoleSession } from './support';
import { modelFixture, taskFixture } from '../../src/shared/picturebook/fixtures';

test.afterEach(async ({ page }, info) => {
  if (info.status === info.expectedStatus || !process.env.NONBIRI_VISUAL_DIR) return;
  await writeFile(
    resolve(process.env.NONBIRI_VISUAL_DIR, 'user-polish-error.txt'),
    await page.locator('body').innerText(),
  );
});
for (const scenario of [
  { width: 1440, theme: 'light', locale: 'en' },
  { width: 1440, theme: 'dark', locale: 'zh' },
  { width: 390, theme: 'light', locale: 'en' },
  { width: 390, theme: 'dark', locale: 'zh' },
] as const) {
  test(
    'user notices, preferences and picture-book amounts ' + scenario.width + ' ' + scenario.theme,
    async ({ page }) => {
      const guard = collectConsoleViolations(page);
      const zh = scenario.locale === 'zh';
      await page.setViewportSize({ width: scenario.width, height: 900 });
      await page.emulateMedia({ reducedMotion: 'reduce' });
      await page.addInitScript(({ theme, locale }) => {
        localStorage.setItem('nb.theme', theme);
        localStorage.setItem('nb.lang', locale);
      }, scenario);
      await mockPublicConfig(page, 'user');
      await mockRoleSession(page, 'user', 'user');
      await page.route('**/api/events', (route) =>
        route.fulfill({ contentType: 'text/event-stream', body: ': connected\n\n' }),
      );
      await page.route('**/api/caller-key', (route) =>
        route.fulfill({ json: null, headers: { 'X-Nonbiri-CallerKey-Generation': '0' } }),
      );
      await page.route('**/api/models?**', (route) =>
        route.fulfill({
          json: {
            data: [],
            next_cursor: null,
            pagination: { page: '1', page_size: 10, total_items: '0', total_pages: '1' },
          },
        }),
      );
      await page.route('**/api/charity/models?**', (route) =>
        route.fulfill({
          json: {
            models: [],
            donation_intake: 'open',
            server_now: 1800000000,
            pagination: { page: '1', page_size: 10, total_items: '0', total_pages: '1' },
          },
        }),
      );
      await page.route('**/api/checkin**', (route) => {
        const game = new URL(route.request().url()).pathname.endsWith('/game');
        return route.fulfill({
          json: {
            enabled: true,
            mutually_exclusive: true,
            blocked_by_other_checkin: game,
            asset_type: game ? 'game' : 'general',
            checked_in_today: !game,
            balance: '8000',
            award_min: '0.125',
            award_max: '12000.001',
            balance_cap: '0',
          },
        });
      });
      await page.route('**/api/home/game-summary', (route) =>
        route.fulfill({ json: { continue: [], pending_results: [] } }),
      );
      const announcement = {
        epoch: 'b1e_' + 'A'.repeat(21) + 'Q',
        id: 'ann_' + 'A'.repeat(21) + 'Q',
        revision: '1',
        severity: 'info',
        pinned: false,
        dismissible: true,
        published_at: 1700000000,
        expires_at: null,
        effective_language: scenario.locale,
        fallback_from: null,
        title: zh ? '绘本活动与服务更新' : 'Picture-book and service update',
        excerpt: (zh
          ? '阅读完整公告，了解服务更新、图像生成和活动安排。'
          : 'Read the announcement for service updates, image generation and upcoming activities. '
        ).repeat(5),
      };
      await page.route('**/api/announcements?**', (route) =>
        route.fulfill({ json: { data: [announcement], next_cursor: null } }),
      );
      await page.route('**/api/announcements/ann_*', (route) => {
        const { excerpt, ...detail } = announcement;
        return route.fulfill({ json: { ...detail, rendered_body: '<p>' + excerpt + '</p>' } });
      });
      await page.route('**/api/issues?**', (route) =>
        route.fulfill({
          json: {
            data: [
              'auth',
              'rate_limit',
              'timeout',
              'protocol',
              'transport',
              'interrupted',
              'Future service reason',
            ].map((safe_detail, index) => ({
              id: 'iss_' + String(index).padEnd(22, 'A'),
              state: 'current',
              source: 'model_discovery',
              resource_kind: 'endpoint_key',
              summary_code: 'discovery_failed',
              safe_detail,
              deep_link: null,
              first_seen_at: 1800000000,
              last_seen_at: 1800000001,
              count: '1',
              closed_at: null,
            })),
            next_cursor: null,
            projection_incomplete: false,
            pagination: { page: '1', page_size: 20, total_items: '7', total_pages: '1' },
          },
        }),
      );
      const model = modelFixture();
      model.display_name = zh ? '绘本工作室' : 'Picture-book studio';
      model.price = { paper: '8000', brush: '1000' };
      model.description = zh
        ? '选择尺寸，画下今天的小故事。'
        : 'Choose a size and illustrate a small story.';
      model.parameters[0] = {
        ...model.parameters[0],
        max_length: 1500,
        length_unit: 'utf16_units',
      };
      model.parameters.push({
        key: 'size',
        supported: true,
        required: true,
        type: 'string',
        length_unit: 'utf8_bytes',
        default: '512x768',
      });
      model.size_capability = {
        mode: 'width_height',
        width: { minimum: 256, maximum: 1024, step: 256 },
        height: { minimum: 256, maximum: 1024, step: 256 },
        max_pixels: 786432,
        auto: true,
      };
      model.pricing = {
        default: { paper: '8000', brush: '1000' },
        fallback: 'unavailable',
        tiers: [{ tier: 'auto', paper: '6000', brush: '1000' }],
        sizes: [
          { width: 512, height: 768, paper: '8000', brush: '1000' },
          { width: 768, height: 512, paper: '7000', brush: '1000' },
        ],
      };
      const task = taskFixture({
        queue_position: 1001,
        charge: { paper: '32000', brush: '4000' },
        refund: { paper: '0', brush: '0' },
      });
      const refundTask = taskFixture({
        id: 'img_' + 'B'.repeat(21) + 'Q',
        status: 'cancelled',
        billing_state: 'refunded',
        charge: { paper: '0', brush: '0' },
        refund: { paper: '8000', brush: '1000' },
        queue_position: null,
        completed_at: 1700000010,
      });
      const prefix = '/api/limited-activities/picture-book';
      await page.route('**/api/limited-activities/picture-book**', (route) => {
        const path = new URL(route.request().url()).pathname;
        if (path === prefix)
          return route.fulfill({
            json: {
              key: 'picture-book',
              name: 'Picture book',
              cover_key: 'picture-book',
              visible: true,
              starts_at: null,
              ends_at: null,
              paused: false,
              revision: '1',
              status: 'open',
              module_config: {
                paper_price: '1',
                brush_price: '1',
                brush_cap: '100',
                brush_exchanged: '0',
                brush_remaining: '100',
              },
            },
          });
        if (path === prefix + '/wallet')
          return route.fulfill({
            json: { general: '1000000', sketch_paper: '1000000', sketch_brush: '1000000' },
          });
        if (path === prefix + '/models')
          return route.fulfill({ json: { data: [model], next_cursor: null } });
        if (path === prefix + '/queue')
          return route.fulfill({
            json: { queued: 0, running: 0, own: [], dispatch_paused: false },
          });
        if (path === prefix + '/tasks')
          return route.fulfill({ json: { data: [task, refundTask], next_cursor: null } });
        if (path === prefix + '/tasks/' + refundTask.id) return route.fulfill({ json: refundTask });
        if (path === prefix + '/tasks/' + task.id) return route.fulfill({ json: task });
        return route.fallback();
      });
      const evidence = process.env.NONBIRI_VISUAL_DIR;
      if (evidence) await mkdir(evidence, { recursive: true });
      for (const [name, path] of [
        ['home', '/'],
        ['report', '/report'],
        ['issues', '/issues'],
        ['picturebook', '/activities/picture-book'],
        ['account', '/account'],
      ] as const) {
        await page.goto(USER_ORIGIN + path);
        await expect(page.locator('main')).toBeVisible();
        if (name === 'home') await expect(page.locator('.home-announcement-preview')).toBeVisible();
        if (name === 'report') await expect(page.locator('textarea').first()).toBeVisible();
        if (name === 'issues') await expect(page.getByText('Future service reason')).toBeVisible();
        if (name === 'picturebook') {
          await expect(
            page.getByLabel(zh ? '精确尺寸' : 'Exact size', { exact: true }),
          ).toBeVisible();
          await page
            .getByRole('button', { name: zh ? '查看任务' : 'View task', exact: true })
            .first()
            .click();
          await expect(
            page.getByRole('heading', { name: zh ? '任务详情' : 'Task details' }),
          ).toBeVisible();
        }
        if (name === 'account') await expect(page.getByRole('radiogroup')).toHaveCount(5);
        if (name === 'home') {
          await expect(
            page.getByText(zh ? '今日已签' : 'Claimed today', { exact: true }),
          ).toBeVisible();
          await expect(
            page.getByText(zh ? '明天可领' : 'Available tomorrow', { exact: true }),
          ).toBeVisible();
          expect(
            await page
              .locator('.home-announcement-preview')
              .evaluate(
                (node) =>
                  node.getBoundingClientRect().height <=
                  Number.parseFloat(getComputedStyle(node).lineHeight) * 2 + 1,
              ),
          ).toBe(true);
        }
        if (name === 'report') await expect(page.locator('.nb-note--warn')).toBeVisible();
        if (name === 'issues') {
          await expect(
            page.getByText(zh ? '服务密钥验证失败' : 'The service rejected the key.'),
          ).toBeVisible();
          await expect(page.getByText('auth', { exact: true })).toHaveCount(0);
        }
        if (name === 'picturebook') {
          await expect(
            page.getByText(zh ? '长度上限: 1,500 字' : 'Length limit: 1,500 characters'),
          ).toBeVisible();
          await expect(
            page.locator('legend').filter({ hasText: zh ? /^尺寸$/ : /^Size$/ }),
          ).toHaveCount(0);
          await expect(
            page.locator('.picturebook-facts dd').filter({ hasText: '32,000' }),
          ).toBeVisible();
          await expect(page.getByText('1,001', { exact: true })).toBeVisible();
          await page
            .getByRole('button', { name: zh ? '查看任务' : 'View task', exact: true })
            .last()
            .click();
          const taskCard = page
            .locator('section.card')
            .filter({ has: page.getByRole('heading', { name: zh ? '任务详情' : 'Task details' }) });
          await expect(taskCard.locator('dd').filter({ hasText: '8,000' })).toBeVisible();
        }
        if (name === 'account') {
          for (const label of await page.locator('.nb-seg label').all()) {
            expect(await label.evaluate((node) => getComputedStyle(node).whiteSpace)).toBe(
              'nowrap',
            );
          }
        }
        expect(
          await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1),
        ).toBe(true);
        await page.evaluate(() => window.scrollTo(0, 0));
        if (evidence)
          await page.screenshot({
            path: resolve(
              evidence,
              name + '-' + scenario.width + '-' + scenario.theme + '-' + scenario.locale + '.png',
            ),
            fullPage: true,
          });
      }
      guard.assertNone();
    },
  );
}
