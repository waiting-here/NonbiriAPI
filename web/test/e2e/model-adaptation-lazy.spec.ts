import { mkdir, readFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { USER_ORIGIN } from './ports';
import { numberedPage } from './numbered-fixtures';
import { collectConsoleViolations, mockPublicConfig, mockRoleSession } from './support';
import { expect, test } from './test';

for (const width of [1440, 390]) {
  for (const theme of ['light', 'dark']) {
    test(`personal adaptation opens on demand at ${width}px ${theme}`, async ({ page }) => {
      const model = JSON.parse(
        await readFile(
          resolve(process.cwd(), '..', 'internal/resources/testdata/manual_update.json'),
          'utf8',
        ),
      ).affected_models[0].model;
      const guard = collectConsoleViolations(page);
      await page.setViewportSize({ width, height: 900 });
      await page.addInitScript((theme) => {
        localStorage.setItem('nb.lang', 'en');
        localStorage.setItem('nb.theme', theme);
      }, theme);
      await mockPublicConfig(page, 'user');
      await mockRoleSession(page, 'user', 'level6');
      let adaptationReads = 0;
      let bindingReads = 0;
      await page.route(USER_ORIGIN + '/api/**', async (route) => {
        const url = new URL(route.request().url());
        let body: unknown;
        if (url.pathname.endsWith('/request-adaptation')) {
          adaptationReads++;
          body = {
            revision: '1',
            forward_headers: { mode: 'replace', values: [] },
            fixed_headers: { mode: 'replace', values: {} },
            body_defaults: { mode: 'replace', values: {} },
            body_forced: { mode: 'replace', values: {} },
            native_extension_paths: { mode: 'replace', values: [] },
            excluded_request_fields: [],
          };
        } else if (url.pathname === '/api/models/' + model.id) body = model;
        else if (url.pathname.endsWith('/bindings')) {
          bindingReads++;
          body = {
            bindings: [
              {
                id: '1',
                endpoint_key_id: '1',
                endpoint_base_url: 'https://example.test',
                connector_type: 'ai-sdk-gateway-v3',
                endpoint_note: 'Gateway',
                endpoint_key_display_head: '',
                endpoint_key_display_tail: '',
                endpoint_key_note: '',
                upstream_model_id: 'test-model',
                ord: 0,
              },
            ],
            binding_revision: model.binding_revision,
          };
        } else if (
          url.pathname.endsWith('/binding-candidates') ||
          url.pathname === '/api/endpoints' ||
          url.pathname === '/api/connections'
        ) {
          body = numberedPage([], url.searchParams);
        } else if (url.pathname === '/api/models') body = numberedPage([model], url.searchParams);
        else {
          await route.fallback();
          return;
        }
        await route.fulfill({ json: body });
      });
      await page.goto(USER_ORIGIN + '/models?model_id=' + model.id);
      await expect(page.getByRole('button', { name: 'Edit model', exact: true })).toBeVisible();
      await expect.poll(() => bindingReads).toBe(1);
      expect(adaptationReads).toBe(0);
      const advanced = page.locator('summary').filter({ hasText: 'Request adaptation' });
      await advanced.focus();
      await page.keyboard.press('Enter');
      const cache = page.getByLabel('Automatic cache default');
      await expect(cache).toBeVisible();
      await cache.selectOption('1h');
      await advanced.click();
      await advanced.click();
      await expect(cache).toHaveValue('1h');
      expect(adaptationReads).toBe(1);
      expect(bindingReads).toBe(1);
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
        true,
      );
      if (process.env.NONBIRI_VISUAL_DIR) {
        const directory = resolve(process.env.NONBIRI_VISUAL_DIR);
        await mkdir(directory, { recursive: true });
        await cache.scrollIntoViewIfNeeded();
        await page.screenshot({
          path: resolve(directory, `model-adaptation-${width}-${theme}.png`),
          fullPage: true,
        });
      }
      guard.assertNone();
    });
  }
}
