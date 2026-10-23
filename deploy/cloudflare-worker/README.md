# Cloudflare Worker relay

This optional Worker forwards NonbiriAPI HTTP requests to public DNS upstreams.
Adding an upstream requires no Worker routing entry. It forwards the methods,
paths, queries, authentication headers, and streaming bodies already supported
by NonbiriAPI's connectors; it does not add protocol conversions.

## Configure and deploy

Use a Cloudflare account and Wrangler when ready to deploy. The module has no
runtime dependencies or build step. Review the Worker name in `wrangler.jsonc`,
then run these commands from this directory:

```sh
npx wrangler deploy
npx wrangler secret put NONBIRI_PROXY_TOKEN
```

The second command prompts for the private shared token. Use at least 32 random
bytes encoded as hex, or another random value of 32–4096 visible ASCII characters
without spaces. Until the secret is set, every request is rejected. Store it as
a Worker secret, never in the committed configuration. Configure the application
with the same value:

```text
NONBIRI_CLOUDFLARE_WORKER_URL=https://your-worker.your-subdomain.workers.dev/
NONBIRI_CLOUDFLARE_WORKER_TOKEN=<the same value as the NONBIRI_PROXY_TOKEN secret>
```

Set both application values together. The URL must use HTTPS with no userinfo,
query, or fragment. Model settings control content requests; see the
[application guide](../../docs/cloudflare-egress.md). Rotate the token on both
sides together.

Keep the three compatibility flags and disabled observability. Do not add
origin, service, VPC, or Tunnel bindings or copy incoming `request.cf` values;
these change the public-network boundary. The template has no storage, logs,
caching, retries, or direct-route fallback. Cloudflare limits and upstream
restrictions still apply.

## Forwarding contract

`X-Nonbiri-Proxy-Target` carries the complete target URL and
`X-Nonbiri-Proxy-Token` carries the shared token. Both are removed before
forwarding. Configuration and authentication are checked before target parsing.
Targets must use HTTP(S), contain a DNS hostname, and fit within 8192 UTF-8 bytes.
Userinfo, fragments, control characters, backslashes, IP literals, single-label
names, localhost/local/internal names, and the Worker's own hostname are rejected.
The application retains its endpoint validation and resource limits. Cloudflare
resolves the upstream; local DNS checks do not pin that remote connection.

GET, HEAD, POST, PUT, PATCH, DELETE, and OPTIONS are forwarded. Upstream
authentication and content headers are retained. Hop-by-hop headers, fields
named by `Connection`, `Host`, forwarding/source-IP headers including
Cloudflare's client-IP headers, and all `X-Nonbiri-Proxy-*` headers are removed.
Cloudflare can add its own network metadata to outbound traffic.

Outbound requests use public `fetch`, manual redirects, `cache: 'no-store'`,
explicit cache-disable settings, and the incoming cancellation signal. Every
3xx response is canceled and reported as a relay error without another fetch.
Other statuses and bodies are relayed, including API errors and SSE. Response
bodies remain streams; no complete stream is buffered. Upstream private headers
are removed so they cannot forge a relay failure. Responses carry no-store
browser and CDN cache controls.

After removing all upstream `X-Nonbiri-Proxy-*` headers, the Worker sets exactly
one `X-Nonbiri-Proxy-Response: upstream` header on every relayed response,
including non-200 errors and SSE. Worker-generated errors omit this confirmation.
The application requires it to distinguish upstream responses from Worker or
Cloudflare failures. Proxy failures do not count against donated keys; confirmed
upstream errors still follow the normal failure rules. The application removes
these internal headers before logging or returning responses to callers.

Worker failures return the same short JSON body and a closed
`X-Nonbiri-Proxy-Error` value, with no target, token, or exception text:

| Value | HTTP status | Meaning |
| --- | --- | --- |
| `configuration` | 503 | The Worker secret is absent or invalid. |
| `authentication` | 401 | The token is absent or incorrect. |
| `target` | 400 | The target fails validation. |
| `method` | 405 | The method is unsupported. |
| `redirect` | 502 | The upstream returned a 3xx status. |
| `upstream` | 502 | The outbound request failed or was canceled. |

An error after streaming starts terminates the stream; it cannot become a new
JSON response. The application's existing stream-completion handling applies.

## Local verification

With Node.js 22 or later, from the repository root:

```sh
node --test deploy/cloudflare-worker/worker.test.mjs
```

Tests use real Fetch/Streams objects and a fetch stub with no network requests.
They verify forwarding, validation, header filtering, redirects, cancellation,
and backpressure. They do not prove deployment, remote DNS behavior, account
configuration, or real upstream connectivity.

Runtime references: [Request options](https://developers.cloudflare.com/workers/runtime-apis/request/),
[compatibility flags](https://developers.cloudflare.com/workers/configuration/compatibility-flags/),
[client-IP headers](https://developers.cloudflare.com/fundamentals/reference/http-headers/),
[IP-literal limitation](https://developers.cloudflare.com/workers/platform/known-issues/#fetch-to-ip-addresses),
and [public fetch implementation notes](https://github.com/cloudflare/workerd/blob/main/src/workerd/io/compatibility-date.capnp).
