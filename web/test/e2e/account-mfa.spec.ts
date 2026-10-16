import { mkdir } from 'node:fs/promises';
import { join } from 'node:path';
import { expect, test } from './test';
import { ADMIN_ORIGIN, USER_ORIGIN } from './ports';
import {
  assertNoSensitiveBrowserPersistence,
  installURLPersistenceObserver,
  mockPublicConfig,
  mockRoleSession,
} from './support';
import { MFA_FIXTURE_SECRET, mockAccountMfa } from '../fixtures/mfa';
import { managedUserFixture } from '../fixtures/managedUsers';
import en from '../../src/shared/i18n/common/en.json' with { type: 'json' };
import zh from '../../src/shared/i18n/common/zh.json' with { type: 'json' };

for (const width of [1440, 390])
  for (const theme of ['light', 'dark'] as const) {
    test(`account MFA setup ${width} ${theme}`, async ({ page, context }) => {
      const language = theme === 'dark' ? 'zh' : 'en';
      const copy = (language === 'zh' ? zh : en).mfa;
      const errors: { type: string; length: number }[] = [];
      page.on('pageerror', (error) =>
        errors.push({ type: 'pageerror', length: error.message.length }),
      );
      page.on('console', (message) => {
        if (!['error', 'warning'].includes(message.type())) return;
        if (/^Failed to load resource:.*403/.test(message.text())) return;
        errors.push({ type: message.type(), length: message.text().length });
      });
      await page.setViewportSize({ width, height: 900 });
      await page.addInitScript(
        ({ theme, language }) => {
          localStorage.setItem('nb.theme', theme);
          localStorage.setItem('nb.lang', language);
        },
        { theme, language },
      );
      await mockRoleSession(page, 'user', 'user');
      await mockPublicConfig(page, 'user');
      const fixture = await mockAccountMfa(page, { required: true });
      await installURLPersistenceObserver(context, [MFA_FIXTURE_SECRET]);
      await page.goto(USER_ORIGIN + '/account');
      await expect(page.getByRole('heading', { name: copy.setupTitle, exact: true })).toBeVisible();
      const qr = page.getByRole('img', { name: copy.qrLabel });
      await expect(qr).toBeVisible();
      await expect.poll(() => qr.evaluate((node) => (node as HTMLCanvasElement).width)).toBe(224);
      await expect(
        page.getByRole('button', { name: copy.confirmSetup, exact: true }),
      ).toBeInViewport({ ratio: 1 });
      await page.getByText(copy.manualKey, { exact: true }).click();
      await expect(page.getByLabel(copy.setupKey, { exact: true })).toHaveValue(MFA_FIXTURE_SECRET);
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
        true,
      );
      const directory = process.env.NONBIRI_VISUAL_DIR;
      if (directory) {
        await mkdir(directory, { recursive: true });
        await page.screenshot({ path: join(directory, `mfa-setup-${width}-${theme}.png`) });
      }
      await page.getByLabel(copy.code, { exact: true }).fill('000000');
      await page.getByRole('button', { name: copy.confirmSetup, exact: true }).click();
      await expect(page.getByText(copy.invalidCode, { exact: true })).toBeVisible();
      await expect(page.getByLabel(copy.code, { exact: true })).toHaveValue('');
      if (directory)
        await page.screenshot({ path: join(directory, `mfa-error-${width}-${theme}.png`) });
      await page.getByLabel(copy.code, { exact: true }).fill('123456');
      await page.getByLabel(copy.code, { exact: true }).press('Enter');
      await expect(
        page.getByRole('heading', { name: copy.accountTitle, exact: true }),
      ).toBeVisible();
      await expect(qr).toHaveCount(0);
      await expect(page.getByRole('button', { name: copy.disable, exact: true })).toHaveCount(0);
      expect(
        fixture.writes
          .filter((write) => write.path.endsWith('/confirm'))
          .map((write) => write.code),
      ).toEqual(['000000', '123456']);
      await assertNoSensitiveBrowserPersistence(page, [MFA_FIXTURE_SECRET]);
      expect(errors).toEqual([]);
    });
  }

