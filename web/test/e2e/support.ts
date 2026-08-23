import { expect, type BrowserContext, type Locator, type Page } from '@playwright/test';
import type { TestRole, TestStation } from '../unit/support';
import { ADMIN_ORIGIN, FIXTURE_ORIGIN, USER_ORIGIN } from './ports';

const JSON_HEADERS = { 'content-type': 'application/json', 'cache-control': 'no-store' };
const ALLOWED_BROWSER_ORIGINS = new Set([ADMIN_ORIGIN, USER_ORIGIN, FIXTURE_ORIGIN]);
const ALLOWED_WEBSOCKET_ORIGINS = new Set(
  [...ALLOWED_BROWSER_ORIGINS].map((origin) => origin.replace(/^http:/, 'ws:')),
);
const URL_OBSERVER_BINDING = '__NONBIRI_REPORT_URL_HIT__';
const URL_OBSERVER_SURFACES = new Set([
  'document',
  'history.pushState',
  'history.replaceState',
  'navigation.popstate',
  'navigation.hashchange',
  'navigation.pageshow',
  'navigation.navigate',
  'navigation.full',
]);

interface URLObservationState {
  readonly forbiddenTokens: readonly string[];
  readonly hitSurfaces: Set<string>;
  readonly observedPages: WeakSet<Page>;
}

const urlObservationStates = new WeakMap<BrowserContext, URLObservationState>();

function normalizeForbiddenTokens(tokens: readonly string[], errorMessage: string): string[] {
  if (tokens.length === 0 || tokens.some((token) => token.length < 8)) {
    throw new Error(errorMessage);
  }
  const normalized = [...new Set(tokens)].sort();
  if (normalized.length !== tokens.length) {
    throw new Error(errorMessage);
  }
  return normalized;
}

interface JsonFixture {
  origin: string;
  method: string;
  path: string;
  body: unknown;
  status?: number;
}

function canonicalMethod(method: string): string {
  const canonical = method.trim().toUpperCase();
  if (!/^[A-Z]+$/.test(canonical)) throw new Error('Fixture methods must be HTTP tokens.');
  return canonical;
}

function exactFixtureURL(origin: string, path: string): URL {
  if (!ALLOWED_BROWSER_ORIGINS.has(origin) || !path.startsWith('/')) {
    throw new Error('Browser fixtures require an allowed origin and absolute path.');
  }
  const parsed = new URL(path, origin);
  if (parsed.origin !== origin || parsed.hash) {
    throw new Error('Browser fixture paths must not contain an origin or fragment.');
  }
  return parsed;
}

export async function mockJson(page: Page, fixture: JsonFixture): Promise<void> {
  const expectedURL = exactFixtureURL(fixture.origin, fixture.path);
  const expectedMethod = canonicalMethod(fixture.method);
  await page.route('**/*', async (route) => {
    const request = route.request();
    const requestURL = new URL(request.url());
    if (
      request.method() !== expectedMethod ||
      requestURL.origin !== expectedURL.origin ||
      requestURL.pathname !== expectedURL.pathname ||
      requestURL.search !== expectedURL.search
    ) {
      await route.fallback();
      return;
    }
    await route.fulfill({
      status: fixture.status ?? 200,
      headers: JSON_HEADERS,
      body: JSON.stringify(fixture.body),
    });
  });
}

export async function installLoopbackNetworkBoundary(context: BrowserContext): Promise<void> {
  await context.route('**/*', async (route) => {
    const requestURL = new URL(route.request().url());
    if (!ALLOWED_BROWSER_ORIGINS.has(requestURL.origin)) {
      await route.abort('blockedbyclient');
      return;
    }
    await route.fallback();
  });
  await context.routeWebSocket('**/*', async (webSocket) => {
    const requestURL = new URL(webSocket.url());
    if (!ALLOWED_WEBSOCKET_ORIGINS.has(requestURL.origin)) {
      await webSocket.close({ code: 1008, reason: 'Blocked by the browser test boundary.' });
      return;
    }
    webSocket.connectToServer();
  });
}

