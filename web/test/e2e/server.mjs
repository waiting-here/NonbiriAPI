import { createServer } from 'node:http';
import { readFile, realpath, stat } from 'node:fs/promises';
import { dirname, extname, resolve, sep } from 'node:path';
import { fileURLToPath } from 'node:url';

const webRoot = resolve(dirname(fileURLToPath(import.meta.url)), '../..');
const MIME = new Map([
  ['.css', 'text/css; charset=utf-8'],
  ['.html', 'text/html; charset=utf-8'],
  ['.js', 'text/javascript; charset=utf-8'],
  ['.json', 'application/json; charset=utf-8'],
  ['.svg', 'image/svg+xml'],
]);

const DEBUG_LIMITS = Object.freeze({
  session_bytes: 4_194_304,
  traces: 32,
  events: 128,
  subscribers: 2,
  event_bytes: 524_288,
  trace_bytes: 786_432,
});
const DEBUG_SESSION_ONE = `dbs_${'A'.repeat(22)}`;
const DEBUG_SESSION_TWO = `dbs_${'B'.repeat(21)}Q`;
const DEBUG_TRACE_ID = `dbt_${'T'.repeat(21)}A`;
const DEBUG_TRACE_MARKER = 'debug-body-marker-abcdefghijkl';
const DEBUG_EVENT_ONE = `dbe_${'E'.repeat(21)}A`;
const DEBUG_EVENT_TWO = `dbe_${'F'.repeat(21)}Q`;
const DEBUG_EVENT_THREE = `dbe_${'G'.repeat(21)}g`;
const debugReconnectCounts = new Map();

function send(response, status, contentType, body) {
  response.writeHead(status, {
    'cache-control': 'no-store',
    'content-type': contentType,
    'x-content-type-options': 'nosniff',
  });
  response.end(body);
}

function debugMetadata(id, generation, revision, mode, lastEventId) {
  return {
    active: true,
    id,
    generation: String(generation),
    revision: String(revision),
    mode,
    created_at: 1,
    expires_at: 3_601,
    idle_expires_at: 601,
    inflight_count: 0,
    connected_subscribers: 1,
    last_event_id: lastEventId,
    limits: DEBUG_LIMITS,
  };
}

function debugTrace() {
  const body = JSON.stringify({ marker: DEBUG_TRACE_MARKER, stream: false });
  return {
    trace_id: DEBUG_TRACE_ID,
    revision: '1',
    state: 'terminal',
    request: {
      route_kind: 'openai_chat_completions',
      model: 'fixture/model',
      stream: false,
      body: {
        media_type: 'application/json',
        byte_count: Buffer.byteLength(body),
        text: body,
        base64: null,
        truncated: false,
      },
    },
    upstream_result: null,
    caller_result: {
      http_status: 422,
      error_code: 'debug_dry_run_intercepted',
      source: 'platform',
      message: '[NonbiriAPI] Debug Dry request intercepted.',
      completed_at: 2,
    },
    created_at: 1,
    updated_at: 2,
    truncated: false,
  };
}

function debugEvent({
  eventId,
  sessionId,
  generation,
  kind,
  data,
}) {
  return {
    version: 2,
    event_id: eventId,
    session_id: sessionId,
    generation: String(generation),
    kind,
    occurred_at: 2,
    data,
  };
}

function debugSSEFrame(id, eventName, body, omitTerminator = false) {
  const frame = `id: ${id}\nevent: ${eventName}\ndata: ${JSON.stringify(body)}\n`;
  return omitTerminator ? frame : `${frame}\n`;
}

function debugSSEHeaders(request) {
  const origin = typeof request.headers.origin === 'string' ? request.headers.origin : 'null';
  return {
    'cache-control': 'no-store, no-cache',
    'content-type': 'text/event-stream; charset=utf-8',
    'access-control-allow-origin': origin,
    'access-control-allow-credentials': 'true',
    'x-content-type-options': 'nosniff',
    connection: 'keep-alive',
  };
}

