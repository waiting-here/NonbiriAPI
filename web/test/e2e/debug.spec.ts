import { expect, test } from './test';
import { resolve } from 'node:path';
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

const SESSION_ONE = `dbs_${'A'.repeat(22)}`;
const SESSION_TWO = `dbs_${'B'.repeat(21)}Q`;
const TRACE_MARKER = 'debug-body-marker-abcdefghijkl';
const EVENT_ONE = `dbe_${'E'.repeat(21)}A`;

const LIMITS = {
  session_bytes: 4_194_304,
  traces: 32,
  events: 128,
  subscribers: 2,
  event_bytes: 524_288,
  trace_bytes: 786_432,
};

type DebugMode = 'dry' | 'live';

interface DebugFixture {
  embedding?: boolean;
  charity?: boolean;
  scenario: string;
  active: boolean;
  id: string;
  generation: string;
  revision: string;
  mode: DebugMode;
  lastEventId: string | null;
  startCalls: number;
  deleteCalls: number;
  modeWrites: Array<Record<string, unknown>>;
  eventLastIDs: string[];
  requestURLs: string[];
  eventConnections: number;
}

function metadata(fixture: DebugFixture) {
  return {
    active: true,
    id: fixture.id,
    generation: fixture.generation,
    revision: fixture.revision,
    mode: fixture.mode,
    created_at: 1,
    expires_at: 3_601,
    idle_expires_at: 601,
    inflight_count: 0,
    connected_subscribers: 0,
    last_event_id: fixture.lastEventId,
    limits: LIMITS,
  };
}

