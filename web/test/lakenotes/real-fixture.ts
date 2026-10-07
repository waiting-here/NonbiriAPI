import { readFileSync } from 'node:fs';
import { randomBytes } from 'node:crypto';
import { expect, type Browser, type BrowserContext } from '@playwright/test';

interface Fixture {
  user_url: string;
  admin_url: string;
  control_url: string;
  control_token: string;
  users: { id: string; level: number; cookie: { Name: string; Value: string } }[];
  admin_cookie: { Name: string; Value: string };
}
export const statePath = process.env.NONBIRI_IMAGE_BROWSER_STATE!;
export const fixture = (): Fixture => JSON.parse(readFileSync(statePath, 'utf8')) as Fixture;
export async function context(
  browser: Browser,
  admin = false,
  index = 0,
  language = 'en',
  width = 1280,
) {
  const f = fixture(),
    origin = admin ? f.admin_url : f.user_url,
    cookie = admin ? f.admin_cookie : f.users[index].cookie;
  const ctx = await browser.newContext({ viewport: { width, height: 900 } });
  await ctx.addCookies([
    {
      name: cookie.Name,
      value: cookie.Value,
      domain: new URL(origin).hostname,
      path: admin ? '/admin' : '/api',
      httpOnly: true,
      sameSite: 'Lax',
    },
  ]);
  await ctx.addInitScript(
    ({ language }) => {
      if (location.protocol !== 'http:' && location.protocol !== 'https:') return;
      localStorage.setItem('nb.lang', language);
      localStorage.setItem('nb.theme', language === 'zh' ? 'dark' : 'light');
    },
    { language },
  );
  return ctx;
}
export async function api(
  ctx: BrowserContext,
  path: string,
  method = 'GET',
  data?: unknown,
  admin = false,
  key = randomBytes(16).toString('base64url'),
) {
  const origin = admin ? fixture().admin_url : fixture().user_url;
  return ctx.request.fetch(origin + path, {
    method,
    data,
    headers: { Origin: origin, 'Idempotency-Key': key },
  });
}
export async function control(ctx: BrowserContext, action: string, data = {}) {
  const f = fixture(),
    response = await ctx.request.post(f.control_url + '/' + action, {
      data,
      headers: { Authorization: 'Bearer ' + f.control_token },
    });
  expect(response.status()).toBe(200);
}