test('optional account enables and disables an authenticator with a new code', async ({ page }) => {
  await mockRoleSession(page, 'user', 'user');
  await mockPublicConfig(page, 'user');
  const fixture = await mockAccountMfa(page);
  await page.goto(USER_ORIGIN + '/account');
  await page.getByRole('button', { name: en.mfa.enable, exact: true }).click();
  await page.getByLabel(en.mfa.code, { exact: true }).fill('123456');
  await page.getByRole('button', { name: en.mfa.confirmSetup, exact: true }).click();
  await expect(page.getByRole('alertdialog')).toHaveCount(0);
  await page.getByRole('button', { name: en.mfa.disable, exact: true }).click();
  const dialog = page.getByRole('alertdialog');
  await expect(dialog.getByText(en.mfa.freshCodeHelp, { exact: true })).toBeVisible();
  await dialog.getByLabel(en.mfa.code, { exact: true }).fill('654321');
  await dialog.getByRole('button', { name: en.mfa.disableConfirm, exact: true }).click();
  await expect(page.getByRole('alertdialog')).toHaveCount(0);
  await expect(page.getByRole('button', { name: en.mfa.enable, exact: true })).toBeVisible();
  expect(fixture.state.enrolled).toBe(false);
});

test('expiry preserves the announcement editor and never replays a write', async ({ page }) => {
  await mockRoleSession(page, 'user', 'level6');
  await mockPublicConfig(page, 'user');
  const fixture = await mockAccountMfa(page, { enrolled: true, verified: true });
  const id = 'ann_' + 'A'.repeat(22);
  const notice = {
    id,
    state: 'draft',
    revision: '1',
    draft: { zh: null, en: { title: 'Original title', body: 'Original body' } },
    published: null,
    severity: 'info',
    pinned: false,
    dismissible: true,
    expires_at: null,
    withdrawn_at: null,
    created_at: 1800000000,
    updated_at: 1800000000,
  };
  let writes = 0;
  await page.route('**/api/steward/announcements/**', async (route) => {
    if (route.request().method() === 'PATCH') writes++;
    if (!fixture.state.verified) {
      await route.fulfill({
        status: 403,
        json: { error: { code: 'mfa_required', message: 'Verify authenticator.' } },
      });
      return;
    }
    await route.fulfill({ json: notice });
  });
  await page.goto(USER_ORIGIN + '/steward?tab=announcements&announcement=' + id);
  const input = page.getByLabel('English title', { exact: true });
  await input.fill('Keep this edited title');
  await input.evaluate((node) => node.setAttribute('data-kept-input', 'same'));
  fixture.state.verified = false;
  await page.getByRole('button', { name: 'Save private draft', exact: true }).click();
  const dialog = page.getByRole('alertdialog', { name: en.mfa.verifyTitle });
  await expect(dialog).toBeVisible();
  await expect(input).toHaveAttribute('data-kept-input', 'same');
  const directory = process.env.NONBIRI_VISUAL_DIR;
  if (directory) {
    await mkdir(directory, { recursive: true });
    await page.screenshot({ path: join(directory, 'mfa-recovery-dialog.png') });
  }
  await page.keyboard.press('Escape');
  await expect(dialog).toHaveCount(0);
  await expect(input).toHaveValue('Keep this edited title');
  await page.getByRole('button', { name: en.mfa.verify, exact: true }).click();
  await page.getByLabel(en.mfa.code, { exact: true }).fill('654321');
  await page
    .getByRole('alertdialog')
    .getByRole('button', { name: en.mfa.verify, exact: true })
    .click();
  await expect(page.getByRole('alertdialog')).toHaveCount(0);
  await expect(input).toHaveValue('Keep this edited title');
  await expect(input).toHaveAttribute('data-kept-input', 'same');
  expect(writes).toBe(1);
});

