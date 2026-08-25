import { ADMIN_ORIGIN, FIXTURE_ORIGIN, USER_ORIGIN } from './ports';
import {
  assertNoSensitiveBrowserPersistence,
  collectConsoleViolations,
  installURLPersistenceObserver,
  mockJson,
  mockPublicConfig,
  mockRoleSession,
  tabTo,
  useNarrowReducedMotion,
} from './support';
import { expect, test } from './test';

const EPHEMERAL_MARKER = 'synthetic-ephemeral-marker';

test('JSON fixtures require exact origin, method, pathname, and search', async ({
  context,
  page,
}) => {
  await mockRoleSession(page, 'fixture', 'level4');
  await mockJson(page, {
    origin: FIXTURE_ORIGIN,
    method: 'GET',
    path: '/fixture/api/exact?mode=one',
    body: { result: 'exact-fixture' },
  });
  await page.goto(`${FIXTURE_ORIGIN}/fixture/route/exact`);

  expect(
    await page.evaluate(
      async (url) => (await (await fetch(url)).json()).result,
      '/fixture/api/exact?mode=one',
    ),
  ).toBe('exact-fixture');
  expect(
    await page.evaluate(async (url) => {
      try {
        await fetch(url);
        return false;
      } catch {
        return true;
      }
    }, 'https://example.invalid/fixture/api/exact?mode=one'),
  ).toBe(true);

  const wrongMethod = await page.evaluate(async (url) => {
    const response = await fetch(url, { method: 'POST' });
    return { body: await response.json(), status: response.status };
  }, '/fixture/api/exact?mode=one');
  expect(wrongMethod).toEqual({ body: { error: 'method_not_allowed' }, status: 405 });
  const wrongQuery = await page.evaluate(async (url) => {
    const response = await fetch(url);
    return { body: await response.json(), status: response.status };
  }, '/fixture/api/exact?mode=two');
  expect(wrongQuery).toEqual({ body: { error: 'unmocked_test_api' }, status: 404 });

  const popupPromise = page.waitForEvent('popup');
  await page.evaluate(() => window.open('about:blank', '_blank'));
  const popup = await popupPromise;
  await popup.goto(`${FIXTURE_ORIGIN}/fixture/popup`);
  expect(
    await popup.evaluate(async (url) => {
      return Promise.race([
        fetch(url).then(
          () => false,
          () => true,
        ),
        new Promise<boolean>((resolve) => setTimeout(() => resolve(false), 2_000)),
      ]);
    }, 'https://example.invalid/fixture/api/exact?mode=one'),
  ).toBe(true);
  await popup.close();

  for (const apiURL of [`${USER_ORIGIN}/api`, `${ADMIN_ORIGIN}/admin/api`]) {
    const response = await context.request.get(apiURL);
    expect(response.status()).toBe(404);
    expect(await response.json()).toEqual({ error: 'unmocked_test_api' });
  }

  const webSocketResult = await page.evaluate(
    (url) =>
      new Promise<string>((resolve) => {
        const socket = new WebSocket(url);
        let settled = false;
        let timeoutId = 0;
        const finish = (result: string) => {
          if (settled) return;
          settled = true;
          clearTimeout(timeoutId);
          resolve(result);
        };
        socket.addEventListener('open', () => finish('open'));
        socket.addEventListener('error', () => finish('error'));
        socket.addEventListener('close', (event) => finish(`close:${event.code}`));
        timeoutId = window.setTimeout(() => finish('timeout'), 2_000);
      }),
    'wss://example.invalid/fixture/api/exact?mode=one',
  );
  expect(webSocketResult).not.toBe('open');
  expect(webSocketResult).not.toBe('timeout');
  expect(context.pages()).toHaveLength(1);
});

test('URL observer remembers a history mutation after the URL is cleaned', async ({
  context,
  page,
}) => {
  await installURLPersistenceObserver(context, [EPHEMERAL_MARKER]);
  await mockRoleSession(page, 'fixture', 'level4');
  await page.goto(`${FIXTURE_ORIGIN}/fixture/route/url-observer`);
  await page.evaluate((marker) => {
    history.pushState({}, '', `/fixture/history/${marker}`);
    history.replaceState({}, '', '/fixture/history/clean');
  }, EPHEMERAL_MARKER);
  expect(page.url()).not.toContain(EPHEMERAL_MARKER);
  await expect(assertNoSensitiveBrowserPersistence(page, [EPHEMERAL_MARKER])).rejects.toThrow(
    'a synthetic marker entered URL history',
  );
});

