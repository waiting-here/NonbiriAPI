import { expect, test } from './test';
import {
  assertNoSensitiveBrowserPersistence,
  collectConsoleViolations,
  installURLPersistenceObserver,
  mockPublicConfig,
  mockRoleSession,
  tabTo,
  useNarrowReducedMotion as configureNarrowReducedMotion,
} from './support';
import { FIXTURE_ORIGIN, USER_ORIGIN } from './ports';

type BrowserContext = Parameters<typeof installURLPersistenceObserver>[0];
type Page = Parameters<typeof collectConsoleViolations>[0];
type RouteHandler = NonNullable<Parameters<Page['route']>[1]>;
type Route = Parameters<RouteHandler>[0];

const SESSION_ONE = 'dbg_abcdefghijklmnopqrstuv';
const SESSION_TWO = 'dbg_zyxwvutsrqponmlkjihgfe';
const TRACE_MARKER = 'debug-body-marker-abcdefghijkl';
const CONFIRMATION_ID = 'confirm_abcdefghijklmnopqrstuv';

const LIMITS = {
  max_sessions: 64,
  hub_bytes: 128,
  session_bytes: 4,
  max_traces: 32,
  max_events: 128,
  event_bytes: 512,
  subscriber_queue: 64,
  max_subscribers: 2,
  raw_request_bytes: 64,
  messages_tools_bytes: 128,
  parameters_bytes: 64,
  effective_summary_bytes: 64,
  response_bytes: 256,
  trace_bytes: 768,
  first_attach_seconds: 30,
  reconnect_seconds: 30,
  idle_seconds: 600,
  absolute_seconds: 3_600,
  heartbeat_seconds: 15,
  write_deadline_seconds: 15,
  confirmation_seconds: 60,
};

type DebugMode = 'dry' | 'live';

interface DebugFixture {
  scenario: string;
  active: boolean;
  id: string;
  generation: number;
  mode: DebugMode;
  connected: boolean;
  lastEventId: number;
  startCalls: number;
  deleteCalls: number;
  challengeCalls: number;
  modeWrites: Array<Record<string, unknown>>;
  eventLastIDs: string[];
  requestURLs: string[];
  eventConnections: number;
}

function metadata(fixture: DebugFixture) {
  return {
    id: fixture.id,
    generation: fixture.generation,
    mode: fixture.mode,
    created_at: 1,
    expires_at: 3_601,
    idle_expires_at: 601,
    connected: fixture.connected,
    last_event_id: fixture.lastEventId,
    limits: LIMITS,
  };
}

function makeFixture(scenario: string, active = false): DebugFixture {
  return {
    scenario,
    active,
    id: SESSION_ONE,
    generation: 1,
    mode: 'dry',
    connected: active,
    lastEventId: active ? 2 : 0,
    startCalls: 0,
    deleteCalls: 0,
    challengeCalls: 0,
    modeWrites: [],
    eventLastIDs: [],
    requestURLs: [],
    eventConnections: 0,
  };
}

async function fulfillJSON(route: Route, value: unknown, status = 200) {
  await route.fulfill({
    status,
    headers: { 'cache-control': 'no-store', 'content-type': 'application/json' },
    body: JSON.stringify(value),
  });
}

async function installDebugFixture(page: Page, fixture: DebugFixture) {
  await page.route('**/*', async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    if (url.origin !== USER_ORIGIN) {
      await route.fallback();
      return;
    }
    fixture.requestURLs.push(request.url());

    if (request.method() === 'GET' && url.pathname === '/api/debug/events') {
      fixture.eventConnections += 1;
      fixture.eventLastIDs.push(request.headers()['last-event-id'] ?? '');
      const scenario = fixture.scenario;
      if (scenario.startsWith('truncated-') && fixture.eventConnections === 1) {
        // Let the first parser failure exercise recovery; the next stream is
        // a stable local stream so the test can inspect the fail-closed view.
        fixture.scenario = 'basic-one-stream';
      }
      await route.continue({
        url: `${FIXTURE_ORIGIN}/fixture/debug-events?case=${scenario}`,
      });
      return;
    }

    if (request.method() === 'GET' && url.pathname === '/api/debug/session') {
      await fulfillJSON(route, fixture.active ? metadata(fixture) : { active: false });
      return;
    }

    if (request.method() === 'POST' && url.pathname === '/api/debug/session') {
      fixture.startCalls += 1;
      fixture.active = true;
      fixture.id = fixture.startCalls === 1 ? SESSION_ONE : SESSION_TWO;
      fixture.generation = fixture.startCalls;
      fixture.mode = 'dry';
      fixture.connected = false;
      fixture.lastEventId = 0;
      if (
        fixture.scenario.startsWith('basic-auto-') ||
        fixture.scenario.startsWith('basic-one-') ||
        fixture.scenario.startsWith('basic-two-')
      ) {
        fixture.scenario = fixture.startCalls === 1 ? 'basic-one-stream' : 'basic-two-stream';
      }
      await fulfillJSON(route, metadata(fixture));
      return;
    }

    if (request.method() === 'DELETE' && url.pathname === '/api/debug/session') {
      fixture.deleteCalls += 1;
      fixture.active = false;
      fixture.connected = false;
      await route.fulfill({ status: 204, headers: { 'cache-control': 'no-store' } });
      return;
    }

    if (request.method() === 'POST' && url.pathname === '/api/debug/session/live-challenge') {
      fixture.challengeCalls += 1;
      await fulfillJSON(route, { confirmation_id: CONFIRMATION_ID });
      return;
    }

    if (request.method() === 'PUT' && url.pathname === '/api/debug/session/mode') {
      const body = (request.postDataJSON() ?? {}) as Record<string, unknown>;
      fixture.modeWrites.push(body);
      if (body.mode === 'live') {
        fixture.mode = 'live';
        fixture.connected = true;
      } else {
        fixture.mode = 'dry';
        fixture.connected = false;
      }
      await fulfillJSON(route, metadata(fixture));
      return;
    }

    await route.fallback();
  });
}

