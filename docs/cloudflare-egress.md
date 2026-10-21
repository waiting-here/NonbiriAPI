# Cloudflare forwarding

Platform models can use an authenticated Cloudflare Worker to reach upstreams
through Cloudflare's network. One Worker handles different providers and endpoint
paths; adding an upstream does not require a new gateway mapping. The connector
still determines the supported API operations and wire format.

## Configure the Worker

Deploy the [included Worker](../deploy/cloudflare-worker/README.md) with its
provided compatibility flags and logging disabled. Set its private
`NONBIRI_PROXY_TOKEN` secret to a random token of at least 32 visible ASCII
characters, without whitespace. Use the same value in the application's private
startup environment:

| Variable | Meaning |
| --- | --- |
| `NONBIRI_CLOUDFLARE_WORKER_URL` | Complete HTTPS Worker entry URL, without user information, query or fragment. |
| `NONBIRI_CLOUDFLARE_WORKER_TOKEN` | The same private token configured as `NONBIRI_PROXY_TOKEN` on the Worker. |

Configure both variables or leave both empty. Restart the application after a
change. Keep secrets outside version control and readable only by the service
account and operator. The application does not create or deploy Cloudflare
resources. Configured status means the process loaded the settings; it does not
prove that the Worker, token or upstream is reachable.

Use a Worker with public outbound access and no private-network, origin or service
bindings. Preserve `global_fetch_strictly_public`, `enable_request_signal` and
`request_signal_passthrough` in the supplied configuration. Do not enable payload
logs, caching or extra retries. Worker requests authenticate before fetching any
target, and do not follow upstream redirects.