test('URL observer remembers a full navigation after the URL is cleaned', async ({
  context,
  page,
}) => {
  await installURLPersistenceObserver(context, [EPHEMERAL_MARKER]);
  await page.goto(`${FIXTURE_ORIGIN}/fixture/navigation?marker=${EPHEMERAL_MARKER}`);
  await page.goto(`${FIXTURE_ORIGIN}/fixture/navigation/clean`);
  expect(page.url()).not.toContain(EPHEMERAL_MARKER);
  await expect(assertNoSensitiveBrowserPersistence(page, [EPHEMERAL_MARKER])).rejects.toThrow(
    'a synthetic marker entered URL history',
  );
});

test('URL persistence scan rejects a token-set mismatch', async ({ context, page }) => {
  await installURLPersistenceObserver(context, [EPHEMERAL_MARKER]);
  await page.goto(`${FIXTURE_ORIGIN}/fixture/navigation/clean`);
  await expect(
    assertNoSensitiveBrowserPersistence(page, ['synthetic-different-marker']),
  ).rejects.toThrow('Persistence scan token set does not match');
});

test('URL observer installation rejects an already navigated page', async ({ context, page }) => {
  await page.goto(`${FIXTURE_ORIGIN}/fixture/navigation/already-started`);
  await expect(installURLPersistenceObserver(context, [EPHEMERAL_MARKER])).rejects.toThrow(
    'before the first navigation',
  );
});

test('fixture exposes reusable viewport, keyboard, API, role, console, and storage guards', async ({
  context,
  page,
}) => {
  await installURLPersistenceObserver(context, [EPHEMERAL_MARKER]);
  const warningGuard = collectConsoleViolations(page);
  await page.evaluate(() => console.warn('synthetic warning for the console guard'));
  expect(() => warningGuard.assertNone()).toThrow();
  const consoleGuard = collectConsoleViolations(page);
  await useNarrowReducedMotion(page);
  await mockRoleSession(page, 'fixture', 'level4');
  await mockJson(page, {
    origin: FIXTURE_ORIGIN,
    method: 'GET',
    path: '/fixture/api/status',
    body: {
      error: { code: 'fixture_unavailable', message: EPHEMERAL_MARKER },
    },
  });

  await page.goto(`${FIXTURE_ORIGIN}/fixture/route/start?locale=zh&theme=dark`);
  await expect(page.getByRole('heading', { name: 'Browser test fixture' })).toBeVisible();
  await expect(page.locator('html')).toHaveAttribute('lang', 'zh-CN');
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
  expect(page.viewportSize()).toEqual({ width: 390, height: 844 });
  expect(await page.evaluate(() => matchMedia('(prefers-reduced-motion: reduce)').matches)).toBe(
    true,
  );
  await expect(page.locator('#role')).toHaveText('level4');

  const routeLink = page.getByRole('link', { name: 'Next fixture route' });
  await tabTo(page, routeLink);
  await page.keyboard.press('Enter');
  await expect(page.locator('#route')).toHaveText('/fixture/route/next');

  const apiButton = page.getByRole('button', { name: 'Load fixture API' });
  await tabTo(page, apiButton);
  await page.keyboard.press('Enter');
  await expect(page.locator('#api-result')).toHaveText('fixture_unavailable');
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true,
  );
  await page.evaluate(
    (marker) => localStorage.setItem('fixture-transient', marker),
    EPHEMERAL_MARKER,
  );
  await expect(assertNoSensitiveBrowserPersistence(page, [EPHEMERAL_MARKER])).rejects.toThrow();
  await page.evaluate(() => localStorage.removeItem('fixture-transient'));
  await assertNoSensitiveBrowserPersistence(page, [EPHEMERAL_MARKER]);
  consoleGuard.assertNone();
});

for (const fixture of [
  { station: 'admin' as const, origin: ADMIN_ORIGIN, role: 'admin' as const },
  { station: 'user' as const, origin: USER_ORIGIN, role: 'level4' as const },
]) {
  test(`${fixture.station} production build boots on a deep route`, async ({ context, page }) => {
    const consoleGuard = collectConsoleViolations(page);
    await installURLPersistenceObserver(context, [EPHEMERAL_MARKER]);
    await useNarrowReducedMotion(page);
    await page.addInitScript(() => {
      localStorage.setItem('nb.lang', 'en');
      localStorage.setItem('nb.theme', 'dark');
    });
    await mockPublicConfig(page, fixture.station);
    await mockRoleSession(page, fixture.station, fixture.role);

    await page.goto(`${fixture.origin}/foundation/deep-route`);
    await expect(page.getByRole('heading', { name: 'Page not found' })).toBeVisible();
    await expect(page.locator('html')).toHaveAttribute('lang', 'en');
    await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
    expect(await page.locator('link[rel="stylesheet"]').count()).toBeGreaterThan(0);
    expect(await page.locator('script[type="module"]').count()).toBeGreaterThan(0);

    await assertNoSensitiveBrowserPersistence(page, [EPHEMERAL_MARKER]);
    consoleGuard.assertNone();
  });
}