async function prepareDebug(
  context: BrowserContext,
  page: Page,
  locale: 'en' | 'zh',
  theme: 'light' | 'dark',
  fixture: DebugFixture,
) {
  const consoleGuard = collectConsoleViolations(page);
  await installURLPersistenceObserver(context, [
    SESSION_ONE,
    SESSION_TWO,
    TRACE_MARKER,
    CONFIRMATION_ID,
  ]);
  await configureNarrowReducedMotion(page);
  await page.addInitScript(
    ({ language, selectedTheme }) => {
      localStorage.setItem('nb.lang', language);
      localStorage.setItem('nb.theme', selectedTheme);
    },
    { language: locale, selectedTheme: theme },
  );
  await mockPublicConfig(page, 'user');
  await mockRoleSession(page, 'user', 'user');
  await installDebugFixture(page, fixture);
  return consoleGuard;
}

async function assertViewportContract(page: Page) {
  expect(await page.evaluate(() => matchMedia('(prefers-reduced-motion: reduce)').matches)).toBe(
    true,
  );
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true,
  );
}

test('Debug route starts dry, replaces, confirms live, and stops without retaining secrets', async ({
  context,
  page,
}) => {
  const fixture = makeFixture('basic-auto-stream');
  const consoleGuard = await prepareDebug(context, page, 'en', 'light', fixture);

  await page.goto(`${USER_ORIGIN}/debug`);
  await expect(page.getByRole('heading', { name: 'Request debugger' })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'No active debug session' })).toBeVisible();
  const start = page.getByRole('button', { name: 'Start debug session' }).first();
  await tabTo(page, start);
  await expect(start).toBeFocused();
  await start.press('Enter');

  await expect(page.locator('.debug-connection[data-state="connected"]')).toBeVisible();
  await expect(page.getByText('Dry run active').first()).toBeVisible();
  await expect(page.getByText(TRACE_MARKER).first()).toBeVisible();
  await expect(page.getByText(SESSION_ONE).first()).toBeVisible();
  expect(fixture.modeWrites).toEqual([]);

  const stop = page.getByRole('button', { name: 'Stop and clear session' });
  await tabTo(page, stop);
  await expect(stop).toBeFocused();

  await page.getByRole('button', { name: 'Replace session' }).press('Enter');
  await expect(page.getByText(SESSION_TWO).first()).toBeVisible();
  await expect(page.getByText(SESSION_ONE)).toHaveCount(0);
  await expect.poll(() => fixture.eventConnections).toBe(2);
  await expect(page.locator('.debug-connection[data-state="connected"]')).toBeVisible();
  await expect(page.getByText('Dry run active').first()).toBeVisible();

  const liveSwitch = page.getByRole('switch');
  await liveSwitch.focus();
  await page.keyboard.press('Space');
  const dialog = page.getByRole('alertdialog');
  await expect(dialog).toBeVisible();
  const confirm = dialog.getByRole('button', { name: 'Enable actual sending' });
  await confirm.focus();
  await page.keyboard.press('Enter');
  await expect(page.getByText('Actual sending is enabled').first()).toBeVisible();
  await expect(liveSwitch).toHaveAttribute('aria-checked', 'true');
  expect(fixture.challengeCalls).toBe(1);
  expect(fixture.modeWrites).toContainEqual({ mode: 'live', confirmation_id: CONFIRMATION_ID });

  await page.getByRole('button', { name: 'Stop and clear session' }).click();
  await expect(page.getByRole('heading', { name: 'No active debug session' })).toBeVisible();
  expect(fixture.startCalls).toBe(2);
  expect(fixture.deleteCalls).toBe(1);
  expect(
    fixture.requestURLs.every(
      (url) =>
        ![SESSION_ONE, SESSION_TWO, TRACE_MARKER, CONFIRMATION_ID].some((token) =>
          url.includes(token),
        ),
    ),
  ).toBe(true);
  await assertViewportContract(page);
  await assertNoSensitiveBrowserPersistence(page, [
    SESSION_ONE,
    SESSION_TWO,
    TRACE_MARKER,
    CONFIRMATION_ID,
  ]);
  consoleGuard.assertNone();
});