function userSession(role: Exclude<TestRole, 'anonymous' | 'admin'>) {
  const level = role === 'level5' ? 5 : role === 'level4' ? 4 : 1;
  return {
    user: {
      id: '1',
      username: 'fixture-user',
      lang: 'en',
      is_banned: false,
      credits: '0',
      donation_credit: '0',
      effective_level: level,
      created_at: '2026-01-01T00:00:00Z',
    },
  };
}

export async function mockRoleSession(
  page: Page,
  station: TestStation | 'fixture',
  role: TestRole,
): Promise<void> {
  if (station === 'fixture') {
    await mockJson(page, {
      origin: FIXTURE_ORIGIN,
      method: 'GET',
      path: '/fixture/api/session',
      body: { role },
    });
    return;
  }
  const path = station === 'admin' ? '/admin/api/session' : '/api/session';
  const origin = station === 'admin' ? ADMIN_ORIGIN : USER_ORIGIN;
  if (role === 'anonymous') {
    await mockJson(page, {
      origin,
      method: 'GET',
      path,
      status: 401,
      body: { error: { code: 'unauthorized', message: 'Synthetic anonymous session.' } },
    });
    return;
  }
  if (station === 'admin') {
    await mockJson(page, {
      origin,
      method: 'GET',
      path,
      body: { admin: { username: 'fixture-admin' } },
    });
    return;
  }
  if (role === 'admin')
    throw new Error('The user station cannot receive an administrator session.');
  await mockJson(page, { origin, method: 'GET', path, body: userSession(role) });
}

export async function mockPublicConfig(page: Page, station: TestStation): Promise<void> {
  await mockJson(page, {
    origin: station === 'admin' ? ADMIN_ORIGIN : USER_ORIGIN,
    method: 'GET',
    path: station === 'admin' ? '/admin/api/config' : '/api/config',
    body: {
      site_name: 'Fixture Site',
      default_locale: 'en',
      maintenance_mode: false,
      registration_open: false,
    },
  });
}

export async function useNarrowReducedMotion(page: Page): Promise<void> {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.emulateMedia({ reducedMotion: 'reduce' });
}