function serveDebugEvents(request, response) {
  const url = new URL(request.url ?? '/', 'http://127.0.0.1');
  const scenario = url.searchParams.get('case') ?? '';
  if (!/^[a-z0-9-]{1,64}$/.test(scenario)) {
    send(response, 400, 'application/json; charset=utf-8', '{"error":"invalid_fixture_case"}');
    return;
  }
  const mode = url.searchParams.get('mode') === 'live' ? 'live' : 'dry';
  const revision = /^(0|[1-9][0-9]*)$/.test(url.searchParams.get('revision') ?? '')
    ? url.searchParams.get('revision')
    : '1';

  response.writeHead(200, debugSSEHeaders(request));
  const heartbeat = setInterval(() => {
    if (!response.writableEnded) response.write(': fixture-heartbeat\n\n');
  }, 500);
  const closeHeartbeat = () => clearInterval(heartbeat);
  request.once('close', closeHeartbeat);
  response.once('close', closeHeartbeat);

  const keepOpen = () => {
    if (!response.writableEnded) response.write(': fixture-ready\n\n');
  };
  const writeSnapshot = (id, generation, eventId) => {
    const session = debugMetadata(id, generation, revision, mode, eventId);
    const event = debugEvent({
      eventId,
      sessionId: id,
      generation,
      kind: 'snapshot',
      data: {
        session,
        traces: [debugTrace()],
        first_event_id: eventId,
        last_event_id: eventId,
      },
    });
    response.write(
      debugSSEFrame(eventId, 'snapshot', event),
    );
  };

  if (scenario.startsWith('basic-one-')) {
    writeSnapshot(DEBUG_SESSION_ONE, 1, DEBUG_EVENT_ONE);
    keepOpen();
    return;
  }
  if (scenario.startsWith('basic-two-')) {
    writeSnapshot(DEBUG_SESSION_TWO, 2, DEBUG_EVENT_ONE);
    keepOpen();
    return;
  }
  if (scenario.startsWith('reconnect-')) {
    const count = (debugReconnectCounts.get(scenario) ?? 0) + 1;
    debugReconnectCounts.set(scenario, count);
    const eventId = count === 1 ? DEBUG_EVENT_ONE : DEBUG_EVENT_TWO;
    writeSnapshot(DEBUG_SESSION_ONE, 1, eventId);
    if (count === 1) {
      closeHeartbeat();
      response.end();
    } else {
      keepOpen();
    }
    return;
  }
  if (scenario.startsWith('gap-')) {
    writeSnapshot(DEBUG_SESSION_ONE, 1, DEBUG_EVENT_ONE);
    const gap = debugEvent({
      eventId: DEBUG_EVENT_TWO,
      sessionId: DEBUG_SESSION_ONE,
      generation: 1,
      kind: 'gap',
      data: { reason: 'ring_evicted', first_available_event_id: DEBUG_EVENT_ONE },
    });
    response.write(
      debugSSEFrame(DEBUG_EVENT_TWO, 'gap', gap),
    );
    keepOpen();
    return;
  }
  if (scenario.startsWith('truncated-')) {
    writeSnapshot(DEBUG_SESSION_ONE, 1, DEBUG_EVENT_ONE);
    const mismatched = debugEvent({
      eventId: DEBUG_EVENT_TWO,
      sessionId: DEBUG_SESSION_ONE,
      generation: 1,
      kind: 'trace_upsert',
      data: debugTrace(),
    });
    response.write(
      debugSSEFrame(DEBUG_EVENT_THREE, 'trace_upsert', mismatched),
    );
    closeHeartbeat();
    response.end();
    return;
  }
  closeHeartbeat();
  response.end();
}

function safeCandidate(root, pathname) {
  let decoded;
  try {
    decoded = decodeURIComponent(pathname);
  } catch {
    return null;
  }
  const candidate = resolve(root, `.${decoded}`);
  return candidate === root || candidate.startsWith(`${root}${sep}`) ? candidate : null;
}

function isWithin(root, candidate) {
  return candidate === root || candidate.startsWith(`${root}${sep}`);
}

function isAPIPath(pathname) {
  return (
    pathname === '/api' ||
    pathname.startsWith('/api/') ||
    pathname === '/admin/api' ||
    pathname.startsWith('/admin/api/')
  );
}

async function realStationFile(root, pathname) {
  const lexicalCandidate = safeCandidate(root, pathname);
  if (!lexicalCandidate) return null;
  try {
    let candidate = lexicalCandidate;
    const candidateStat = await stat(candidate);
    if (candidateStat.isDirectory()) candidate = resolve(candidate, 'index.html');
    const realCandidate = await realpath(candidate);
    if (!isWithin(root, realCandidate) || !(await stat(realCandidate)).isFile()) return null;
    return realCandidate;
  } catch {
    return null;
  }
}

async function serveStation(request, response, station) {
  if (request.method !== 'GET' && request.method !== 'HEAD') {
    send(response, 405, 'application/json; charset=utf-8', '{"error":"method_not_allowed"}');
    return;
  }
  const url = new URL(request.url ?? '/', 'http://127.0.0.1');
  if (isAPIPath(url.pathname)) {
    send(response, 404, 'application/json; charset=utf-8', '{"error":"unmocked_test_api"}');
    return;
  }

  try {
    const root = await realpath(resolve(webRoot, 'dist', station));
    let candidate = await realStationFile(root, url.pathname);
    if (!candidate) candidate = await realStationFile(root, '/index.html');
    if (!candidate) throw new Error('Station index is unavailable.');
    const body = await readFile(candidate);
    const contentType = MIME.get(extname(candidate)) ?? 'application/octet-stream';
    send(response, 200, contentType, request.method === 'HEAD' ? '' : body);
  } catch {
    send(
      response,
      500,
      'text/plain; charset=utf-8',
      'The browser fixture requires a completed web build.',
    );
  }
}

