import { expect, test } from './test';
import { mockPublicConfig, mockRoleSession } from './support';
import { createOptions, mockConnectionResources } from './connection-fixtures';
import { USER_ORIGIN } from './ports';
import { serviceCopy, servicePanel } from '../service-onboarding';
import common from '../../src/shared/i18n/common/en.json' with { type: 'json' };
import user from '../../src/user/i18n/en.json' with { type: 'json' };

for (const width of [1440, 390]) {
  test(`service Sheet keyboard focus and drafts survive dismissal and tab changes ${width}`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 900 });
    await page.emulateMedia({ reducedMotion: 'reduce' });
    await page.addInitScript(() => localStorage.setItem('nb.lang', 'en'));
    await mockPublicConfig(page, 'user');
    await mockRoleSession(page, 'user', 'user');
    await mockConnectionResources(page);
    await page.route('**/api/endpoint-create-options**', (route) =>
      route.fulfill({ json: createOptions }),
    );
    const c = common.common.connections;
    const services = user.user.services;
    const workflow = serviceCopy();
    await page.goto(USER_ORIGIN + '/endpoints?connection_id=1');
    const add = page.getByRole('button', { name: c.addEntry, exact: true });
    await add.click();
    const sheet = servicePanel(page, 'en', true);
    await sheet
      .getByLabel(services.serviceAddress, { exact: true })
      .fill('https://draft.example/v1');
    await sheet.getByLabel(workflow.serviceName, { exact: true }).fill('Unfinished endpoint');
    await expect(
      sheet.getByRole('radio', { name: c.existingCredential, exact: true }),
    ).toBeChecked();
    await expect(
      sheet.getByRole('button', { name: services.addAndFetch, exact: true }),
    ).toBeDisabled();
    const advanced = sheet.locator('summary').filter({ hasText: services.advancedSetup });
    await advanced.click();
    await expect(sheet.getByRole('combobox', { name: c.protocol, exact: true })).toBeVisible();
    await advanced.click();
    await expect(sheet.getByRole('combobox', { name: c.protocol, exact: true })).toBeHidden();
    await sheet.getByRole('heading', { name: c.addEntry, exact: true }).focus();
    await page.keyboard.press('Shift+Tab');
    await expect(advanced).toBeFocused();
    await page.keyboard.press('Tab');
    await expect(sheet.getByRole('button', { name: c.close, exact: true })).toBeFocused();

    await page.keyboard.press('Escape');
    await expect(sheet).toBeHidden();
    await expect(add).toBeFocused();
    await add.click();
    await expect(sheet.getByLabel(services.serviceAddress, { exact: true })).toHaveValue(
      'https://draft.example/v1',
    );
    await expect(sheet.getByLabel(workflow.serviceName, { exact: true })).toHaveValue(
      'Unfinished endpoint',
    );
    await sheet.getByRole('button', { name: c.close, exact: true }).click();

    await page.getByRole('tab', { name: c.editConnection, exact: true }).click();
    await page.getByRole('textbox', { name: c.name, exact: true }).fill('Unfinished provider name');
    await page.getByRole('tab', { name: c.credentials, exact: true }).click();
    await page.getByRole('button', { name: c.newCredential, exact: true }).click();
    const keyDraft = page.getByRole('region', { name: c.newCredential, exact: true });
    await keyDraft.getByLabel(c.secret, { exact: true }).fill('synthetic-retained-key');
    await page.getByRole('tab', { name: c.entries, exact: true }).click();
    await expect(keyDraft).toBeHidden();
    await page.getByRole('tab', { name: c.editConnection, exact: true }).click();
    await expect(page.getByRole('textbox', { name: c.name, exact: true })).toHaveValue(
      'Unfinished provider name',
    );
    await page.getByRole('tab', { name: c.credentials, exact: true }).click();
    await expect(keyDraft.getByLabel(c.secret, { exact: true })).toHaveValue(
      'synthetic-retained-key',
    );
  });
}
