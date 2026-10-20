# Cloudflare endpoint forwarding

An operator can route selected endpoint bases through Cloudflare AI Gateway.
The setting applies to matching personal, charity and discovery requests.
Endpoint records and connector protocols stay the same. An endpoint without a
matching rule uses its existing direct connection.

## Configure a route

Set `NONBIRI_CLOUDFLARE_ROUTES` to a JSON array in the private startup environment.
Unset, blank or `[]` disables forwarding. At most 32 routes are accepted.
Each route has these fields:

| Field | Meaning |
| --- | --- |
| `source_base_url` | Complete endpoint base to match after URL normalization. Include its API mount. |
| `gateway_base_url` | HTTPS provider-native or custom-provider mount at the gateway. Include any API version required before the remaining request path. |
| `token_env` | Name of a separate environment variable containing the gateway Run token. |
| `credential_header` | The provider credential header required on each matching request. |
| `credential_query_parameter` | Optional query parameter receiving that header's value. The credential header is removed by default. |
| `retain_credential_header` | Optional boolean, default `false`. Set `true` with a query parameter to send the credential in both places. Requires `credential_query_parameter`. |

For example, a Gemini endpoint using `/v1beta` can use:

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

Discovery and generation may require different credential forms at the gateway.
This example keeps the provider header and adds the query parameter for both.

Put the actual token in `NONBIRI_CF_GATEWAY_TOKEN` in the same private environment,
and restart after changing either value. The route JSON contains a variable
name, never the token. Keep this environment outside version control and readable
only by the service account and operator.

Routes are not tied to Google. For an OpenAI-compatible endpoint, configure its
exact source and gateway API mounts, set `credential_header` to `Authorization`,
and omit `credential_query_parameter` when Bearer authentication is supported.
Cloudflare's [provider-native example](https://developers.cloudflare.com/ai-gateway/usage/providers/google-ai-studio/)
and [custom-provider documentation](https://developers.cloudflare.com/ai-gateway/configuration/custom-providers/)
describe the gateway mounts. A connector still supports only its documented
operations and wire format.

The complete source base must match; a parent domain or a different API version
does not select the route. Duplicate normalized sources, malformed URLs, missing
token variables and invalid credential settings fail configuration. Gateway URLs
cannot contain credentials, a query or a fragment. Each request must have the
configured provider credential, and a query-key collision fails the request.

## Gateway settings and privacy

Create an authenticated gateway with a token limited to **AI Gateway Run**.
Disable request logs, payload collection, caching and extra retries, require
provider credentials, and keep stored provider keys and billing fallback disabled.
The application also fixes per-request controls to disable logging and cache,
allow one attempt, and send `cf-aig-no-wholesale:true`. Incoming gateway controls
cannot override these values. See Cloudflare's
[header reference](https://developers.cloudflare.com/ai-gateway/glossary/).

Cloudflare receives the provider credential, request content and response content
for each routed call. Describe that processing in the instance's privacy notice.
Disabling gateway logs and caching does not guarantee zero retention across every
provider or network service.

Query credentials exist only on the private outbound request. The original
endpoint remains the public request identity. Routed responses are checked before
header capture, error-diagnostic storage and protocol parsing for reflected
provider credentials, gateway tokens and their supported encoded forms.
Transport errors do not expose the private gateway URL.

## Connection behavior and verification

Both the original endpoint and gateway pass the shared outbound policy checks.
Connections to the gateway retain DNS/IP validation, response limits, shared
concurrency, timeouts and cancellation. Redirects are rejected. The application
does not enable environment proxies, retry through a different gateway, or fall
back to the direct endpoint after a failure.

Before enabling a route, verify model discovery, normal generation and streaming
with the intended connector and provider key. Check an invalid-key control, long
responses and client cancellation. HTTP 200 alone is insufficient: the connector
must still validate the protocol's terminal event and usage. Keep gateway-side
timeouts compatible with the application's request budget.

## Google official channels

AI Studio's official OpenAI-compatible base is
`https://generativelanguage.googleapis.com/v1beta/openai`. Use
`openai-compatible` with a bare API key and a Cloudflare Custom Provider
pointing to the official origin, preserving the complete API mount.
See [Google's compatibility guide](https://ai.google.dev/gemini-api/docs/openai/).

Vertex AI's official OpenAI-compatible Chat Completions endpoint also uses
`openai-compatible`. Regional and global bases are separate exact routes:

```json
[
  {
    "source_base_url": "https://us-central1-aiplatform.googleapis.com/v1/projects/PROJECT/locations/us-central1/endpoints/openapi",
    "gateway_base_url": "https://gateway.ai.cloudflare.com/v1/ACCOUNT/GATEWAY/custom-vertex-regional/v1/projects/PROJECT/locations/us-central1/endpoints/openapi",
    "token_env": "NONBIRI_CF_GATEWAY_TOKEN",
    "credential_header": "Authorization"
  },
  {
    "source_base_url": "https://aiplatform.googleapis.com/v1/projects/PROJECT/locations/global/endpoints/openapi",
    "gateway_base_url": "https://gateway.ai.cloudflare.com/v1/ACCOUNT/GATEWAY/custom-vertex-global/v1/projects/PROJECT/locations/global/endpoints/openapi",
    "token_env": "NONBIRI_CF_GATEWAY_TOKEN",
    "credential_header": "Authorization"
  }
]
```

Configure the corresponding Cloudflare Custom Provider bases as
`https://us-central1-aiplatform.googleapis.com` and
`https://aiplatform.googleapis.com`. Append the project path once; the
application does not create these providers. Use model ID `google/{model}`
and store the bare OAuth access token. The connector adds `Bearer `.
Do not copy the complete Authorization header into a query parameter.

Access tokens normally expire after one hour. Refresh and update them externally;
the platform does not store service-account JSON, obtain ADC credentials or
refresh tokens. Expired tokens follow existing authentication-failure behavior.
Model availability depends on project and location. Register models manually:
automatic `/models` discovery is not established by the verified Vertex contract.

These examples cover existing OpenAI-compatible Chat operations. Native Vertex
OAuth, native model discovery and complete Express-mode support are outside this
compatibility claim. Controlled regional/global tests verify paths, Bearer,
JSON, SSE and errors; they do not establish live provider availability.

References: [Vertex compatibility](https://docs.cloud.google.com/vertex-ai/generative-ai/docs/start/openai),
[authentication](https://docs.cloud.google.com/vertex-ai/generative-ai/docs/migrate/openai/auth-and-credentials),
[locations](https://docs.cloud.google.com/vertex-ai/generative-ai/docs/learn/locations)
and [Custom Providers](https://developers.cloudflare.com/ai-gateway/configuration/custom-providers/).

## View loaded routes

Administrators and current level-6 stewards can view loaded routes on the endpoint
policies page. Refresh reads the current process without probing the upstream.
Configuration changes require a restart.

Session-authenticated `GET /admin/api/egress/cloudflare-routes` and
`GET /api/steward/egress/cloudflare-routes` accept no query or body and return
`{enabled,routes}`. Each route has `source_base_url`, `gateway_origin`,
`credential_header`, `credential_query_parameter` (empty if absent) and
`retain_credential_header`. Tokens, environment-variable names and full gateway
paths are omitted. CallerKey, level-5 and ordinary user access are rejected.
Responses use `Cache-Control: no-store`.

`enabled:false,routes:[]` means no routes are loaded. Enabled means configuration
is active; it does not establish credential validity or network health.