function makeFixture(scenario: string, active = false): DebugFixture {
  return {
    scenario,
    active,
    id: SESSION_ONE,
    generation: '1',
    revision: '1',
    mode: 'dry',
    lastEventId: active ? EVENT_ONE : null,
    startCalls: 0,
    deleteCalls: 0,
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
        url: `${FIXTURE_ORIGIN}/fixture/debug-events?case=${scenario}&mode=${fixture.mode}&revision=${fixture.revision}&operation=${fixture.embedding ? 'embedding' : 'chat'}&scope=${fixture.charity ? 'charity' : 'self'}`,
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
      fixture.generation = String(fixture.startCalls);
      fixture.revision = '1';
      fixture.mode = 'dry';
      fixture.lastEventId = null;
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

    if (request.method() === 'POST' && url.pathname === '/api/debug/session/stop') {
      fixture.deleteCalls += 1;
      fixture.active = false;
      await route.fulfill({ status: 204, headers: { 'cache-control': 'no-store' } });
      return;
    }

    if (request.method() === 'POST' && url.pathname === '/api/debug/session/replace') {
      fixture.startCalls += 1;
      fixture.active = true;
      fixture.id = SESSION_TWO;
      fixture.generation = String(fixture.startCalls);
      fixture.revision = '1';
      fixture.mode = 'dry';
      fixture.lastEventId = null;
      fixture.scenario = 'basic-two-stream';
      await fulfillJSON(route, metadata(fixture));
      return;
    }

    if (request.method() === 'PUT' && url.pathname === '/api/debug/session/mode') {
      const body = (request.postDataJSON() ?? {}) as Record<string, unknown>;
      fixture.modeWrites.push(body);
      fixture.revision = String(BigInt(fixture.revision) + 1n);
      if (body.mode === 'live') {
        fixture.mode = 'live';
      } else {
        fixture.mode = 'dry';
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
  await installURLPersistenceObserver(context, [SESSION_ONE, SESSION_TWO, TRACE_MARKER,
    ...(fixture.embedding ? ['caller-supplied'] : [])]);
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

async function confirmSessionAction(page: Page, trigger: string, confirm: string) {
  await page.getByRole('button', { name: trigger }).first().click();
  const dialog = page.getByRole('alertdialog');
  await expect(dialog).toBeVisible();
  await dialog.getByRole('button', { name: confirm }).click();
}

async function revealTraceMarker(page: Page, summary: RegExp) {
  const trace = page.locator('.ops-debug-trace').first();
  await trace.locator('details').first().locator('summary').first().click();
  await trace.getByText(summary).click();
  await expect(trace.locator('pre.ops-debug-json')).toContainText(TRACE_MARKER);
}

for (const locale of ['en', 'zh'] as const)
  for (const theme of ['light', 'dark'] as const)
    for (const width of [390, 1280])
      test(`Embedding Debug keeps private input in memory at ${locale} ${theme} ${width}`, async ({
        context,
        page,
      }) => {
        const fixture = makeFixture('basic-one-stream', true);
        fixture.embedding = true;
        fixture.charity = width === 390;
        fixture.mode = theme === 'dark' ? 'live' : 'dry';
        const guard = await prepareDebug(context, page, locale, theme, fixture);
        await page.setViewportSize({ width, height: 900 });
        await page.goto(`${USER_ORIGIN}/debug`);
        await expect(page.locator('p').filter({ hasText: 'debug_live_cancelled' })).toContainText('(409)');
        const label =
          locale === 'zh'
            ? fixture.charity
              ? '公益向量嵌入'
              : '自用向量嵌入'
            : fixture.charity
              ? 'Charity embedding'
              : 'Personal embedding';
        const trace = page.locator('.ops-debug-trace').first();
        await expect(trace.locator('summary').first()).toContainText(label);
        await trace.locator('summary').first().click();
        const fields = trace.locator('.ops-debug-presence').first();
        await expect(fields.locator('dt')).toHaveText([
          'model',
          'input',
          'encoding_format',
          'dimensions',
          'user',
        ]);
        await expect(fields).toContainText('base64');
        await expect(fields).toContainText('caller-supplied');
        await expect(trace).toContainText(
          fixture.mode === 'live' ? 'debug_live_result_captured' : 'debug_dry_run_intercepted',
        );
        await expect(trace.locator('textarea')).toHaveCount(0);
        await expect(page.locator('html')).toHaveAttribute('data-theme', theme);
        await expect(page.locator('html')).toHaveAttribute(
          'lang',
          locale === 'zh' ? 'zh-CN' : 'en',
        );
        await assertViewportContract(page);
        if (process.env.NONBIRI_VISUAL_DIR)
          await trace.screenshot({
            path: resolve(
              process.env.NONBIRI_VISUAL_DIR,
              `embedding-debug-${locale}-${theme}-${width}.png`,
            ),
          });
        const stop = locale === 'zh' ? '停止会话' : 'Stop session';
        await confirmSessionAction(page, stop, stop);
        await expect(page.locator('.ops-debug-trace')).toHaveCount(0);
        await expect(page.getByText('caller-supplied')).toHaveCount(0);
        await assertNoSensitiveBrowserPersistence(page, [
          SESSION_ONE,
          SESSION_TWO,
          TRACE_MARKER,
          'caller-supplied',
        ]);
        guard.assertNone();
      });

test('Debug route starts dry, replaces, confirms live, and stops without retaining secrets', async ({
  context,
  page,
}) => {
  const fixture = makeFixture('basic-auto-stream');
  const consoleGuard = await prepareDebug(context, page, 'en', 'light', fixture);

  await page.goto(`${USER_ORIGIN}/debug`);
  await expect(page.getByRole('heading', { name: 'Debug', exact: true })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'No active Debug session' })).toBeVisible();
  const start = page.getByRole('button', { name: 'Start Debug' }).first();
  await tabTo(page, start);
  await expect(start).toBeFocused();
  await start.press('Enter');

  const connection = page.locator('.card').filter({
    has: page.getByRole('heading', { name: 'Connection and mode' }),
  });
  await expect(connection.getByText('Connected', { exact: true })).toBeVisible();
  await expect(connection.getByText('Preview (Dry, not sent)', { exact: true })).toBeVisible();
  await revealTraceMarker(page, /Raw request body/);
  await expect(page.getByText(SESSION_ONE)).toHaveCount(0);
  expect(fixture.modeWrites).toEqual([]);

  const stop = page.getByRole('button', { name: 'Stop session' }).first();
  await tabTo(page, stop);
  await expect(stop).toBeFocused();

  await page.getByRole('button', { name: 'Start over' }).press('Enter');
  const replaceDialog = page.getByRole('alertdialog');
  await expect(replaceDialog).toBeVisible();
  await replaceDialog.getByRole('button', { name: 'Replace session' }).press('Enter');
  await expect(page.getByRole('heading', { name: 'Connection and mode' })).toBeVisible();
  await expect(page.getByText(SESSION_TWO)).toHaveCount(0);
  await expect(page.getByText(SESSION_ONE)).toHaveCount(0);
  await expect.poll(() => fixture.eventConnections).toBe(2);
  await expect(connection.getByText('Connected', { exact: true })).toBeVisible();
  await expect(connection.getByText('Preview (Dry, not sent)', { exact: true })).toBeVisible();

  const live = page.getByRole('button', { name: 'Enable live mode' });
  await live.focus();
  await page.keyboard.press('Space');
  const dialog = page.getByRole('alertdialog');
  await expect(dialog).toBeVisible();
  const confirm = dialog.getByRole('button', { name: 'Enable live' });
  await confirm.focus();
  await page.keyboard.press('Enter');
  await expect(connection.getByText('Real send (Live)', { exact: true })).toBeVisible();
  expect(fixture.modeWrites).toContainEqual({
    mode: 'live',
    expected_revision: '1',
    live_confirmation: true,
  });

  await confirmSessionAction(page, 'Stop session', 'Stop session');
  await expect(page.getByRole('heading', { name: 'No active Debug session' })).toBeVisible();
  expect(fixture.startCalls).toBe(2);
  expect(fixture.deleteCalls).toBe(1);
  expect(
    fixture.requestURLs.every(
      (url) => ![SESSION_ONE, SESSION_TWO, TRACE_MARKER].some((token) => url.includes(token)),
    ),
  ).toBe(true);
  await assertViewportContract(page);
  await assertNoSensitiveBrowserPersistence(page, [SESSION_ONE, SESSION_TWO, TRACE_MARKER]);
  consoleGuard.assertNone();
});

test('Debug reconnect accepts a fresh bounded snapshot after stream closure', async ({
  context,
  page,
}) => {
  const fixture = makeFixture('reconnect-stream', true);
  const consoleGuard = await prepareDebug(context, page, 'en', 'dark', fixture);

  await page.goto(`${USER_ORIGIN}/debug`);
  await expect(page.getByRole('heading', { name: 'Debug', exact: true })).toBeVisible();
  const connection = page.locator('.card').filter({
    has: page.getByRole('heading', { name: 'Connection and mode' }),
  });
  await expect(connection.getByText('Connected', { exact: true })).toBeVisible();
  await revealTraceMarker(page, /Raw request body/);
  await expect.poll(() => fixture.eventLastIDs.length).toBeGreaterThanOrEqual(2);
  expect(fixture.eventLastIDs[0]).toBe('');
  await expect(connection.getByText('Preview (Dry, not sent)', { exact: true })).toBeVisible();

  await confirmSessionAction(page, 'Stop session', 'Stop session');
  await expect(page.getByRole('heading', { name: 'No active Debug session' })).toBeVisible();
  expect(fixture.deleteCalls).toBe(1);
  expect(
    fixture.requestURLs.every(
      (url) => ![SESSION_ONE, SESSION_TWO, TRACE_MARKER].some((token) => url.includes(token)),
    ),
  ).toBe(true);
  await assertViewportContract(page);
  await assertNoSensitiveBrowserPersistence(page, [SESSION_ONE, SESSION_TWO, TRACE_MARKER]);
  consoleGuard.assertNone();
});

test('Debug gap and truncated-event recovery stays visibly safe in Chinese dark mode', async ({
  context,
  page,
}) => {
  const fixture = makeFixture('gap-stream');
  const consoleGuard = await prepareDebug(context, page, 'zh', 'dark', fixture);

  await page.goto(`${USER_ORIGIN}/debug`);
  await expect(page.getByRole('heading', { name: '调试', exact: true })).toBeVisible();
  await page.getByRole('button', { name: '开始调试' }).first().click();
  const connection = page.locator('.card').filter({
    has: page.getByRole('heading', { name: '连接与模式' }),
  });
  await expect(connection.getByText('已连接', { exact: true })).toBeVisible();
  await expect(connection.getByText(/实时更新出现中断：更新记录已清除/)).toBeVisible();
  await expect(connection.getByText('预览（Dry，不发送）', { exact: true })).toBeVisible();
  expect(await page.locator('html').getAttribute('lang')).toBe('zh-CN');
  expect(await page.locator('html').getAttribute('data-theme')).toBe('dark');

  await confirmSessionAction(page, '停止会话', '停止会话');
  await expect(page.getByText('没有进行中的调试会话')).toBeVisible();
  await assertViewportContract(page);
  await page.setViewportSize({ width: 780, height: 844 });
  await page.evaluate(() => {
    document.documentElement.style.zoom = '200%';
  });
  await expect(page.getByRole('heading', { name: '调试', exact: true })).toBeVisible();
  expect(fixture.eventConnections).toBe(1);
  expect(
    fixture.requestURLs.every(
      (url) => ![SESSION_ONE, SESSION_TWO, TRACE_MARKER].some((token) => url.includes(token)),
    ),
  ).toBe(true);
  await assertNoSensitiveBrowserPersistence(page, [SESSION_ONE, SESSION_TWO, TRACE_MARKER]);
  consoleGuard.assertNone();
});

test('Debug mismatched SSE event requires explicit safe recovery without browser persistence', async ({
  context,
  page,
}) => {
  const fixture = makeFixture('truncated-stream');
  const consoleGuard = await prepareDebug(context, page, 'en', 'light', fixture);

  await page.goto(`${USER_ORIGIN}/debug`);
  await page.getByRole('button', { name: 'Start Debug' }).first().click();
  await expect(page.getByRole('alert')).toContainText('The service returned an invalid response.');
  await page.getByRole('button', { name: 'Retry' }).click();
  const connection = page.locator('.card').filter({
    has: page.getByRole('heading', { name: 'Connection and mode' }),
  });
  await expect(connection.getByText('Connected', { exact: true })).toBeVisible();
  await expect(connection.getByText('Preview (Dry, not sent)', { exact: true })).toBeVisible();
  await expect.poll(() => fixture.eventConnections).toBeGreaterThanOrEqual(2);
  await confirmSessionAction(page, 'Stop session', 'Stop session');
  await expect(page.getByRole('heading', { name: 'No active Debug session' })).toBeVisible();
  expect(fixture.eventLastIDs[0]).toBe('');
  expect(
    fixture.requestURLs.every(
      (url) => ![SESSION_ONE, SESSION_TWO, TRACE_MARKER].some((token) => url.includes(token)),
    ),
  ).toBe(true);
  await assertNoSensitiveBrowserPersistence(page, [SESSION_ONE, SESSION_TWO, TRACE_MARKER]);
  consoleGuard.assertNone();
});