const fixtureDocument = `<!doctype html>
<html lang="en" data-theme="light">
  <head>
    <meta charset="UTF-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <title>Browser test fixture</title>
    <style>
      body { box-sizing: border-box; max-width: 48rem; margin: 0 auto; padding: 1rem; font-family: sans-serif; }
      button, a { margin: 0.5rem; padding: 0.75rem; }
      @media (prefers-reduced-motion: reduce) { body { scroll-behavior: auto; } }
    </style>
  </head>
  <body>
    <main>
      <h1>Browser test fixture</h1>
      <p id="route"></p>
      <p id="role" role="status">anonymous</p>
      <a id="route-link" href="/fixture/route/next">Next fixture route</a>
      <button id="api-button" type="button">Load fixture API</button>
      <p id="api-result" role="status">idle</p>
    </main>
    <script>
      const parameters = new URLSearchParams(location.search);
      const locale = parameters.get('locale') === 'zh' ? 'zh-CN' : 'en';
      const theme = parameters.get('theme') === 'dark' ? 'dark' : 'light';
      document.documentElement.lang = locale;
      document.documentElement.dataset.theme = theme;
      document.querySelector('#route').textContent = location.pathname;

      fetch('/fixture/api/session', { cache: 'no-store' })
        .then((response) => response.json())
        .then((payload) => { document.querySelector('#role').textContent = payload.role ?? 'anonymous'; })
        .catch(() => { document.querySelector('#role').textContent = 'session-error'; });

      document.querySelector('#route-link').addEventListener('click', (event) => {
        event.preventDefault();
        history.pushState({}, '', event.currentTarget.href);
        document.querySelector('#route').textContent = location.pathname;
      });
      document.querySelector('#api-button').addEventListener('click', async () => {
        const response = await fetch('/fixture/api/status', { cache: 'no-store' });
        const payload = await response.json();
        document.querySelector('#api-result').textContent = payload.error?.code
          ?? (response.ok ? 'ok' : 'fixture_error');
      });
    </script>
  </body>
</html>`;

function serveFixture(request, response) {
  if (request.method !== 'GET' && request.method !== 'HEAD') {
    send(response, 405, 'application/json; charset=utf-8', '{"error":"method_not_allowed"}');
    return;
  }
  const url = new URL(request.url ?? '/', 'http://127.0.0.1');
  if (url.pathname === '/health') {
    send(response, 200, 'text/plain; charset=utf-8', request.method === 'HEAD' ? '' : 'ok');
    return;
  }
  if (url.pathname === '/fixture/debug-events') {
    serveDebugEvents(request, response);
    return;
  }
  if (url.pathname.startsWith('/fixture/api/')) {
    send(response, 404, 'application/json; charset=utf-8', '{"error":"unmocked_test_api"}');
    return;
  }
  send(response, 200, 'text/html; charset=utf-8', request.method === 'HEAD' ? '' : fixtureDocument);
}

async function closeServers(servers) {
  await Promise.all(
    servers.map(
      (server) =>
        new Promise((resolveClose) => {
          if (!server.listening) {
            resolveClose();
            return;
          }
          server.close(resolveClose);
          server.closeAllConnections();
        }),
    ),
  );
}

export async function startFixtureServers(rawPorts) {
  if (
    rawPorts.length !== 3 ||
    rawPorts.some((port) => !Number.isInteger(port) || port < 1_024 || port > 65_535)
  ) {
    throw new Error('Expected three distinct test ports from 1024 through 65535.');
  }
  if (new Set(rawPorts).size !== rawPorts.length) {
    throw new Error('Browser fixture ports must be distinct.');
  }

  const servers = [
    createServer((request, response) => void serveStation(request, response, 'admin')),
    createServer((request, response) => void serveStation(request, response, 'user')),
    createServer(serveFixture),
  ];
  try {
    await Promise.all(
      servers.map(
        (server, index) =>
          new Promise((resolveListen, rejectListen) => {
            const reject = (error) => rejectListen(error);
            server.once('error', reject);
            server.listen(rawPorts[index], '127.0.0.1', () => {
              server.off('error', reject);
              resolveListen();
            });
          }),
      ),
    );
  } catch (error) {
    await closeServers(servers);
    throw error;
  }
  process.stdout.write('browser fixtures ready\n');

  return async function closeFixtureServers() {
    await closeServers(servers);
  };
}

const directInvocation = process.argv[1]
  ? resolve(process.argv[1]) === resolve(fileURLToPath(import.meta.url))
  : false;
if (directInvocation) {
  const close = await startFixtureServers(process.argv.slice(2).map(Number));
  const shutdown = () => void close().then(() => process.exit(0));
  process.once('SIGINT', shutdown);
  process.once('SIGTERM', shutdown);
}
