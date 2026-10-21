import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import worker, { createProxy } from './worker.mjs';

const TOKEN = 'test-only-shared-token-0123456789abcdef';
const ENV = { NONBIRI_PROXY_TOKEN: TOKEN };
const WORKER = 'https://relay.example/private-entry';
const TARGET = 'https://api.alpha.example/v1/chat/completions';
const FAILURE = { error: { code: 'proxy_error', message: 'Proxy request failed.' } };

function incoming(target = TARGET, options = {}) {
  const { token = TOKEN, headers = {}, ...init } = options;
  const values = new Headers(headers);
  if (token !== null) values.set('X-Nonbiri-Proxy-Token', token);
  if (target !== null) values.set('X-Nonbiri-Proxy-Target', target);
  const method = init.method ?? 'POST';
  return new Request(WORKER, {
    method,
    body: method === 'GET' || method === 'HEAD' ? undefined : '{"model":"sample"}',
    ...init,
    headers: values,
    duplex: 'half',
  });
}

async function expectFailure(response, kind, status) {
  assert.equal(response.status, status);
  assert.equal(response.headers.get('x-nonbiri-proxy-error'), kind);
  assert.equal(response.headers.get('cache-control'), 'no-store');
  assert.equal(response.headers.get('cdn-cache-control'), 'no-store');
  assert.deepEqual(await response.json(), FAILURE);
}

test('configuration and authentication fail before target parsing or fetch', async () => {
  let calls = 0;
  const proxy = createProxy(() => { calls++; throw new Error('must not fetch'); });
  for (const configured of [undefined, null, '', 'x'.repeat(31), 'x'.repeat(4097), TOKEN + ' ', TOKEN + '\n', 'é'.repeat(32)]) {
    await expectFailure(await proxy.fetch(incoming('invalid'), { NONBIRI_PROXY_TOKEN: configured }), 'configuration', 503);
  }
  for (const provided of [null, '', 'x'.repeat(31), 'x'.repeat(4097), 'wrong-token-0123456789abcdefghijkl', TOKEN + ' value']) {
    await expectFailure(await proxy.fetch(incoming('invalid', { token: provided }), ENV), 'authentication', 401);
  }
  assert.equal(calls, 0);
});

test('visible-ASCII token bounds are accepted without alternate deployment settings', async () => {
  let calls = 0;
  const proxy = createProxy(() => { calls++; return new Response(null, { status: 204 }); });
  for (const token of ['!'.repeat(32), '~'.repeat(4096)]) {
    assert.equal((await proxy.fetch(incoming(TARGET, { token }), { NONBIRI_PROXY_TOKEN: token })).status, 204);
  }
  assert.equal(calls, 2);
  assert.equal(typeof worker.fetch, 'function');
});

test('unsafe targets are rejected without outbound requests', async () => {
  let calls = 0;
  const proxy = createProxy(() => { calls++; return new Response(null, { status: 204 }); });
  for (const target of [
    null, '', '/relative', 'https:api.alpha.example', 'file://api.alpha.example/a',
    'ftp://api.alpha.example/a', 'https://user:password@api.alpha.example/',
    'https://@api.alpha.example/', 'https://api.alpha.example/a#fragment',
    'https://api.alpha.example/a#', 'https://api.alpha.example/a b',
    'https://api.alpha.example/a\tb', 'https://api.alpha.example/a\x7fb',
    'https://api.alpha.example\\private', 'https://localhost/',
    'https://a.localhost./', 'https://service.local/', 'https://metadata.google.internal/',
    'https://private/', 'https://127.0.0.1/', 'https://0177.0.0.1/',
    'https://2130706433/', 'https://0x7f000001/', 'https://%31%32%37.0.0.1/',
    'https://[::1]/', 'https://[::ffff:192.0.2.1]/', 'https://RELAY.EXAMPLE.:8443/',
    'https://bad..example/', `https://${'x'.repeat(64)}.example/`,
    `https://api.alpha.example/${'x'.repeat(8192)}`,
  ]) {
    await expectFailure(await proxy.fetch(incoming(target), ENV), 'target', 400);
  }
  assert.equal(calls, 0);
});

test('target size boundary and arbitrary HTTP DNS endpoints are supported', async () => {
  const seen = [];
  const proxy = createProxy((url) => { seen.push(url); return new Response(null, { status: 204 }); });
  const prefix = 'https://api.alpha.example/';
  const maximum = prefix + 'x'.repeat(8192 - prefix.length);
  for (const target of [maximum, 'http://api.beta.example:8080/v2/models?limit=2', 'https://xn--bcher-kva.example/v1']) {
    assert.equal((await proxy.fetch(incoming(target), ENV)).status, 204);
  }
  assert.equal(seen[0], maximum);
  await expectFailure(await proxy.fetch(incoming(maximum + 'x'), ENV), 'target', 400);
  await expectFailure(await proxy.fetch(incoming(prefix + 'é'.repeat(4096)), ENV), 'target', 400);
  assert.equal(seen.length, 3);
});