Cloudflare Workers fetch accepts public DNS hostnames, not IP-literal upstreams.
An enabled model targeting an IP literal fails through this route; it does not
silently switch to a direct connection. Public HTTP and HTTPS endpoints otherwise
retain their original path, query, authentication and protocol. See Cloudflare's
[fetch limitations](https://developers.cloudflare.com/workers/platform/known-issues/#fetch-to-ip-addresses)
and [request API](https://developers.cloudflare.com/workers/runtime-apis/request/).

## Choose models

Personal models enable Cloudflare forwarding by default. Charity models disable
it by default. These defaults also apply to models created before this setting
was introduced. Owners can change their personal models; administrators and
level-6 stewards can change charity models in the existing model editor.

The saved `cloudflare_proxy_enabled` setting is captured when a request is
admitted. Changing the model does not reroute an already admitted request. When
enabled, the process uses the Worker if configured. Without a Worker, existing
AI Gateway mappings remain available for matching endpoints; unmatched endpoints
use a direct connection. When disabled, the model uses a direct connection.
Forwarding failures never trigger an automatic direct retry.

The read-only `cloudflare_proxy_mode` field describes the process configuration:
`worker`, `legacy` or `direct`. It is independent of the model's saved switch.
`legacy` means only matching old mappings are forwarded; `direct` means no
Cloudflare forwarding is configured. Model discovery and key probes have no
model switch and use the configured Worker, then legacy mappings when no Worker
is configured.

After upgrading, manually enable forwarding for charity models that use Google
official endpoints and need the Cloudflare route.

## Privacy and connection behavior

Cloudflare receives upstream credentials and request/response content for each
routed call. Describe this processing in the instance privacy notice. Disabling
Worker logs and caching does not guarantee zero retention across every network
service or provider.

The application checks the original endpoint and Worker against its shared
outbound policy. The connection to the Worker retains DNS/IP validation and
pinning, concurrency limits, request deadlines, cancellation and response limits.
Cloudflare resolves and connects to the final upstream; that remote DNS decision
is not pinned to the application's local lookup. The Worker uses public fetch
with the supplied compatibility flags, rejects local names and its own hostname,
and never uses private-network bindings or follows redirects.

Only a private outbound clone receives the target and Worker authentication
headers. These control headers are stripped before forwarding to the upstream.
The Worker also removes hop-by-hop headers and source-IP forwarding headers,
including headers Cloudflare added on the incoming connection. Cloudflare can
still apply its own network headers. See its
[Worker subrequest header behavior](https://developers.cloudflare.com/fundamentals/reference/http-headers/#cf-connecting-ip-in-worker-subrequests).
This changes the network egress; it does not remove account, key or provider
policy restrictions, or guarantee a particular exit country.

JSON and streaming responses retain their protocol. Responses are checked for
reflected upstream credentials, the Worker token and private forwarding URL before
diagnostic capture and protocol parsing. Transport and Worker failures expose
fixed safe errors. Worker failures do not increase a donated key's failure count
or trigger its quota backoff. The Worker never retries an upstream fetch.

The Worker marks genuine upstream responses with the private
`X-Nonbiri-Proxy-Response: upstream` header after stripping all upstream-supplied
proxy control headers. The application requires that confirmation and removes it
before response capture. Missing or invalid confirmation, Worker error markers,
connection failures and proxy stream transport failures are platform failures.
This also covers Cloudflare errors returned before the Worker runs. Caller
cancellation retains its existing classification. Confirmed upstream HTTP errors
and invalid protocol content retain normal handling; billing still follows
recorded delivery and consumption facts.

Before production use, verify normal generation, streaming, an invalid-key
control and cancellation with the intended connector. HTTP 200 alone is
insufficient: the connector must still validate protocol completion and usage.

## Legacy AI Gateway configuration

`NONBIRI_CLOUDFLARE_ROUTES` remains supported for existing installations when no
Worker is configured. It is a JSON array of at most 32 exact endpoint mappings;
unset, blank or `[]` loads none. Worker configuration takes precedence over the
whole array.

| Field | Meaning |
| --- | --- |
| `source_base_url` | Complete upstream API base, matched after normalization. |
| `gateway_base_url` | HTTPS provider-native or Custom Provider mount, including the required API path. |
| `token_env` | Environment variable containing a private AI Gateway Run token. |
| `credential_header` | Upstream authentication header. |
| `credential_query_parameter` | Optional query key receiving the header value; removes that header by default. |
| `retain_credential_header` | Optional boolean, default false; requires a query key and retains both forms. |

For a Gemini `/v1beta` base, an existing route can be:

```json
[
  {
    "source_base_url": "https://generativelanguage.googleapis.com/v1beta",
    "gateway_base_url": "https://gateway.ai.cloudflare.com/v1/ACCOUNT_ID/GATEWAY_ID/google-ai-studio/v1beta",
    "token_env": "NONBIRI_CF_GATEWAY_TOKEN",
    "credential_header": "x-goog-api-key",
    "credential_query_parameter": "key",
    "retain_credential_header": true
  }
]
```

Duplicate normalized sources, malformed URLs, missing token variables and invalid
credential settings fail configuration. Bases cannot contain user information,
query or fragment. A complete base must match, including its API mount. A parent
domain or another API version is not a wildcard. Gateway query-key collisions
fail the request. Credentials added to queries remain private to the outbound
clone.

Require gateway authentication and provider credentials. Disable logs, payload
collection, cache, additional retries, stored provider keys and billing fallback.
The application fixes gateway controls accordingly. See the
[provider-native guide](https://developers.cloudflare.com/ai-gateway/usage/providers/google-ai-studio/)
and [Custom Providers](https://developers.cloudflare.com/ai-gateway/configuration/custom-providers/).

## Google official channels

AI Studio supports the existing `google-gemini` connector, or
`openai-compatible` at
`https://generativelanguage.googleapis.com/v1beta/openai` with a bare API key.
The generic Worker preserves the connector's native credential header; it needs
no provider-specific query adaptation. See
[Google's compatibility guide](https://ai.google.dev/gemini-api/docs/openai/).

Vertex AI's OpenAI-compatible Chat endpoint uses `openai-compatible`. Example
bases are:

- Regional: `https://us-central1-aiplatform.googleapis.com/v1/projects/PROJECT/locations/us-central1/endpoints/openapi`
- Global: `https://aiplatform.googleapis.com/v1/projects/PROJECT/locations/global/endpoints/openapi`

Use model ID `google/{model}` and store the bare OAuth access token; the connector
adds `Bearer `. Tokens normally expire after one hour and must be refreshed
externally. The platform does not store service-account JSON, obtain ADC
credentials or refresh tokens. Expired credentials follow the existing
authentication-failure behavior. Register models manually: automatic `/models`
discovery is not established by the verified Vertex contract. Model availability
depends on the project and location.

Native Vertex OAuth, native discovery and complete Express-mode support are
outside this compatibility claim. Controlled tests cover regional/global paths,
Bearer authentication, JSON, SSE and errors; they do not establish live provider
availability. References: [Vertex compatibility](https://docs.cloud.google.com/vertex-ai/generative-ai/docs/start/openai),
[authentication](https://docs.cloud.google.com/vertex-ai/generative-ai/docs/migrate/openai/auth-and-credentials)
and [locations](https://docs.cloud.google.com/vertex-ai/generative-ai/docs/learn/locations).

## View loaded configuration

Administrators and current level-6 stewards can inspect the loaded configuration
on the endpoint policies page. Refresh reads the running process without probing
any upstream. Changes still require deployment configuration and a restart.

Session-authenticated `GET /admin/api/egress/cloudflare-routes` and
`GET /api/steward/egress/cloudflare-routes` accept no query or body and return
`{enabled,mode,worker_origin,routes}`. `mode` is `worker`, `legacy` or `direct`;
`worker_origin` contains only the configured Worker's scheme and host, or an
empty string. `enabled` means either forwarding configuration is loaded.

Each legacy route includes `source_base_url`, `gateway_origin`,
`credential_header`, `credential_query_parameter` and `retain_credential_header`.
These mappings are inactive when a Worker is configured. Tokens, secret variable
names and full private forwarding paths are omitted. CallerKey, level-5 and
ordinary user access are rejected. Responses use `Cache-Control: no-store`.
