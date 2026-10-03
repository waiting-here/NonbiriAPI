import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { USER_ORIGIN } from './ports';
import { numberedPage } from './numbered-fixtures';
import { collectConsoleViolations, mockPublicConfig, mockRoleSession } from './support';
import { expect, test } from './test';

const original = JSON.parse(
  readFileSync(
    resolve(process.cwd(), '..', 'internal/resources/testdata/manual_update.json'),
    'utf8',
  ),
).affected_models[0].model;
for (const locale of ['en', 'zh'] as const) {
  for (const theme of ['light', 'dark'] as const) {
    test(
      'personal model roles and optional automation guide ' + locale + ' ' + theme,
      async ({ page }) => {
        const copy = JSON.parse(
          readFileSync(resolve(process.cwd(), 'src/user/i18n/' + locale + '.json'), 'utf8'),
        ).user;
        const roles = JSON.parse(
          readFileSync(
            resolve(process.cwd(), 'src/shared/i18n/common/' + locale + '.json'),
            'utf8',
          ),
        ).common.rolePolicy;
        const guard = collectConsoleViolations(page);
        await page.setViewportSize({ width: theme === 'dark' ? 320 : 1440, height: 900 });
        await page.addInitScript(
          ({ locale, theme }) => {
            localStorage.setItem('nb.lang', locale);
            localStorage.setItem('nb.theme', theme);
          },
          { locale, theme },
        );
        await mockPublicConfig(page, 'user');
        await mockRoleSession(page, 'user', 'level6');
        let model = { ...original, role_policy: { default_action: 'native', rules: {} } };
        const writes: Record<string, unknown>[] = [];
        await page.route(USER_ORIGIN + '/api/**', async (route) => {
          const request = route.request();
          const url = new URL(request.url());
          let body: unknown;
          if (url.pathname === '/api/caller-key') {
            await route.fulfill({ json: null, headers: { 'X-Nonbiri-CallerKey-Generation': '0' } });
            return;
          }
          if (url.pathname === '/api/models/' + original.id) {
            if (request.method() === 'PATCH') {
              const input = request.postDataJSON();
              expect(request.headers()['idempotency-key']).toBeTruthy();
              writes.push(input);
              const wire = { ...input };
              delete wire.expected_revision;
              model = { ...model, ...wire, revision: '3' };
            }
            body = model;
          } else if (url.pathname.endsWith('/bindings')) {
            body = { bindings: [], binding_revision: '1' };
          } else if (url.pathname.endsWith('/binding-candidates')) {
            body = numberedPage([], url.searchParams);
          } else if (url.pathname === '/api/models') {
            body = numberedPage([model], url.searchParams);
          } else if (url.pathname === '/api/endpoints') {
            body = numberedPage([], url.searchParams);
          } else {
            await route.fallback();
            return;
          }
          await route.fulfill({ json: body });
        });
        await page.goto(USER_ORIGIN + '/models?model_id=' + original.id);
        await page
          .getByRole('button', { name: copy.core['models.editModel'], exact: true })
          .click();
        await page.locator('summary').filter({ hasText: copy.models.advanced }).click();
        await page.locator('summary').filter({ hasText: roles.title }).click();
        const editor = page.getByRole('group', { name: roles.title });
        await editor.getByRole('combobox', { name: roles.defaultAction }).selectOption('reject');
        await editor.getByRole('button', { name: roles.addRule }).click();
        const roleName = editor.getByRole('textbox', { name: roles.roleName });
        await expect(roleName).toBeFocused();
        await roleName.fill('developer');
        await editor.getByRole('combobox', { name: roles.roleAction }).selectOption('system');
        await page.getByRole('button', { name: copy.core['common.save'], exact: true }).click();
        await expect.poll(() => writes.length).toBe(1);
        await expect(editor).toHaveCount(0);
        expect(writes).toHaveLength(1);
        expect(writes[0]).toMatchObject({
          expected_revision: original.revision,
          role_policy: { default_action: 'reject', rules: { developer: 'system' } },
        });
        await page.locator('summary').filter({ hasText: copy.models.advanced }).click();
        await expect(page.getByText('developer', { exact: true })).toBeVisible();
        await page.goto(USER_ORIGIN + '/keys');
        const guide = page.locator('.personal-automation-guide > .nb-fold');
        await expect(guide).not.toHaveAttribute('open');
        await guide.locator('summary').focus();
        await page.keyboard.press('Enter');
        await expect(guide).toHaveAttribute('open');
        await expect(
          guide.getByRole('heading', { name: copy.personalAutomation.resultTitle }),
        ).toBeVisible();
        await expect(
          guide.getByText('/api/automation/models/{id}/bindings', { exact: true }),
        ).toBeVisible();
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
          true,
        );
        guard.assertNone();
      },
    );
  }
}
