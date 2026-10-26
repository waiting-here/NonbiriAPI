# Request admission and outbound transformation

Client-abuse auditing and configured enforcement use the original inbound request. Keep a bounded, allowlisted source snapshot after trusted host/proxy validation; never replace that snapshot with outbound headers or adapted request content.

After authentication and the minimum parsing and model-access checks needed to identify an authorized call, run applicable abuse rules and complete their configured actions before content-length checks, balance reservation, candidate availability, debug capture, or other business rejection paths. A later rejection must not prevent a matching rule's action. Invalid credentials, inaccessible models, malformed requests and transport/resource limits retain their own entry boundaries; do not widen which users or calls a rule is authorized to punish.

Only admitted requests may proceed to route adaptation and dispatch. Fixed headers, forwarded headers, generated session values, body defaults/forced values and future connector transformations must operate on an outbound representation. For example, a configured upstream `User-Agent` cannot hide the caller's original client signature, and an upstream-only signature cannot create a false inbound match.

Live automatic client-rule bans apply to both personal and charity model calls when the rule and its administrator-controlled ban binding are enabled. Historical audit scans and model discovery do not trigger these bans. Charity-only short-content and RPM penalties retain their existing scope. Actions remain idempotent per logical request and respect account-retirement barriers.

For short charity input, freeze the debug mode before recording the rejection or its penalties. Dry run captures the request and warns about the minimum length in both the caller response and debug trace; it does not deduct points or add a short-content event to automatic-ban or suspension counters. The usual dry-run response still means no upstream request was sent. Live debug and ordinary requests retain short-content rejection, deductions and configured automatic restrictions. This exception does not bypass original-client rules, authentication, model access or RPM admission.

Chat input length includes message strings/text parts, function-tool descriptions, parameter-schema text (decoded string keys and values), and prior tool-call arguments. Legacy `functions`/`function_call` fields count the same way. Tool results count once through message content. The count uses Unicode characters after model-configured field exclusions; image/audio payloads, call IDs, sender names and unrelated metadata do not contribute. Tool presence alone never exempts a short request. Counting does not alter the request, log its contents or infer tokens from JSON byte length.

## Review and regression expectations

Any new preprocessing, proxying, adaptation, routing, billing, retry or debug path must preserve this order. Check both directions: a matching original header is enforced despite replacement, and a permitted original header is evaluated before the upstream replacement is sent. Cover pre-dispatch refusals with no upstream attempt, reservation or managed-session refresh. Reuse the ingress snapshot and common admission path for every supported request kind; do not add per-connector audit variants.
