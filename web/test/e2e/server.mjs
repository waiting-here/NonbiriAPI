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

function send(response, status, contentType, body) {
  response.writeHead(status, {
    'cache-control': 'no-store',
    'content-type': contentType,
    'x-content-type-options': 'nosniff',
  });
  response.end(body);
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