test('Debug reconnect sends Last-Event-ID, then a fresh bounded cursor after stream truncation', async ({
  context,
  page,
}) => {
  const fixture = makeFixture('reconnect-stream', true);
  const consoleGuard = await prepareDebug(context, page, 'en', 'dark', fixture);

  await page.goto(`${USER_ORIGIN}/debug`);
  await expect(page.getByRole('heading', { name: 'Request debugger' })).toBeVisible();
  await expect(page.locator('.debug-connection[data-state="connected"]')).toBeVisible();
  await expect(page.getByText(TRACE_MARKER).first()).toBeVisible();
  await expect.poll(() => fixture.eventLastIDs.length).toBeGreaterThanOrEqual(2);
  // Initial attachment has no cursor; recovery deliberately reopens from a
  // bounded fresh-snapshot cursor after the server-authoritative dry readback.
  expect(fixture.eventLastIDs.slice(0, 2)).toEqual(['', '9007199254740991']);
  await expect(page.getByText('Dry run active').first()).toBeVisible();

  await page.getByRole('button', { name: 'Stop and clear session' }).click();
  await expect(page.getByRole('heading', { name: 'No active debug session' })).toBeVisible();
  expect(fixture.deleteCalls).toBe(1);
  expect(
    fixture.requestURLs.every(
      (url) =>
        ![SESSION_ONE, SESSION_TWO, TRACE_MARKER, CONFIRMATION_ID].some((token) =>
          url.includes(token),
        ),
    ),
  ).toBe(true);
  await assertViewportContract(page);
  await assertNoSensitiveBrowserPersistence(page, [
    SESSION_ONE,
    SESSION_TWO,
    TRACE_MARKER,
    CONFIRMATION_ID,
  ]);
  consoleGuard.assertNone();
});

test('Debug gap and truncated-event recovery stays visibly safe in Chinese dark mode', async ({
  context,
  page,
}) => {
  const fixture = makeFixture('gap-stream');
  const consoleGuard = await prepareDebug(context, page, 'zh', 'dark', fixture);

  await page.goto(`${USER_ORIGIN}/debug`);
  await expect(page.getByRole('heading', { name: '请求调试器' })).toBeVisible();
  await page.getByRole('button', { name: '启动调试会话' }).first().click();
  await expect(page.locator('.debug-connection[data-state="connected"]')).toBeVisible();
  await expect(page.getByText('恢复缺口').first()).toBeVisible();
  await expect(page.getByText('已丢弃 2 个调试副本').first()).toBeVisible();
  await expect(page.getByText('Dry run 已启用').first()).toBeVisible();
  expect(await page.locator('html').getAttribute('lang')).toBe('zh-CN');
  expect(await page.locator('html').getAttribute('data-theme')).toBe('dark');

  await page.getByRole('button', { name: '停止并清空会话' }).click();
  await expect(page.getByText('没有活动调试会话')).toBeVisible();
  await assertViewportContract(page);
  await page.setViewportSize({ width: 780, height: 844 });
  await page.evaluate(() => {
    document.documentElement.style.zoom = '200%';
  });
  await expect(page.getByRole('heading', { name: '请求调试器' })).toBeVisible();
  expect(fixture.eventConnections).toBe(1);
  expect(
    fixture.requestURLs.every(
      (url) =>
        ![SESSION_ONE, SESSION_TWO, TRACE_MARKER, CONFIRMATION_ID].some((token) =>
          url.includes(token),
        ),
    ),
  ).toBe(true);
  await assertNoSensitiveBrowserPersistence(page, [
    SESSION_ONE,
    SESSION_TWO,
    TRACE_MARKER,
    CONFIRMATION_ID,
  ]);
  consoleGuard.assertNone();
});

test('Debug truncated and mismatched SSE event enters safe recovery without browser persistence', async ({
  context,
  page,
}) => {
  const fixture = makeFixture('truncated-stream');
  const consoleGuard = await prepareDebug(context, page, 'en', 'light', fixture);

  await page.goto(`${USER_ORIGIN}/debug`);
  await page.getByRole('button', { name: 'Start debug session' }).first().click();
  await expect(page.getByText('Recovery gap').first()).toBeVisible();
  await expect(page.getByText('Dry run active').first()).toBeVisible();
  await expect.poll(() => fixture.eventConnections).toBeGreaterThanOrEqual(2);
  await page.getByRole('button', { name: 'Stop and clear session' }).click();
  await expect(page.getByRole('heading', { name: 'No active debug session' })).toBeVisible();
  expect(fixture.eventLastIDs[0]).toBe('');
  expect(fixture.eventLastIDs[1]).toBe('9007199254740991');
  expect(
    fixture.requestURLs.every(
      (url) =>
        ![SESSION_ONE, SESSION_TWO, TRACE_MARKER, CONFIRMATION_ID].some((token) =>
          url.includes(token),
        ),
    ),
  ).toBe(true);
  await assertNoSensitiveBrowserPersistence(page, [
    SESSION_ONE,
    SESSION_TWO,
    TRACE_MARKER,
    CONFIRMATION_ID,
  ]);
  consoleGuard.assertNone();
});