test('only the supported HTTP methods are forwarded', async () => {
  const methods = [];
  const proxy = createProxy((url, init) => {
    methods.push(new Request(url, init).method);
    return new Response(null, { status: 204 });
  });
  for (const method of ['GET', 'HEAD', 'POST', 'PUT', 'PATCH', 'DELETE', 'OPTIONS']) {
    assert.equal((await proxy.fetch(incoming(TARGET, { method }), ENV)).status, 204);
  }
  assert.deepEqual(methods, ['GET', 'HEAD', 'POST', 'PUT', 'PATCH', 'DELETE', 'OPTIONS']);
  for (const method of ['CONNECT', 'TRACE', 'PROPFIND']) {
    const request = incoming();
    // Node's Request constructor rejects CONNECT/TRACE before an HTTP handler;
    // supply the ingress method slot while keeping the real Request body/headers.
    Object.defineProperty(request, 'method', { value: method });
    await expectFailure(await proxy.fetch(request, ENV), 'method', 405);
  }
  assert.equal(methods.length, 7);
});

test('two unrelated upstreams preserve paths, queries, authentication, JSON and non-200 status', async () => {
  for (const target of [
    'https://api.alpha.example:8443/v2/a%2Fb?key=a%2Bb&duplicate=1&duplicate=2',
    'https://messages.beta.example/v1/messages?version=2026-01-01',
  ]) {
    let captured;
    let calls = 0;
    const proxy = createProxy(async (url, init) => {
      calls++;
      captured = new Request(url, init);
      return new Response('{"error":{"type":"rate_limit"}}', {
        status: 429, headers: { 'content-type': 'application/json', 'retry-after': '17' },
      });
    });
    const body = '{"model":"sample","messages":[{"content":"你好"}]}';
    const response = await proxy.fetch(incoming(target, { body, headers: {
      authorization: 'Bearer upstream-token', 'x-api-key': 'upstream-key',
      'anthropic-version': '2023-06-01', 'content-type': 'application/json',
    } }), ENV);
    assert.equal(calls, 1);
    assert.equal(captured.url, target);
    assert.equal(await captured.text(), body);
    assert.equal(captured.headers.get('authorization'), 'Bearer upstream-token');
    assert.equal(captured.headers.get('x-api-key'), 'upstream-key');
    assert.equal(captured.headers.get('anthropic-version'), '2023-06-01');
    assert.equal(captured.headers.get('content-type'), 'application/json');
    assert.equal(response.status, 429);
    assert.equal(response.headers.get('retry-after'), '17');
    assert.equal(response.headers.get('x-nonbiri-proxy-error'), null);
    assert.deepEqual(await response.json(), { error: { type: 'rate_limit' } });
  }
});

test('private, hop-by-hop and source IP headers are removed in both directions', async () => {
  const removed = [
    'connection', 'keep-alive', 'proxy-authenticate', 'proxy-authorization',
    'proxy-connection', 'te', 'trailer', 'transfer-encoding', 'upgrade',
    'forwarded', 'x-forwarded-for', 'x-forwarded-proto', 'x-real-ip',
    'cf-connecting-ip', 'cf-connecting-ipv6', 'cf-pseudo-ipv4', 'true-client-ip',
    'client-ip', 'x-client-ip', 'x-original-forwarded-for', 'x-originating-ip',
    'x-cluster-client-ip', 'x-envoy-external-address', 'fastly-client-ip',
    'fly-client-ip', 'via', 'host', 'x-nonbiri-proxy-extra', 'x-nonbiri-proxy-error',
  ];
  const headers = Object.fromEntries(removed.map((name) => [name, 'private-value']));
  headers.connection = 'keep-alive, X-Remove-Me';
  headers['x-remove-me'] = 'connection-private';
  headers['content-type'] = 'application/json';
  const request = incoming(TARGET, { headers });
  Object.defineProperty(request, 'cf', { value: { resolveOverride: 'private.internal', cacheEverything: true } });
  let seen;
  const proxy = createProxy((url, init) => {
    seen = { request: new Request(url, init), init };
    return new Response('{}', { headers: { ...headers, 'x-nonbiri-proxy-token': TOKEN, 'x-nonbiri-proxy-target': TARGET } });
  });
  const response = await proxy.fetch(request, ENV);
  assert.equal(response.status, 200);
  for (const name of [...removed, 'x-remove-me', 'x-nonbiri-proxy-token', 'x-nonbiri-proxy-target']) {
    assert.equal(seen.request.headers.get(name), null, name);
    assert.equal(response.headers.get(name), null, name);
  }
  assert.equal(request.headers.get('cf-connecting-ip'), 'private-value');
  assert.equal(seen.init.signal, request.signal);
  assert.equal(seen.request.redirect, 'manual');
  assert.equal(seen.request.cache, 'no-store');
  assert.deepEqual(seen.init.cf, { cacheEverything: false, cacheTtlByStatus: { '100-599': -1 } });
  assert.equal(response.headers.get('cloudflare-cdn-cache-control'), 'no-store');
  assert.equal(await response.text(), '{}');
});

