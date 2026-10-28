import { mkdirSync, readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { expect, test, type Page } from '@playwright/test';
import common from '../../src/shared/i18n/common/en.json' with { type: 'json' };
import admin from '../../src/admin/i18n/en.json' with { type: 'json' };

type Fixture = {
  admin_url: string;
  admin_cookie: { Name: string; Value: string };
};

async function inspectLayouts(page: Page, name: string) {
  for (const theme of ['light', 'dark']) {
    await page.evaluate((value) => {
      document.documentElement.dataset.theme = value;
    }, theme);
    for (const width of [1440, 390]) {
      await page.setViewportSize({ width, height: width === 390 ? 844 : 1000 });
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
        true,
      );
      if (process.env.NONBIRI_VISUAL_DIR) {
        mkdirSync(process.env.NONBIRI_VISUAL_DIR, { recursive: true });
        await page.screenshot({
          path: resolve(process.env.NONBIRI_VISUAL_DIR, `${name}-${theme}-${width}.png`),
          animations: 'disabled',
        });
      }
    }
  }
}

test('administrator configures a source, edits personal memory and cleans retained operations', async ({
  browser,
}) => {
  const state = JSON.parse(
    readFileSync(process.env.NONBIRI_AUDIT_BROWSER_STATE!, 'utf8'),
  ) as Fixture;
  const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
  await context.addCookies([
    {
      name: state.admin_cookie.Name,
      value: state.admin_cookie.Value,
      domain: new URL(state.admin_url).hostname,
      path: '/admin',
      httpOnly: true,
      sameSite: 'Lax',
    },
  ]);
  await context.addInitScript(() => {
    localStorage.setItem('nb.lang', 'en');
    localStorage.setItem('nb.theme', 'light');
  });
  const copy = common.common.agent;
  const history = admin.admin.managementHistory;
  try {
    const page = await context.newPage();
    const errors: string[] = [];
    page.on('pageerror', (error) => errors.push(error.message));
    await page.goto(state.admin_url + '/ai?ai_tab=settings');
    await page.getByRole('button', { name: copy.drivers.addSource, exact: true }).click();
    const form = page.getByRole('form', { name: copy.drivers.editSource, exact: true });
    await form.getByLabel(copy.drivers.provider, { exact: true }).selectOption('chatgpt');
    await form.getByLabel(copy.drivers.name, { exact: true }).fill('Browser ChatGPT source');
    await form.getByLabel(common.common.connections.model, { exact: true }).fill('fixture-model');
    await form.getByRole('button', { name: common.common.save, exact: true }).click();
    await expect(
      page
        .getByRole('table', { name: copy.drivers.sources, exact: true })
        .getByText('Browser ChatGPT source', { exact: true }),
    ).toBeVisible();
    const response = await context.request.get(state.admin_url + '/admin/api/ai/drivers');
    expect(response.ok()).toBe(true);
    const config = await response.json();
    const profile = config.profiles.find(
      (item: { name: string }) => item.name === 'Browser ChatGPT source',
    );
    expect(profile.settings.extra_parameters).toEqual({});
    expect(profile.settings.max_output_tokens).toBe(0);
    const assignment = page
      .locator('form')
      .filter({ has: page.getByLabel(copy.drivers.scopes.admin_chat, { exact: true }) });
    await assignment
      .getByLabel(copy.drivers.scopes.admin_chat, { exact: true })
      .selectOption(profile.id);
    await assignment.getByRole('button', { name: common.common.save, exact: true }).click();
    await expect(
      assignment.getByRole('button', { name: common.common.save, exact: true }),
    ).toBeDisabled();
    await page
      .getByRole('heading', { name: copy.drivers.sources, exact: true })
      .scrollIntoViewIfNeeded();
    await inspectLayouts(page, 'assistant-sources');

    await page.goto(state.admin_url + '/ai?ai_tab=memories');
    const content = page.getByLabel(copy.memoryContent, { exact: true });
    await content.fill('Prefer a brief explanation before changing donation settings.');
    await page.getByRole('button', { name: common.common.save, exact: true }).click();
    await expect(page.getByText(common.common.outcome.saved, { exact: true })).toBeVisible();
    await page.reload();
    await expect(content).toHaveValue(
      'Prefer a brief explanation before changing donation settings.',
    );

    await page.goto(state.admin_url + '/management-operations');
    await page.getByLabel(history.fields.domain, { exact: true }).selectOption('ai');
    await page.getByRole('button', { name: history.apply, exact: true }).click();
    await expect(page.getByRole('table', { name: history.groups, exact: true })).toBeVisible();
    await page.getByRole('heading', { name: history.title, exact: true }).scrollIntoViewIfNeeded();
    await inspectLayouts(page, 'management-history');
    await page
      .getByRole('button', { name: /^Open \d/ })
      .first()
      .click();
    await page
      .getByRole('button', { name: /^Details · / })
      .first()
      .click();
    await expect(
      page.getByRole('button', { name: history.closeDetails, exact: true }),
    ).toBeVisible();
    await page.getByRole('button', { name: history.backToGroups, exact: true }).click();
    await page.getByRole('button', { name: history.cleanupFiltered, exact: true }).click();
    const dialog = page.getByRole('alertdialog');
    await expect(dialog).toBeVisible();
    await dialog.getByRole('button', { name: history.cleanupStart, exact: true }).click();
    await expect(dialog).not.toBeVisible();
    await expect(
      page.locator('p').filter({ hasText: new RegExp(`^${history.cleanupStates.completed} ·`) }),
    ).toBeVisible();
    await expect(page.getByRole('table', { name: history.groups, exact: true })).toHaveCount(0);
    expect(errors).toEqual([]);
  } finally {
    await context.close();
  }
});
