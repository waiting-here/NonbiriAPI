import { mkdirSync, readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { expect, test, type Locator, type Page } from '@playwright/test';
import common from '../../src/shared/i18n/common/en.json' with { type: 'json' };
import admin from '../../src/admin/i18n/en.json' with { type: 'json' };

type Fixture = {
  admin_url: string;
  user_url: string;
  admin_cookie: { Name: string; Value: string };
  users: { id: string; level: number; cookie: { Name: string; Value: string } }[];
};

async function inspectLayouts(page: Page, name: string, anchor?: Locator) {
  for (const theme of ['light', 'dark']) {
    await page.evaluate((value) => {
      document.documentElement.dataset.theme = value;
    }, theme);
    for (const width of [1440, 390]) {
      await page.setViewportSize({ width, height: width === 390 ? 844 : 1000 });
      if (anchor) await anchor.scrollIntoViewIfNeeded();
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

test('administrator assigns Codex individually and retains readable changes after deleting its source', async ({
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
  const originalName = 'Browser Codex source';
  const revisedName = 'Renamed Codex source';
  try {
    const page = await context.newPage();
    const errors: string[] = [];
    const authorizationRequests: string[] = [];
    page.on('pageerror', (error) => errors.push(error.message));
    page.on('request', (request) => {
      if (new URL(request.url()).hostname === 'auth.openai.com')
        authorizationRequests.push(request.method());
    });
    await page.goto(state.admin_url + '/ai?ai_tab=settings');
    const codex = page.getByRole('region', { name: copy.codex.title, exact: true });
    await expect(codex.getByText(copy.codex.disconnected, { exact: true })).toBeVisible();
    await expect(
      codex.getByRole('button', { name: copy.codex.connect, exact: true }),
    ).toBeEnabled();
    await inspectLayouts(page, 'codex-disconnected', codex);
    await page.getByRole('button', { name: copy.drivers.addSource, exact: true }).click();
    const form = page.getByRole('form', { name: copy.drivers.editSource, exact: true });
    await form.getByLabel(copy.drivers.provider, { exact: true }).selectOption('codex');
    await form.getByLabel(copy.drivers.name, { exact: true }).fill(originalName);
    await form.getByLabel(common.common.connections.model, { exact: true }).fill('original-model');
    await expect(form.getByLabel(common.common.connections.entryURL, { exact: true })).toHaveCount(
      0,
    );
    await expect(form.getByLabel(common.common.connections.secret, { exact: true })).toHaveCount(0);
    await form.getByRole('button', { name: common.common.save, exact: true }).click();
    const sources = page.getByRole('table', { name: copy.drivers.sources, exact: true });
    await expect(sources.getByText(originalName, { exact: true })).toBeVisible();
    const readConfiguration = async () => {
      const response = await context.request.get(state.admin_url + '/admin/api/ai/drivers');
      expect(response.ok()).toBe(true);
      return response.json();
    };
    const configuration = await readConfiguration();
    const profile = configuration.profiles.find(
      (entry: { name: string }) => entry.name === originalName,
    );
    expect(profile.settings.provider).toBe('codex');
    const bindings = page.getByRole('region', { name: copy.drivers.bindings, exact: true });
    for (const scope of ['default', 'auto_review', 'steward_chat'] as const) {
      await expect(
        bindings.getByLabel(copy.drivers.scopes[scope], { exact: true }).locator('option', {
          hasText: originalName,
        }),
      ).toHaveCount(0);
    }
    const administrator = bindings.locator('form').filter({
      has: page.getByLabel(copy.drivers.scopes.admin_chat, { exact: true }),
    });
    await administrator.getByRole('combobox').selectOption(profile.id);
    await administrator.getByRole('button', { name: common.common.save, exact: true }).click();
    await expect(
      administrator.getByRole('button', { name: common.common.save, exact: true }),
    ).toBeDisabled();
    const individual = bindings.locator('details').filter({
      has: page.locator('summary', { hasText: copy.drivers.individual }),
    });
    await individual.locator('summary').click();
    const steward = state.users.find((entry) => entry.level === 5)!;
    await individual.getByLabel(copy.drivers.steward, { exact: true }).selectOption(steward.id);
    const personal = individual.locator('form');
    await personal.getByRole('combobox').selectOption(profile.id);
    await personal.getByRole('button', { name: common.common.save, exact: true }).click();
    await expect(
      personal.getByRole('button', { name: common.common.save, exact: true }),
    ).toBeDisabled();
    const assigned = (await readConfiguration()).bindings.filter(
      (entry: { driver_id: string }) => entry.driver_id === profile.id,
    );
    expect(
      assigned
        .map((entry: { scope: string; actor_id: string }) => [entry.scope, entry.actor_id])
        .sort(),
    ).toEqual(
      [
        ['admin_chat', '0'],
        ['steward_chat', steward.id],
      ].sort(),
    );
    await inspectLayouts(page, 'codex-individual-source', individual);

    await form.getByLabel(copy.drivers.name, { exact: true }).fill(revisedName);
    await form.getByLabel(common.common.connections.model, { exact: true }).fill('revised-model');
    await form.getByRole('button', { name: common.common.save, exact: true }).click();
    await expect(sources.getByText(revisedName, { exact: true })).toBeVisible();
    for (const binding of [administrator, personal]) {
      await binding.getByRole('combobox').selectOption('');
      await binding.getByRole('button', { name: common.common.save, exact: true }).click();
      await expect(
        binding.getByRole('button', { name: common.common.save, exact: true }),
      ).toBeDisabled();
    }
    await form.getByRole('button', { name: common.common.connections.delete, exact: true }).click();
    await form
      .getByRole('button', { name: common.common.connections.delete, exact: true })
      .last()
      .click();
    await expect(sources.getByText(revisedName, { exact: true })).toHaveCount(0);
    expect(
      (await readConfiguration()).profiles.some((entry: { id: string }) => entry.id === profile.id),
    ).toBe(false);

    const root = '/admin/api/management-operations';
    const query = `target_kind=agent_driver&target_id=${profile.id}`;
    const response = await context.request.get(
      state.admin_url + root + '?view=operations&' + query,
    );
    expect(response.ok()).toBe(true);
    const operations = (await response.json()).operations as { id: string; action: string }[];
    const renamed = operations.find((entry) => entry.action.endsWith('.update'))!;
    const deleted = operations.find((entry) => entry.action.endsWith('.delete'))!;
    expect(renamed).toBeDefined();
    expect(deleted).toBeDefined();
    await page.goto(
      state.admin_url +
        `/management-operations?mh_view=operations&mh_target_kind=agent_driver&mh_target_id=${profile.id}`,
    );
    const search = page.getByRole('searchbox', { name: history.fields.search, exact: true });
    await search.fill(originalName);
    await page.getByRole('button', { name: history.apply, exact: true }).click();
    await page
      .getByRole('button', { name: `${history.details} · ${renamed.id}`, exact: true })
      .click();
    const targets = page.getByRole('table', { name: history.targets, exact: true });
    await expect(targets).toContainText(revisedName);
    await expect(targets).toContainText(
      history.snapshot.previousName.replace('{{name}}', originalName),
    );
    const nameChange = targets.locator('.management-history__changes > div').filter({
      has: page.locator('dt[title="name"]'),
    });
    await expect(nameChange).toContainText(originalName);
    await expect(nameChange).toContainText(revisedName);
    const modelChange = targets.locator('.management-history__changes > div').filter({
      has: page.locator('dt[title="settings_json.model"]'),
    });
    await expect(modelChange).toContainText('original-model');
    await expect(modelChange).toContainText('revised-model');
    await inspectLayouts(page, 'management-renamed-snapshot', nameChange);
    await page.getByRole('button', { name: history.closeDetails, exact: true }).click();
    await search.fill(revisedName);
    await page.getByRole('button', { name: history.apply, exact: true }).click();
    await page
      .getByRole('button', { name: `${history.details} · ${deleted.id}`, exact: true })
      .click();
    await expect(targets).toContainText(revisedName);
    const deletedState = targets.getByText(history.snapshot.states.deleted, { exact: true });
    await expect(deletedState).toBeVisible();
    await inspectLayouts(page, 'management-deleted-snapshot', deletedState);
    expect(authorizationRequests).toEqual([]);
    expect(errors).toEqual([]);
  } finally {
    await context.close();
  }
});