test('all redirects cancel their body and return a fixed error without another fetch', async () => {
  for (const status of [300, 301, 302, 303, 304, 307, 308, 399]) {
    let calls = 0;
    let canceled = false;
    const body = status === 304 ? null : new ReadableStream({
      cancel() { canceled = true; return Promise.reject(new Error('private cleanup failure')); },
    }, { highWaterMark: 0 });
    const proxy = createProxy(() => {
      calls++;
      return new Response(body, { status, headers: { location: 'https://private.internal/?secret=' + TOKEN } });
    });
    const response = await proxy.fetch(incoming(), ENV);
    await expectFailure(response, 'redirect', 502);
    assert.equal(response.headers.get('location'), null);
    assert.equal(calls, 1);
    assert.equal(canceled, status !== 304);
  }
});

test('network failures reveal no exception text and do not retry', async () => {
  let calls = 0;
  const proxy = createProxy(() => { calls++; throw new Error(TARGET + TOKEN + WORKER); });
  await expectFailure(await proxy.fetch(incoming(), ENV), 'upstream', 502);
  assert.equal(calls, 1);
});

test('SSE remains unbuffered with backpressure and downstream cancellation', { timeout: 2000 }, async () => {
  let pulls = 0;
  let canceled;
  const stream = new ReadableStream({
    pull(controller) { controller.enqueue(new TextEncoder().encode(`data: ${++pulls}\n\n`)); },
    cancel(reason) { canceled = reason; },
  }, { highWaterMark: 0 });
  const proxy = createProxy(() => new Response(stream, { headers: { 'content-type': 'text/event-stream' } }));
  const response = await proxy.fetch(incoming(), ENV);
  assert.equal(response.body, stream);
  assert.equal(pulls, 0);
  const reader = response.body.getReader();
  assert.equal(new TextDecoder().decode((await reader.read()).value), 'data: 1\n\n');
  await Promise.resolve();
  assert.equal(pulls, 1);
  assert.equal(new TextDecoder().decode((await reader.read()).value), 'data: 2\n\n');
  await reader.cancel('downstream closed');
  assert.equal(canceled, 'downstream closed');
  assert.equal(pulls, 2);
});

test('uploads are passed as streams without pre-reading', async () => {
  let pulls = 0;
  const body = new ReadableStream({
    pull(controller) { pulls++; controller.enqueue(new Uint8Array([0, 255, 1])); controller.close(); },
  }, { highWaterMark: 0 });
  const request = incoming(TARGET, { body });
  let passed;
  const proxy = createProxy((url, init) => { passed = init.body; return new Response(null, { status: 204 }); });
  assert.equal((await proxy.fetch(request, ENV)).status, 204);
  assert.equal(pulls, 0);
  assert.equal(passed, request.body);
  assert.deepEqual(new Uint8Array(await new Response(passed).arrayBuffer()), new Uint8Array([0, 255, 1]));
  assert.equal(pulls, 1);
});

test('client abort reaches an in-flight upstream request and pre-abort prevents fetch', { timeout: 2000 }, async () => {
  const started = Promise.withResolvers();
  const abort = new AbortController();
  let calls = 0;
  let upstreamSignal;
  const proxy = createProxy((url, init) => {
    calls++;
    upstreamSignal = new Request(url, init).signal;
    started.resolve();
    return new Promise((resolve, reject) => upstreamSignal.addEventListener('abort', () => reject(upstreamSignal.reason), { once: true }));
  });
  const pending = proxy.fetch(incoming(TARGET, { signal: abort.signal }), ENV);
  await started.promise;
  abort.abort(new Error('private cancellation reason'));
  await expectFailure(await pending, 'upstream', 502);
  assert.equal(upstreamSignal.aborted, true);
  await expectFailure(await proxy.fetch(incoming(TARGET, { signal: abort.signal }), ENV), 'upstream', 502);
  assert.equal(calls, 1);
});

test('an abort after response headers terminates the forwarded stream', { timeout: 2000 }, async () => {
  const abort = new AbortController();
  const proxy = createProxy((url, init) => new Response(new ReadableStream({
    start(controller) {
      init.signal.addEventListener('abort', () => controller.error(new Error('stream aborted')), { once: true });
    },
  }, { highWaterMark: 0 }), { headers: { 'content-type': 'text/event-stream' } }));
  const response = await proxy.fetch(incoming(TARGET, { signal: abort.signal }), ENV);
  const reading = response.body.getReader().read();
  abort.abort();
  await assert.rejects(reading, /stream aborted/);
});

test('deployment retains public-fetch and cancellation flags with logging disabled', async () => {
  const config = JSON.parse(await readFile(new URL('./wrangler.jsonc', import.meta.url), 'utf8'));
  assert.equal(config.main, 'worker.mjs');
  for (const flag of ['global_fetch_strictly_public', 'enable_request_signal', 'request_signal_passthrough']) {
    assert.ok(config.compatibility_flags.includes(flag));
  }
  assert.equal(config.observability.enabled, false);
  assert.equal(config.preview_urls, false);
  for (const binding of ['services', 'vpc_services', 'durable_objects', 'unsafe', 'tail_consumers', 'vars']) {
    assert.equal(config[binding], undefined);
  }
});
