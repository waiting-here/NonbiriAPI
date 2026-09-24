import { expect, test } from './test';
import { ADMIN_ORIGIN } from './ports';
import { collectConsoleViolations, mockPublicConfig, mockRoleSession } from './support';

for (const locale of ['zh', 'en'] as const) {
  test(
    'image discovery explains configuration, failure and retry in ' + locale,
    async ({ page }) => {
      const errors = collectConsoleViolations(page),
        zh = locale === 'zh';
      await mockRoleSession(page, 'admin', 'admin');
      await mockPublicConfig(page, 'admin');
      await page.addInitScript((language) => {
        localStorage.setItem('nb.lang', language);
        localStorage.setItem('nb.theme', language === 'zh' ? 'dark' : 'light');
      }, locale);
      const adapter = {
        discovery: { method: 'GET', path: '/v1/models', items_pointer: '/data', id_pointer: '/id' },
        submit: {
          method: 'POST',
          path: '/v1/images/generations',
          mapping: { model_pointer: '/model', parameters: { prompt: '/prompt' }, constants: [] },
        },
        response: {
          images_pointer: '/data',
          base64_pointer: '/b64_json',
          working_states: [],
          success_states: [],
          failure_states: [],
        },
      };
      const upstream = {
        revision: '1',
        configured: true,
        base_url: 'https://images.example.invalid',
        secret_set: true,
        rpm: 30,
        concurrency: 2,
        per_user_limit: 1,
        global_limit: 100,
        queue_timeout_seconds: 1800,
        execution_timeout_seconds: 1800,
        memory_budget_mib: 512,
        image_origins: [],
        adapter,
        control: null,
      };
      const keys: string[] = [];
      await page.route('**/admin/api/limited-activities/**', async (route) => {
        const path = new URL(route.request().url()).pathname;
        if (path.endsWith('/upstream')) return route.fulfill({ json: upstream });
        if (path.endsWith('/models/refresh')) {
          keys.push(route.request().headers()['idempotency-key']);
          const operation = {
            id: keys.length === 1 ? 'op_AAAAAAAAAAAAAAAAAAAAAA' : 'op_BBBBBBBBBBBBBBBBBBBBBQ',
            state: 'queued',
            created_at: 1800000000,
            completed_at: null,
            model_count: 0,
            error_code: null,
          };
          return route.fulfill({ json: { operation } });
        }
        if (path.includes('/models/refresh/'))
          return route.fulfill({
            json: {
              id: path.split('/').at(-1),
              state: keys.length === 1 ? 'failed' : 'succeeded',
              created_at: 1800000000,
              completed_at: 1800000001,
              model_count: 0,
              error_code: keys.length === 1 ? 'upstream_failed' : null,
              ...(keys.length === 1 ? { http_status: 401 } : {}),
            },
          });
        if (path.endsWith('/models') || path.endsWith('/controls'))
          return route.fulfill({ json: { data: [], next_cursor: null } });
        return route.fulfill({
          json: {
            key: 'picture-book',
            name: 'Picture book',
            visible: false,
            starts_at: null,
            ends_at: null,
            paused: false,
            revision: '1',
            status: 'unconfigured',
            module_config: {
              paper_price: '1',
              brush_price: '1',
              brush_cap: '100',
              brush_exchanged: '0',
              brush_remaining: '100',
            },
          },
        });
      });
      await page.goto(ADMIN_ORIGIN + '/limited-activities');
      await expect(
        page.getByText(zh ? /返回 base64 图片时可留空/ : /leave blank for base64 images/),
      ).toBeVisible();
      await page
        .getByText(zh ? '查看填写示例与字段说明' : 'Examples and field guide', { exact: true })
        .click();
      await expect(
        page.getByRole('button', {
          name: zh ? '用 OpenAI 兼容示例替换配置' : 'Use the OpenAI-compatible example',
        }),
      ).toBeVisible();
      await page
        .getByRole('button', { name: zh ? '拉取模型目录' : 'Refresh model catalog', exact: true })
        .click();
      const failure = page.locator('.picturebook-discovery-error');
      await expect(failure).toContainText('HTTP 401');
      await expect(failure).toContainText(zh ? '活动专用密钥' : 'activity key');
      for (const width of [1440, 390]) {
        await page.setViewportSize({ width, height: 1000 });
        await expect
          .poll(() => page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1))
          .toBe(true);
        await failure.screenshot({
          path: '../tmp/image-discovery-' + locale + '-' + width + '.png',
        });
      }
      await page
        .getByRole('button', { name: zh ? '重新拉取模型目录' : 'Start a new model refresh' })
        .click();
      await expect(
        page.getByText(zh ? /服务返回了空模型目录/ : /service returned an empty catalog/),
      ).toBeVisible();
      await expect(failure).toHaveCount(0);
      expect(keys).toHaveLength(2);
      expect(keys[0]).not.toBe(keys[1]);
      errors.assertNone();
    },
  );
}