export async function installURLPersistenceObserver(
  context: BrowserContext,
  forbiddenTokens: readonly string[],
): Promise<void> {
  const normalizedTokens = normalizeForbiddenTokens(
    forbiddenTokens,
    'URL observers require unique synthetic tokens of at least eight characters.',
  );
  if (urlObservationStates.has(context)) {
    throw new Error('A URL persistence observer is already installed for this browser context.');
  }
  if (context.pages().some((existingPage) => existingPage.url() !== 'about:blank')) {
    throw new Error('URL persistence observers must be installed before the first navigation.');
  }

  const state: URLObservationState = {
    forbiddenTokens: normalizedTokens,
    hitSurfaces: new Set<string>(),
    observedPages: new WeakSet<Page>(),
  };
  urlObservationStates.set(context, state);

  const recordCandidate = (surface: string, candidate: string) => {
    if (state.forbiddenTokens.some((token) => candidate.includes(token))) {
      state.hitSurfaces.add(surface);
    }
  };
  const observePage = (observedPage: Page) => {
    if (state.observedPages.has(observedPage)) return;
    state.observedPages.add(observedPage);
    observedPage.on('framenavigated', (frame) => {
      if (frame === observedPage.mainFrame()) {
        recordCandidate('navigation.full', frame.url());
      }
    });
  };
  for (const existingPage of context.pages()) observePage(existingPage);
  context.on('page', observePage);

  await context.exposeBinding(URL_OBSERVER_BINDING, (_source, surface: unknown) => {
    if (typeof surface !== 'string' || !URL_OBSERVER_SURFACES.has(surface)) {
      state.hitSurfaces.add('observer.invalid-surface');
      return;
    }
    state.hitSurfaces.add(surface);
  });
  await context.addInitScript(
    ({ bindingName, tokens }) => {
      const testWindow = window as Window & {
        __NONBIRI_REPORT_URL_HIT__?: (surface: string) => Promise<void>;
        __NONBIRI_URL_OBSERVATION__?: { flush: () => Promise<void> };
      };
      const pendingReports = new Set<Promise<void>>();
      let reportFailed = false;
      const reportSurface = (surface: string) => {
        const report = testWindow.__NONBIRI_REPORT_URL_HIT__;
        if (bindingName !== '__NONBIRI_REPORT_URL_HIT__' || typeof report !== 'function') return;
        const pending = report(surface).catch(() => {
          reportFailed = true;
        });
        pendingReports.add(pending);
        void pending.then(() => pendingReports.delete(pending));
      };
      const observe = (surface: string, candidate: unknown) => {
        const value = typeof candidate === 'string' ? candidate : String(candidate ?? '');
        if (tokens.some((token) => value.includes(token))) reportSurface(surface);
      };
      const observeCurrent = (surface: string) => observe(surface, window.location.href);
      observeCurrent('document');

      for (const method of ['pushState', 'replaceState'] as const) {
        const original = history[method].bind(history);
        history[method] = ((state: unknown, unused: string, url?: string | URL | null) => {
          if (url !== undefined && url !== null) observe(`history.${method}`, url);
          return original(state, unused, url);
        }) as History[typeof method];
      }
      window.addEventListener('popstate', () => observeCurrent('navigation.popstate'));
      window.addEventListener('hashchange', () => observeCurrent('navigation.hashchange'));
      window.addEventListener('pageshow', () => observeCurrent('navigation.pageshow'));
      const navigationAPI = (
        window as Window & {
          navigation?: EventTarget;
        }
      ).navigation;
      navigationAPI?.addEventListener('navigate', (event) => {
        const destination = (event as Event & { destination?: { url?: string } }).destination;
        if (destination?.url) observe('navigation.navigate', destination.url);
      });

      Object.defineProperty(window, '__NONBIRI_URL_OBSERVATION__', {
        configurable: false,
        enumerable: false,
        value: Object.freeze({
          async flush() {
            while (pendingReports.size > 0) {
              await Promise.all([...pendingReports]);
            }
            if (reportFailed) throw new Error('URL surface reporting failed.');
          },
        }),
        writable: false,
      });
    },
    { bindingName: URL_OBSERVER_BINDING, tokens: normalizedTokens },
  );
}

export async function tabTo(page: Page, target: Locator, maximumTabs = 24): Promise<void> {
  for (let index = 0; index < maximumTabs; index += 1) {
    await page.keyboard.press('Tab');
    if (await target.evaluate((element) => element === document.activeElement)) return;
  }
  throw new Error(`Keyboard target was not reached within ${maximumTabs} Tab presses.`);
}

interface ConsoleViolation {
  type: string;
  length: number;
}

export function collectConsoleViolations(page: Page) {
  const violations: ConsoleViolation[] = [];
  const record = (type: string, value: string) => {
    violations.push({
      type,
      length: value.length,
    });
  };
  page.on('console', (message) => {
    if (message.type() === 'error' || message.type() === 'warning') {
      record(message.type(), message.text());
    }
  });
  page.on('pageerror', (error) => record('pageerror', error.message));
  return {
    assertNone() {
      expect(violations, 'console violations are represented only by type and length').toEqual([]);
    },
  };
}