test('administrator reset requires confirmation and fresh elevation', async ({
  page,
}) => {
  await mockRoleSession(page, 'admin', 'admin');
  await mockPublicConfig(page, 'admin');
  const target = managedUserFixture();
  let enrolled = true;
  let resets = 0;
  let elevations = 0;
  await page.route('**/admin/api/auth/elevate', async (route) => {
    expect(route.request().postDataJSON()).toEqual({ password: 'synthetic-password' });
    elevations++;
    await route.fulfill({
      json: { token: 'synthetic-elevation', expires_at: Math.floor(Date.now() / 1000) + 300 },
    });
  });
  await page.route('**/admin/api/users**', async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith('/mfa/reset')) {
      expect(route.request().headers()['x-elevated-token']).toBe('synthetic-elevation');
      expect(route.request().postData()).toBeNull();
      resets++;
      enrolled = false;
      await route.fulfill({ json: { enrolled: false } });
    } else if (path.endsWith('/mfa')) await route.fulfill({ json: { enrolled } });
    else if (path.endsWith('/7')) await route.fulfill({ json: target });
    else
      await route.fulfill({
        json: {
          data: [target],
          next_cursor: null,
          pagination: { page: '1', page_size: 20, total_items: '1', total_pages: '1' },
        },
      });
  });
  await page.goto(ADMIN_ORIGIN + '/users?user=7');
  await page.getByRole('button', { name: en.mfa.reset, exact: true }).click();
  const dialog = page.getByRole('alertdialog', { name: en.mfa.resetTitle });
  await expect(
    dialog.getByRole('button', { name: en.mfa.resetConfirm, exact: true }),
  ).toBeDisabled();
  expect(resets).toBe(0);
  await dialog
    .getByLabel(en.management.users.elevatedPassword, { exact: true })
    .fill('synthetic-password');
  const directory = process.env.NONBIRI_VISUAL_DIR;
  if (directory) {
    await mkdir(directory, { recursive: true });
    await page.screenshot({ path: join(directory, 'mfa-admin-reset-dialog.png') });
  }
  await dialog.getByRole('button', { name: en.mfa.resetConfirm, exact: true }).click();
  await expect(page.getByText(en.mfa.resetDone, { exact: true })).toBeVisible();
  expect(resets).toBe(1);
  expect(elevations).toBe(1);
  await expect(page.getByRole('button', { name: en.mfa.reset, exact: true })).toHaveCount(0);
});

test('administrator level requirements default off and save independently', async ({ page }) => {
  await mockRoleSession(page, 'admin', 'admin');
  await mockPublicConfig(page, 'admin');
  const catalog = Array.from({ length: 6 }, (_, index) => {
    const level = index + 1;
    const key = `mfa_required_level_${level}`;
    return {
      key,
      group: 'access',
      type: 'boolean',
      title: { zh: `L${level} 要求验证器`, en: `Require authenticator for L${level}` },
      description: {
        zh: '按当前有效等级要求验证器。',
        en: 'Require an authenticator for the current effective level.',
      },
      unit: null,
      nullable: false,
      null_writable: false,
      raw_default: false,
      effective_fallback: false,
      minimum: null,
      maximum: null,
      step: null,
      allowed_values: [],
      zero_semantics: { zh: '不适用', en: 'Not applicable' },
      null_semantics: { zh: '不接受', en: 'Not accepted' },
      empty_semantics: { zh: '不接受', en: 'Not accepted' },
      independent_gates: [],
      write_endpoint: '/admin/api/site-config/' + key,
    };
  });
  let revision = '1';
  const values: Record<string, boolean> = Object.fromEntries(
    catalog.map((entry) => [entry.key, false]),
  );
  let saved: unknown;
  await page.route('**/admin/api/site-config**', async (route) => {
    if (new URL(route.request().url()).pathname.endsWith('/catalog')) {
      await route.fulfill({ json: { data: catalog } });
    } else if (route.request().method() === 'PATCH') {
      const input = route.request().postDataJSON() as {
        values: Record<string, boolean>;
        expected_revision: string;
      };
      saved = input;
      Object.assign(values, input.values);
      revision = '2';
      await route.fulfill({ json: { revision, changed_keys: Object.keys(input.values) } });
    } else await route.fulfill({ json: { revision, values } });
  });
  await page.goto(ADMIN_ORIGIN + '/settings?group=access');
  for (let level = 1; level <= 6; level++)
    await expect(
      page.getByRole('switch', { name: `Require authenticator for L${level}`, exact: true }),
    ).not.toBeChecked();
  await page.getByRole('switch', { name: 'Require authenticator for L6', exact: true }).check();
  await page.getByRole('button', { name: 'Save all changes', exact: true }).click();
  expect(saved).toBeUndefined();
  await page
    .getByRole('alertdialog')
    .getByRole('button', { name: 'Save all changes', exact: true })
    .click();
  await expect
    .poll(() => saved)
    .toEqual({ expected_revision: '1', values: { mfa_required_level_6: true } });
  for (let level = 1; level <= 5; level++)
    await expect(
      page.getByRole('switch', { name: `Require authenticator for L${level}`, exact: true }),
    ).not.toBeChecked();
});
