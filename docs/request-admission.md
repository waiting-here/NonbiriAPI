# Request admission and outbound transformation

Client-abuse auditing and configured enforcement use the original inbound request. Keep a bounded, allowlisted source snapshot after trusted host/proxy validation; never replace that snapshot with outbound headers or adapted request content.

After authentication and the minimum parsing and model-access checks needed to identify an authorized call, run applicable abuse rules and complete their configured actions before content-length checks, balance reservation, candidate availability, debug capture, or other business rejection paths. A later rejection must not prevent a matching rule's action. Invalid credentials, inaccessible models, malformed requests and transport/resource limits retain their own entry boundaries; do not widen which users or calls a rule is authorized to punish.

Only admitted requests may proceed to route adaptation and dispatch. Fixed headers, forwarded headers, generated session values, body defaults/forced values and future connector transformations must operate on an outbound representation. For example, a configured upstream `User-Agent` cannot hide the caller's original client signature, and an upstream-only signature cannot create a false inbound match.

Preserve existing policy scope: live automatic client-rule bans apply to configured charity calls; ordinary personal calls and historical audit scans do not acquire new punishment behavior from this ordering rule. Actions remain idempotent per logical request and respect account-retirement barriers.

## Review and regression expectations

Any new preprocessing, proxying, adaptation, routing, billing, retry or debug path must preserve this order. Check both directions: a matching original header is enforced despite replacement, and a permitted original header is evaluated before the upstream replacement is sent. Cover pre-dispatch refusals with no upstream attempt, reservation or managed-session refresh. Reuse the ingress snapshot and common admission path for every supported request kind; do not add per-connector audit variants.
