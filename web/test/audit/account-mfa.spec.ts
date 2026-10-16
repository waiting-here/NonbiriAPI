import { readFileSync } from 'node:fs';
import { createHmac, randomUUID } from 'node:crypto';
import { expect, test, type Browser } from '@playwright/test';
import copy from '../../src/shared/i18n/common/en.json' with { type: 'json' };

type Cookie = { Name: string; Value: string };
type Fixture = {
  user_url: string;
  admin_url: string;
  control_url: string;
  control_token: string;
  admin_cookie: Cookie;
  mfa_user: { id: string; cookie: Cookie };
};

test.skip(process.env.NONBIRI_MANAGEMENT_BROWSER !== '1', 'Requires management fixture.');

// Independent RFC 4226 calculation for the seed delivered by the real backend.
function code(secret: string, step: number) {
  const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567';
  let bits = '';
  for (const char of secret) bits += alphabet.indexOf(char).toString(2).padStart(5, '0');
  const seed = Buffer.from(bits.match(/.{8}/g)!.map((byte) => parseInt(byte, 2)));
  const counter = Buffer.alloc(8);
  counter.writeBigUInt64BE(BigInt(step));
  const digest = createHmac('sha1', seed).update(counter).digest();
  const offset = digest[19] & 15;
  return String((digest.readUInt32BE(offset) & 0x7fffffff) % 1000000).padStart(6, '0');
}

async function session(browser: Browser, origin: string, cookie: Cookie, admin = false) {
  const context = await browser.newContext({ viewport: { width: 1366, height: 768 } });
  await context.addCookies([{
    name: cookie.Name, value: cookie.Value, domain: new URL(origin).hostname,
    path: admin ? '/admin' : '/api', httpOnly: true, sameSite: 'Lax',
  }]);
  await context.addInitScript(() => localStorage.setItem('nb.lang', 'en'));
  return context;
}

test('real authenticator enrollment, challenge, policy and administrator reset', async ({ browser }) => {
  const state = JSON.parse(readFileSync(process.env.NONBIRI_AUDIT_BROWSER_STATE!, 'utf8')) as Fixture;
  const user = await session(browser, state.user_url, state.mfa_user.cookie);
  const admin = await session(browser, state.admin_url, state.admin_cookie, true);
  const headers = { Origin: state.admin_url, 'Idempotency-Key': randomUUID() };
  const policy = state.admin_url + '/admin/api/site-config/mfa_required_level_2';
  try {
    const page = await user.newPage();
    await page.goto(state.user_url + '/account');
    await page.getByRole('button', { name: copy.mfa.enable, exact: true }).click();
    await page.getByText(copy.mfa.manualKey, { exact: true }).click();
    const secret = await page.getByLabel(copy.mfa.setupKey, { exact: true }).inputValue();
    await page.getByLabel(copy.mfa.code, { exact: true }).fill(code(secret, Math.floor(Date.now() / 30000) - 1));
    await page.getByRole('button', { name: copy.mfa.confirmSetup, exact: true }).click();
    await expect(page.getByRole('alertdialog')).toHaveCount(0);
    await expect(page.getByText(copy.mfa.enabled, { exact: true })).toBeVisible();
    const status = await user.request.get(state.user_url + '/api/auth/mfa');
    expect(await status.json()).toMatchObject({ enrolled: true, required: false, verified: true });

    // The policy uses the actual administrator route and its commit observer.
    expect((await admin.request.patch(policy, { headers, data: { value: true } })).ok()).toBe(true);
    expect((await user.request.post(state.control_url + '/mfa/expire', {
      headers: { Authorization: 'Bearer ' + state.control_token },
    })).ok()).toBe(true);
    const blocked = await user.request.get(state.user_url + '/api/session');
    expect(blocked.status()).toBe(403);
    expect(await blocked.json()).toMatchObject({ error: { code: 'mfa_required' } });
    await page.reload();
    await expect(page.getByRole('heading', { name: copy.mfa.verifyTitle, exact: true })).toBeVisible();
    await page.getByLabel(copy.mfa.code, { exact: true }).fill(code(secret, Math.floor(Date.now() / 30000)));
    await page.getByRole('button', { name: copy.mfa.verify, exact: true }).click();
    await expect(page.getByRole('heading', { name: copy.mfa.accountTitle, exact: true })).toBeVisible();
    await expect(page.getByText(copy.mfa.requiredPolicy, { exact: true })).toBeVisible();
    await expect(page.getByRole('button', { name: copy.mfa.disable, exact: true })).toHaveCount(0);

    const adminPage = await admin.newPage();
    await adminPage.goto(state.admin_url + '/users?user=' + state.mfa_user.id);
    await adminPage.getByRole('button', { name: copy.mfa.reset, exact: true }).click();
    const dialog = adminPage.getByRole('alertdialog');
    await dialog.getByLabel(copy.management.users.elevatedPassword, { exact: true }).fill('correct horse battery staple');
    await dialog.getByRole('button', { name: copy.mfa.resetConfirm, exact: true }).click();
    await expect(adminPage.getByText(copy.mfa.resetDone, { exact: true })).toBeVisible();
    expect((await user.request.get(state.user_url + '/api/auth/mfa')).status()).toBe(401);
  } finally {
    await admin.request.patch(policy, { headers: { ...headers, 'Idempotency-Key': randomUUID() }, data: { value: false } });
    await user.close();
    await admin.close();
  }
});