export async function assertNoSensitiveBrowserPersistence(
  page: Page,
  forbiddenTokens: readonly string[],
): Promise<void> {
  const normalizedTokens = normalizeForbiddenTokens(
    forbiddenTokens,
    'Persistence scans require unique synthetic tokens of at least eight characters.',
  );
  const urlObserverReady = await page.evaluate(async () => {
    const testWindow = window as Window & {
      __NONBIRI_URL_OBSERVATION__?: { flush: () => Promise<void> };
    };
    const observer = testWindow.__NONBIRI_URL_OBSERVATION__;
    if (!observer || typeof observer.flush !== 'function') return false;
    await observer.flush();
    return true;
  });
  expect(urlObserverReady, 'URL persistence observer must be installed before navigation').toBe(
    true,
  );

  const urlObservation = urlObservationStates.get(page.context());
  if (!urlObservation) {
    throw new Error('URL persistence observer must be installed before navigation.');
  }
  if (
    normalizedTokens.length !== urlObservation.forbiddenTokens.length ||
    normalizedTokens.some((token, index) => token !== urlObservation.forbiddenTokens[index])
  ) {
    throw new Error('Persistence scan token set does not match the installed URL observer.');
  }
  const scanTokens = urlObservation.forbiddenTokens;
  const cookies = await page.context().cookies([...ALLOWED_BROWSER_ORIGINS]);
  const cookieTokenDetected = cookies.some((cookie) =>
    scanTokens.some((token) => cookie.name.includes(token) || cookie.value.includes(token)),
  );
  const result = await page.evaluate(async (tokens) => {
    const hits = new Set<string>();
    let bytesRead = 0;
    const byteLimit = 256 * 1024;
    const encoder = new TextEncoder();
    const scan = (location: string, value: string) => {
      for (const character of value) {
        bytesRead += encoder.encode(character).byteLength;
        if (bytesRead > byteLimit) return;
      }
      if (tokens.some((token) => value.includes(token))) hits.add(location);
    };

    scan('url:current', location.href);
    for (const [key, value] of Object.entries(localStorage)) {
      scan('localStorage:key', key);
      scan('localStorage:value', value);
    }
    for (const [key, value] of Object.entries(sessionStorage)) {
      scan('sessionStorage:key', key);
      scan('sessionStorage:value', value);
    }

    const indexedDBAvailable =
      typeof indexedDB !== 'undefined' && typeof indexedDB.databases === 'function';
    const databases = indexedDBAvailable ? await indexedDB.databases() : [];
    for (const database of databases) scan('indexedDB:name', database.name ?? '');

    const cacheStorageAvailable = 'caches' in window && typeof caches.keys === 'function';
    let cacheCount = 0;
    if (cacheStorageAvailable) {
      const cacheNames = await caches.keys();
      cacheCount = cacheNames.length;
      for (const cacheName of cacheNames) {
        scan('cache:name', cacheName);
      }
    }

    const serviceWorkerAvailable =
      'serviceWorker' in navigator &&
      typeof navigator.serviceWorker.getRegistrations === 'function';
    const registrations = serviceWorkerAvailable
      ? await navigator.serviceWorker.getRegistrations()
      : [];
    for (const registration of registrations) {
      scan('serviceWorker:scope', registration.scope);
      for (const worker of [registration.installing, registration.waiting, registration.active]) {
        if (worker) scan('serviceWorker:scriptURL', worker.scriptURL);
      }
    }

    return {
      hits: [...hits],
      overflow: bytesRead > byteLimit,
      indexedDBAvailable,
      databaseCount: databases.length,
      cacheStorageAvailable,
      cacheCount,
      serviceWorkerAvailable,
      serviceWorkerCount: registrations.length,
    };
  }, scanTokens);

  expect(result.overflow, 'persistence scan exceeded its fail-closed byte budget').toBe(false);
  expect(cookies.length, 'browser tests must not leave cookies').toBe(0);
  expect(cookieTokenDetected, 'a synthetic ephemeral marker reached a browser cookie').toBe(false);
  expect(result.indexedDBAvailable, 'Chromium must expose indexedDB.databases').toBe(true);
  expect(result.databaseCount, 'browser tests must not leave IndexedDB databases').toBe(0);
  expect(result.cacheStorageAvailable, 'Chromium must expose Cache Storage').toBe(true);
  expect(result.cacheCount, 'browser tests must not leave Cache Storage entries').toBe(0);
  expect(result.serviceWorkerAvailable, 'Chromium must expose service-worker registrations').toBe(
    true,
  );
  expect(result.serviceWorkerCount, 'browser tests must not leave service workers').toBe(0);
  expect([...urlObservation.hitSurfaces], 'a synthetic marker entered URL history').toEqual([]);
  expect(result.hits, 'a synthetic ephemeral marker reached browser persistence').toEqual([]);
}
