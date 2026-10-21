import { readFileSync } from 'node:fs';
import { expect, test, type Browser, type BrowserContext, type Locator } from '@playwright/test';

type Cookie = { Name: string; Value: string };
interface Fixture {
  admin_url: string;
  user_url: string;
  admin_cookie: Cookie;
  users: { id: string; level: number; cookie: Cookie }[];
}
interface Metric {
  current: number;
  limit: number;
  peak_24h: number;
  peak_all: number;
}
interface Pressure {
  rpm: Metric;
  concurrency: Metric;
  observed_at: number;
  started_at: number;
  sample_seconds: number;
}
function fixture(): Fixture {
  return JSON.parse(readFileSync(process.env.NONBIRI_AUDIT_BROWSER_STATE!, 'utf8')) as Fixture;
}
async function session(browser: Browser, role: 'admin' | 1 | 5 | 6) {
  const state = fixture();
  const origin = role === 'admin' ? state.admin_url : state.user_url;
  const cookie =
    role === 'admin' ? state.admin_cookie : state.users.find((user) => user.level === role)!.cookie;
  const context = await browser.newContext({
    viewport: { width: 1440, height: 900 },
    reducedMotion: 'reduce',
  });
  await context.addCookies([
    {
      name: cookie.Name,
      value: cookie.Value,
      domain: new URL(origin).hostname,
      path: role === 'admin' ? '/admin' : '/api',
      httpOnly: true,
      sameSite: 'Lax',
    },
  ]);
  await context.addInitScript(() => {
    localStorage.setItem('nb.lang', 'en');
    localStorage.setItem('nb.theme', 'light');
  });
  return { context, origin };
}
function expectPressure(value: Pressure) {
  for (const metric of [value.rpm, value.concurrency])
    for (const key of ['current', 'limit', 'peak_24h', 'peak_all'] as const) {
      expect(Number.isSafeInteger(metric[key]), key).toBe(true);
      expect(metric[key], key).toBeGreaterThanOrEqual(0);
    }
  for (const key of ['observed_at', 'started_at'] as const) {
    expect(Number.isSafeInteger(value[key]), key).toBe(true);
    expect(value[key], key).toBeGreaterThanOrEqual(0);
  }
  expect(value.sample_seconds).toBe(5);
}
async function readPressure(context: BrowserContext, origin: string, path: string) {
  const response = await context.request.get(origin + path, { headers: { Origin: origin } });
  expect(response.status()).toBe(200);
  expect(response.headers()['cache-control']).toContain('no-store');
  const value = (await response.json()) as Pressure;
  expectPressure(value);
  return value;
}
async function expectDisplayed(section: Locator, value: Pressure) {
  for (const [label, metric] of [
    ['Site RPM', value.rpm],
    ['Outbound concurrency', value.concurrency],
  ] as const)
    await expect(section.getByRole('meter', { name: label, exact: true })).toHaveAttribute(
      'aria-valuetext',
      `Current ${metric.current}, limit ${metric.limit}, 24-hour peak ${metric.peak_24h}, all-time peak ${metric.peak_all}`,
    );
}

for (const role of ['admin', 5, 6] as const) {
  test(`${role} reads real site pressure and displays the sampled values`, async ({ browser }) => {
    const { context, origin } = await session(browser, role);
    try {
      const path = (role === 'admin' ? '/admin/api' : '/api/steward') + '/logs/pressure';
      await readPressure(context, origin, path);
      const page = await context.newPage();
      await page.bringToFront();
      const loaded = page.waitForResponse(
        async (response) =>
          new URL(response.url()).pathname === path &&
          response.request().method() === 'GET' &&
          response.status() === 200 &&
          ((await response.json()) as Pressure).observed_at > 0,
        { timeout: 25_000 },
      );
      await page.goto(origin + (role === 'admin' ? '/logs' : '/steward?tab=logs'));
      const response = await loaded;
      expect(response.headers()['cache-control']).toContain('no-store');
      const value = (await response.json()) as Pressure;
      expectPressure(value);
      const section = page.getByRole('region', { name: 'Site load', exact: true });
      if (role === 'admin') {
        const receivedAt = Date.now();
        const refreshed = page.waitForResponse(
          (candidate) =>
            new URL(candidate.url()).pathname === path &&
            candidate.request().method() === 'GET' &&
            Date.now() - receivedAt >= 12_000,
          { timeout: 25_000 },
        );
        await expectDisplayed(section, value);
        expect(await page.evaluate(() => document.visibilityState)).toBe('visible');
        const next = await refreshed;
        expect(next.status()).toBe(200);
        expect(next.headers()['cache-control']).toContain('no-store');
        const updated = (await next.json()) as Pressure;
        expectPressure(updated);
        expect(updated.observed_at).toBeGreaterThan(value.observed_at);
        await expectDisplayed(section, updated);
      } else {
        await expectDisplayed(section, value);
      }
    } finally {
      await context.close();
    }
  });
}

test('ordinary accounts cannot read site pressure and own records make no pressure request', async ({
  browser,
}) => {
  const { context, origin } = await session(browser, 1);
  try {
    const denied = await context.request.get(origin + '/api/steward/logs/pressure', {
      headers: { Origin: origin },
    });
    expect(denied.status()).toBe(403);
    const page = await context.newPage();
    const pressureRequests: string[] = [];
    page.on('request', (request) => {
      if (new URL(request.url()).pathname.endsWith('/logs/pressure'))
        pressureRequests.push(request.url());
    });
    const loaded = page.waitForResponse(
      (response) =>
        new URL(response.url()).pathname === '/api/logs' && response.request().method() === 'GET',
    );
    await page.goto(origin + '/logs');
    expect((await loaded).status()).toBe(200);
    await expect(page.getByRole('heading', { name: 'Records', exact: true })).toBeVisible();
    await expect(page.getByRole('region', { name: 'Site load', exact: true })).toHaveCount(0);
    expect(pressureRequests).toEqual([]);
  } finally {
    await context.close();
  }
});
