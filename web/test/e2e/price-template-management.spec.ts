import { mkdir } from 'node:fs/promises';
import { resolve } from 'node:path';
import { numberedPage } from './numbered-fixtures';
import { ADMIN_ORIGIN } from './ports';
import { mockPublicConfig, mockRoleSession } from './support';
import { expect, test } from './test';

for (const width of [1440, 390])
  for (const theme of ['light', 'dark']) {
    test(`independent templates retain model drafts, list position and failed edits at ${width}px ${theme}`, async ({
      page,
    }) => {
      const violations: { type: string; length: number }[] = [];
      page.on('console', (message) => {
        if (
          ['error', 'warning'].includes(message.type()) &&
          message.text() !==
            'Failed to load resource: the server responded with a status of 409 (Conflict)'
        )
          violations.push({ type: message.type(), length: message.text().length });
      });
      page.on('pageerror', (error) =>
        violations.push({ type: 'pageerror', length: error.message.length }),
      );
      await page.setViewportSize({ width, height: 900 });
      await page.addInitScript((theme) => {
        localStorage.setItem('nb.lang', 'en');
        localStorage.setItem('nb.theme', theme);
      }, theme);
      await mockPublicConfig(page, 'admin');
      await mockRoleSession(page, 'admin', 'admin');
      const templates = Array.from({ length: 45 }, (_, index) => ({
        id: String(index + 1),
        name: `Template ${String(index + 1).padStart(2, '0')}`,
        description: 'Full saved prices',
        enabled: true,
        revision: '1',
        created_at: 1,
        updated_at: 2,
        pricing: { mode: 'per_request', user_price: '3.5', donor_reward: '1.75' },
        token_reserve_credits: null,
        discount: { enabled: false, percent: 0, start_at: null, end_at: null },
      }));
      let conflict = true;
      await page.route(ADMIN_ORIGIN + '/admin/api/**', async (route) => {
        const request = route.request();
        const url = new URL(request.url());
        const root = '/admin/api/charity-price-templates';
        if (url.pathname === root && request.method() === 'GET') {
          const q = url.searchParams.get('q') ?? '';
          await route.fulfill({
            json: numberedPage(
              templates.filter((row) => row.name.includes(q)),
              url.searchParams,
            ),
          });
          return;
        }
        if (url.pathname.startsWith(root + '/')) {
          const row = templates.find((item) => item.id === url.pathname.split('/').at(-1))!;
          if (request.method() === 'GET') {
            await route.fulfill({ json: row });
            return;
          }
          if (conflict) {
            conflict = false;
            row.revision = '2';
            await route.fulfill({
              status: 409,
              json: { error: { code: 'revision_conflict', message: 'Changed' } },
            });
            return;
          }
          const body = request.postDataJSON();
          delete body.expected_revision;
          Object.assign(row, body, { revision: '3' });
          await route.fulfill({ json: row });
          return;
        }
        if (
          [
            '/admin/api/donations',
            '/admin/api/charity-models',
            '/admin/api/donation-sources',
          ].includes(url.pathname)
        ) {
          await route.fulfill({ json: numberedPage([], url.searchParams) });
          return;
        }
        await route.fallback();
      });
      await page.goto(ADMIN_ORIGIN + '/charity?charity_section=models&model_workspace=new');
      const provider = page.getByLabel('Provider', { exact: true });
      await provider.fill('draft-provider');
      await page.getByLabel('Model', { exact: true }).fill('draft-model');
      await page.getByRole('button', { name: 'Choose a template or reference' }).click();
      await page.locator('summary').filter({ hasText: 'Price templates' }).click();
      await page
        .getByRole('row')
        .filter({ hasText: 'Template 01' })
        .getByRole('button', { name: 'View full prices', exact: true })
        .click();
      await expect(
        page.getByRole('button', { name: 'Fill all prices', exact: true }),
      ).toBeVisible();
      await page.getByRole('button', { name: 'Maintain price templates', exact: true }).click();
      await expect(page.getByRole('alertdialog')).toHaveCount(0);
      await expect(
        page.getByRole('heading', { name: 'Price templates', exact: true }),
      ).toBeVisible();
      const selectedTabVisible = async (name: string) => {
        const tab = page.getByRole('tab', { name, exact: true });
        await expect(tab).toHaveAttribute('aria-selected', 'true');
        await expect
          .poll(() =>
            tab.evaluate((node) => {
              const selected = node.getBoundingClientRect();
              const viewport = node.parentElement!.getBoundingClientRect();
              return selected.left >= viewport.left - 1 && selected.right <= viewport.right + 1;
            }),
          )
          .toBe(true);
      };
      await selectedTabVisible('Price templates');
      const templateList = page.locator('.nb-template-list');
      await templateList.getByPlaceholder('Search template name or description').fill('Template');
      const searched = page.waitForResponse(
        (response) =>
          new URL(response.url()).pathname === '/admin/api/charity-price-templates' &&
          new URL(response.url()).searchParams.get('q') === 'Template',
      );
      await templateList.getByPlaceholder('Search template name or description').press('Enter');
      expect((await searched).ok()).toBe(true);
      const pager = page.getByRole('navigation', { name: 'Pagination' });
      await expect(pager.getByRole('textbox')).toBeEnabled();
      await pager.getByRole('textbox').fill('3');
      await pager.getByRole('textbox').press('Enter');
      await expect(page.getByText('Template 45', { exact: true })).toBeVisible();
      const evidence = process.env.NONBIRI_VISUAL_DIR;
      const shot = async (state: string) => {
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
          true,
        );
        if (evidence) {
          await mkdir(evidence, { recursive: true });
          await page.screenshot({
            path: resolve(evidence, `templates-${width}-${theme}-${state}.png`),
            animations: 'disabled',
          });
        }
      };
      await page
        .getByRole('heading', { name: 'Price templates', exact: true })
        .scrollIntoViewIfNeeded();
      await shot('list');
      await pager.scrollIntoViewIfNeeded();
      await shot('pagination');
      await page
        .getByRole('row')
        .filter({ hasText: 'Template 45' })
        .getByRole('button', { name: 'Edit', exact: true })
        .click();
      await expect(page.getByRole('heading', { name: 'Edit price template' })).toBeVisible();
      const verifyLeavePrompt = async () => {
        await page.getByRole('tab', { name: 'Browse by source', exact: true }).click();
        const prompt = page.getByRole('alertdialog', { name: 'Leave this page?' });
        await expect(prompt).toHaveCount(1);
        await shot('leave');
        await prompt.getByRole('button', { name: 'Stay here', exact: true }).click();
        await expect(page.getByRole('alertdialog')).toHaveCount(0);
        await selectedTabVisible('Price templates');
        await expect(page.getByRole('heading', { name: 'Edit price template' })).toBeVisible();
      };
      await verifyLeavePrompt();
      await page.getByLabel('Template description', { exact: true }).fill('Preserved edit');
      await verifyLeavePrompt();
      expect(await page.getByLabel('Template description', { exact: true }).inputValue()).toBe(
        'Preserved edit',
      );
      await page.getByLabel('Template description', { exact: true }).focus();
      await shot('editor');
      await page.getByRole('button', { name: 'Save template “Template 45”' }).click();
      await expect(
        page.getByText(
          'This template changed. Your draft is kept. Load the latest revision before saving again.',
        ),
      ).toBeVisible();
      await page
        .getByText(
          'This template changed. Your draft is kept. Load the latest revision before saving again.',
        )
        .scrollIntoViewIfNeeded();
      await shot('conflict');
      await page.getByRole('button', { name: 'Load latest revision and keep draft' }).click();
      await expect(page.getByLabel('Template description')).toHaveValue('Preserved edit');
      await page.getByRole('button', { name: 'Save template “Template 45”' }).click();
      await expect(page.getByText('Saved.', { exact: true })).toBeVisible();
      await page.getByRole('button', { name: 'Return to price templates' }).click();
      await expect(page.getByText('Template 45', { exact: true })).toBeVisible();
      await expect(pager.getByRole('textbox')).toHaveValue('3');
      await page.getByRole('button', { name: 'Return to model draft' }).click();
      await selectedTabVisible('Charity models and bindings');
      await expect(page.getByRole('button', { name: 'Fill all prices', exact: true })).toHaveCount(
        0,
      );
      await page.getByRole('button', { name: 'Return to current price draft' }).click();
      await expect(provider).toHaveValue('draft-provider');
      await expect(page.getByLabel('Model', { exact: true })).toHaveValue('draft-model');
      expect(violations).toEqual([]);
    });
  }
