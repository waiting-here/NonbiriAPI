const PRIVATE_PREFIX = 'x-nonbiri-proxy-';
const METHODS = new Set(['GET', 'HEAD', 'POST', 'PUT', 'PATCH', 'DELETE', 'OPTIONS']);
const HOP_HEADERS = new Set([
  'connection', 'keep-alive', 'proxy-authenticate', 'proxy-authorization',
  'proxy-connection', 'te', 'trailer', 'transfer-encoding', 'upgrade',
]);
const SOURCE_HEADERS = new Set([
  'forwarded', 'via', 'x-real-ip', 'true-client-ip', 'client-ip', 'x-client-ip',
  'x-originating-ip', 'x-cluster-client-ip', 'x-original-forwarded-for',
  'x-envoy-external-address', 'fastly-client-ip', 'fly-client-ip',
  'cf-connecting-ip', 'cf-connecting-ipv6', 'cf-pseudo-ipv4',
]);
const FAILURE_BODY = '{"error":{"code":"proxy_error","message":"Proxy request failed."}}';
const encoder = new TextEncoder();

function validToken(value) {
  return typeof value === 'string' && value.length >= 32 && value.length <= 4096 &&
    !/[^\x21-\x7e]/.test(value);
}

async function tokensMatch(provided, configured) {
  const digests = await Promise.all([provided, configured].map(
    (value) => crypto.subtle.digest('SHA-256', encoder.encode(value)),
  ));
  const left = new Uint8Array(digests[0]);
  const right = new Uint8Array(digests[1]);
  let difference = 0;
  for (let i = 0; i < left.length; i++) difference |= left[i] ^ right[i];
  return difference === 0;
}

function noStore(headers) {
  headers.set('cache-control', 'no-store');
  headers.set('cdn-cache-control', 'no-store');
  headers.set('cloudflare-cdn-cache-control', 'no-store');
  return headers;
}

function failure(kind, status) {
  return new Response(FAILURE_BODY, {
    status,
    headers: noStore(new Headers({
      'content-type': 'application/json; charset=utf-8',
      'x-nonbiri-proxy-error': kind,
    })),
  });
}

function targetURL(raw, incomingURL) {
  if (typeof raw !== 'string' || raw.length > 8192 || encoder.encode(raw).length > 8192 ||
      /[\x00-\x20\x7f-\x9f\\]/.test(raw) || raw.includes('#')) return null;
  const authority = /^https?:\/\/([^/?#]*)/i.exec(raw)?.[1];
  if (!authority || authority.includes('@')) return null;
  try {
    const target = new URL(raw);
    if (!['http:', 'https:'].includes(target.protocol) || target.username || target.password) return null;
    const host = target.hostname.toLowerCase().replace(/\.$/, '');
    const ownHost = new URL(incomingURL).hostname.toLowerCase().replace(/\.$/, '');
    const labels = host.split('.');
    if (host === ownHost || host.length > 253 || labels.length < 2 ||
        /^(?:\d+\.){3}\d+$/.test(host) ||
        labels.some((label) => !/^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/.test(label)) ||
        ['localhost', 'local', 'internal'].some((suffix) => host === suffix || host.endsWith(`.${suffix}`))) {
      return null;
    }
    return target;
  } catch {
    return null;
  }
}

function cleanHeaders(original) {
  const headers = new Headers(original);
  for (const name of (headers.get('connection') ?? '').split(',')) {
    const token = name.trim();
    if (/^[!#$%&'*+.^_`|~0-9A-Za-z-]+$/.test(token)) headers.delete(token);
  }
  for (const name of [...headers.keys()]) {
    const lower = name.toLowerCase();
    if (lower.startsWith(PRIVATE_PREFIX) || lower.startsWith('x-forwarded-') ||
        lower === 'host' || HOP_HEADERS.has(lower) || SOURCE_HEADERS.has(lower)) {
      headers.delete(name);
    }
  }
  return headers;
}

function cancelBody(response) {
  // Start cancellation without delaying the fixed response on cleanup I/O.
  response?.body?.cancel().catch(() => {});
}

// Deployment uses global public fetch. Tests inject only the network operation;
// validation and forwarding follow the same path without a bypass setting.
export function createProxy(fetchUpstream = globalThis.fetch) {
  return {
    async fetch(request, env) {
      const configured = env?.NONBIRI_PROXY_TOKEN;
      if (!validToken(configured)) return failure('configuration', 503);
      let upstream;
      try {
        const provided = request.headers.get('x-nonbiri-proxy-token');
        if (!validToken(provided) || !await tokensMatch(provided, configured)) {
          return failure('authentication', 401);
        }
        if (!METHODS.has(request.method)) return failure('method', 405);
        const target = targetURL(request.headers.get('x-nonbiri-proxy-target'), request.url);
        if (!target) return failure('target', 400);
        if (request.signal.aborted) return failure('upstream', 502);
        const headers = cleanHeaders(request.headers);
        headers.set('cache-control', 'no-store');
        headers.set('pragma', 'no-cache');
        upstream = await fetchUpstream(target.href, {
          method: request.method,
          headers,
          body: request.method === 'GET' || request.method === 'HEAD' ? undefined : request.body,
          duplex: 'half',
          redirect: 'manual',
          cache: 'no-store',
          signal: request.signal,
          // Never copy request.cf: it can contain origin or cache overrides.
          cf: { cacheEverything: false, cacheTtlByStatus: { '100-599': -1 } },
        });
        if (upstream.status >= 300 && upstream.status < 400) {
          cancelBody(upstream);
          return failure('redirect', 502);
        }
        const responseHeaders = noStore(cleanHeaders(upstream.headers));
        responseHeaders.set('x-nonbiri-proxy-response', 'upstream');
        return new Response(upstream.body, {
          status: upstream.status,
          statusText: upstream.statusText,
          headers: responseHeaders,
        });
      } catch {
        cancelBody(upstream);
        return failure('upstream', 502);
      }
    },
  };
}

export default createProxy();
