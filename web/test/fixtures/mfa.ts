import type { Page } from '@playwright/test';

export const MFA_FIXTURE_SECRET = 'JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP';

export async function mockAccountMfa(
  page: Page,
  initial: { enrolled?: boolean; required?: boolean; verified?: boolean } = {},
) {
  const state = {
    enrolled: false,
    required: false,
    verified: false,
    setupSeconds: 600,
    ...initial,
  };
  const writes: { path: string; code?: string }[] = [];
  const status = () => ({
    enrolled: state.enrolled,
    required: state.required,
    verified: state.verified,
    verified_until: state.verified ? Math.floor(Date.now() / 1000) + 604800 : null,
  });
  await page.route(/\/api\/(session|me)$/, async (route) => {
    if ((state.enrolled || state.required) && !state.verified) {
      await route.fulfill({
        status: 403,
        json: { error: { code: 'mfa_required', message: 'Authenticator required.' } },
      });
    } else await route.fallback();
  });
  await page.route(/\/api\/auth\/mfa(?:\/[^/?]+)?$/, async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    if (request.method() === 'GET') {
      await route.fulfill({ json: status() });
      return;
    }
    const input = request.postDataJSON() as { code?: string };
    writes.push({ path, code: input.code });
    if (path.endsWith('/setup')) {
      await route.fulfill({
        json: {
          secret: MFA_FIXTURE_SECRET,
          otpauth_uri: `otpauth://totp/Fixture?secret=${MFA_FIXTURE_SECRET}&issuer=Fixture`,
          expires_at: Math.floor(Date.now() / 1000) + state.setupSeconds,
        },
      });
      return;
    }
    if (input.code === '000000') {
      await route.fulfill({
        status: 403,
        json: { error: { code: 'mfa_invalid_code', message: 'Incorrect code.' } },
      });
      return;
    }
    if (path.endsWith('/disable')) {
      state.enrolled = false;
      state.verified = false;
    } else {
      state.enrolled = true;
      state.verified = true;
    }
    await route.fulfill({ json: status() });
  });
  return { state, writes };
}
