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
});
const DEBUG_SESSION_ONE = 'dbg_abcdefghijklmnopqrstuv';
const DEBUG_SESSION_TWO = 'dbg_zyxwvutsrqponmlkjihgfe';
const DEBUG_TRACE_ID = 'debug-body-marker-abcdefghijkl';
const debugReconnectCounts = new Map();

function send(response, status, contentType, body) {
  response.writeHead(status, {
    'cache-control': 'no-store',
    'content-type': contentType,
    'x-content-type-options': 'nosniff',
  });
  response.end(body);
}

function debugMetadata(id, generation, mode, lastEventId, connected = true) {
  return {
    id,
    generation,
    mode,
    created_at: 1,
    expires_at: 3_601,
    idle_expires_at: 601,
    connected,
    last_event_id: lastEventId,
    limits: DEBUG_LIMITS,
  };
}

function debugTrace(mode = 'dry') {
  return {
    request_id: DEBUG_TRACE_ID,
    model: 'fixture/model',
    mode,
    terminal: 'dry_completed',
    received_at: 1,
    route: '/v1/chat/completions',
    raw_request: DEBUG_TRACE_ID,
  };
}

function debugEnvelope({
  id,
  generation,
  seq,
  type,
  mode = 'dry',
  payload,
  traceId = '',
  revision = 0,
}) {
  return {
    version: 1,
    seq,
    type,
    session_id: id,
    trace_id: traceId,
    revision,
    at: seq,
    payload: payload ?? {},
    ...(type === 'session_snapshot'
      ? {
          payload: {
            metadata: debugMetadata(id, generation, mode, seq),
            traces: [debugTrace(mode)],
          },
        }
      : {}),
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
  const writeSnapshot = (id, generation, seq, mode = 'dry') => {
    response.write(
      debugSSEFrame(
        seq,
        'session_snapshot',
        debugEnvelope({ id, generation, seq, type: 'session_snapshot', mode }),
      ),
    );
  };

  if (scenario.startsWith('basic-one-')) {
    writeSnapshot(DEBUG_SESSION_ONE, 1, 1);
    keepOpen();
    return;
  }
  if (scenario.startsWith('basic-two-')) {
    writeSnapshot(DEBUG_SESSION_TWO, 2, 1);
    keepOpen();
    return;
  }
  if (scenario.startsWith('reconnect-')) {
    const count = (debugReconnectCounts.get(scenario) ?? 0) + 1;
    debugReconnectCounts.set(scenario, count);
    const sequence = count === 1 ? 3 : 4;
    writeSnapshot(DEBUG_SESSION_ONE, 1, sequence);
    if (count === 1) {
      closeHeartbeat();
      response.end();
    } else {
      keepOpen();
    }
    return;
  }
  if (scenario.startsWith('gap-')) {
    writeSnapshot(DEBUG_SESSION_ONE, 1, 1);
    response.write(
      debugSSEFrame(
        2,
        'gap',
        debugEnvelope({
          id: DEBUG_SESSION_ONE,
          generation: 1,
          seq: 2,
          type: 'gap',
          payload: { reason: 'resume_gap', dropped: 2 },
        }),
      ),
    );
    keepOpen();
    return;
  }
  if (scenario.startsWith('truncated-')) {
    writeSnapshot(DEBUG_SESSION_ONE, 1, 1);
    response.write(
      debugSSEFrame(
        2,
        'not_trace',
        debugEnvelope({
          id: DEBUG_SESSION_ONE,
          generation: 1,
          seq: 2,
          type: 'trace_upsert',
          traceId: DEBUG_TRACE_ID,
          revision: 1,
          payload: {},
        }),
        true,
      ),
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
