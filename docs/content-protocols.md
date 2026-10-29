# Chat and Decisions connectors

The connector selected for an endpoint determines its available upstream protocols. A call also needs an enabled platform `model_types` entry and an upstream service authorized by the endpoint, credential and binding. Keys remain bound to their endpoint and encrypted context; the platform does not infer a protocol from a hostname or model name.

| Client operation | Authorized native service and connector | Upstream resource appended to the versioned base |
| --- | --- | --- |
| `/v1/chat/completions` | OpenAI family `chat_completions` first; otherwise bounded conversion through selected `responses`, Anthropic `anthropic_messages`, Gemini `gemini_generate_content` or Gateway `gateway_text` | `/chat/completions`, `/responses`, `/messages`, `/models/{model}:generateContent` (or `:streamGenerateContent?alt=sse`), or `/language-model` |
| `/v1/responses` | OpenAI family `responses` first; otherwise bounded conversion through selected `chat_completions` or Gateway `gateway_text` | `/responses`, `/chat/completions`, or `/language-model` |
| `/v1/responses/compact`, `/v1/responses/input_tokens` | Corresponding OpenAI family service explicitly authorized | `/responses/compact`, `/responses/input_tokens`; no Chat conversion |
| Embeddings, image generation, Decisions and other direct operations | Corresponding OpenAI family service explicitly authorized; Gateway also supports embeddings, image generation and rerank | Matching native suffix; Gateway uses its service-specific suffix |

The OpenAI family consists of `openai-compatible`, `openai-chat-completions`, `openai-responses` and `openai-decisions`. Their explicit `native_services` selections can authorize the same supported direct services, including Decisions. Omitting the selection keeps the legacy subset for that type; it does not enable all services. Native routes take precedence over compatible conversions. For example, an `openai-responses` endpoint with `embeddings` authorized at every layer can call `/embeddings`; an `openai-chat-completions` binding with `responses` selected uses native `/responses` before considering conversion. The upstream model must actually support the service. Gemini and Anthropic remain Chat conversion connectors. Native Responses remains stateless. See [direct inference](direct-inference.md) for the complete 13-type registry and legacy defaults.

These connectors use the shared outbound boundary, cancellation, timeouts, concurrency limits and credential protections. Responses are bounded to 8 MiB for JSON or 32 MiB cumulatively for SSE, with 256 KiB lines and 1 MiB events, or a smaller configured shared limit. Translated output also respects those bounds. Upstream IDs and model metadata are projected to local completion IDs and the requested platform model; explicit continuation fields below retain the opaque information needed by that protocol.

## Gemini Chat

Gemini rejects tool-call flattening during candidate preflight because the text tool format cannot retain Google thought signatures. Both JSON and SSE calls require this option to be disabled.

Configure the complete API mount, for example `https://generativelanguage.googleapis.com/v1beta`. The connector authenticates with `x-goog-api-key`. An explicit operator-configured [Cloudflare route](cloudflare-egress.md) can move this credential into a private outbound query parameter when required by the gateway. Direct requests retain header authentication. Model IDs are one path segment, optionally prefixed once with `models/`.

Supported Chat input includes leading system/developer text, user/assistant text, function definitions and matched tool results, temperature, top-p, stop sequences, one candidate, output-token limits, and streaming. Images must be bounded PNG, JPEG, GIF or WebP base64 data URLs. Arbitrary remote image URLs, audio, files, server-executed tools, `store`, `reasoning_effort`, multiple candidates, strict function schemas and `parallel_tool_calls:false` with tools are incompatible. `response_format` supports `text` and `json_object`; JSON-schema formats are incompatible.

Function arguments must be JSON objects; omitted upstream `args` means `{}`. Tool-call IDs are preserved or generated from a fresh random response identity and call index when absent upstream, including multiple responses within one second. Every tool result must match an unconsumed earlier call; parallel results retain their IDs and function names. JSON-object results remain objects; ordinary string results use `{ "result": "..." }` in the native function response.

Signed or thought-bearing replies retain ordered native parts in `message.extra_content.google.parts`. Each part keeps its text or function call, thought flag and original `thoughtSignature`; multiple separately signed parts and text/tool interleaving survive replay. Streaming sends this complete extension once in its final delta. Clients must return it unchanged with the displayed assistant content, reasoning and tool calls; the compiler checks that these projections agree before replaying the native order. Signatures are bounded to 64 KiB each and validated as base64; the native part array has at most 4,096 entries within the request/response limits. Legacy message/tool `extra_content.google.thought_signature` remains accepted, and signed tool calls also retain that field. The platform does not fabricate signatures or use a validation-bypass value. These histories require Gemini candidates.

Thought text is returned as `reasoning_content`. Native extensions are limited to `generationConfig.thinkingConfig` (`includeThoughts`, `thinkingBudget` or `thinkingLevel`) and `safetySettings`. Declare these paths in the existing request-adaptation configuration. Output budgets must use Chat token-limit fields; native changes to contents, tool configuration, platform identity or output limits are rejected. The platform pseudonym is sent as a lowercase `labels.safety_identifier` value.

A successful stream requires a recognized candidate finish reason. `STOP` maps to `stop` or `tool_calls`, `MAX_TOKENS` to `length`, and supported safety blocks to `content_filter`. Missing/unknown finish reasons, malformed function calls, content after termination and truncated streams fail. Usage-only tail frames are allowed; EOF alone does not complete a request.

`CONTINUATION` or a non-null `continuationToken` explicitly fails with an automatic-continuation-not-supported diagnostic and retains valid reported usage. The connector makes no follow-up request and does not label the partial output complete. Automatic long-output continuation, `partialArgs` and other tool-argument streaming APIs are outside this connector's scope.

Usage maps `promptTokenCount - cachedContentTokenCount` to uncached input, cached tokens to cache-read, and `candidatesTokenCount + thoughtsTokenCount` to output. Cache-write is zero. Cumulative snapshots replace earlier counts. Invalid, regressive or overflowing counts make usage unknown; inconsistent totals retain a diagnostic flag. Server-tool prompt consumption is not mapped into ordinary Chat buckets. Caller usage chunks follow `stream_options.include_usage`.

Discovery uses bounded `GET /models` pagination: at most 20 pages, 1 MiB in total and 1,000 original entries. Only models advertising `generateContent` are retained. Page cycles, malformed entries and credential reflection reject the complete discovery result.

## Multimodal usage and prices

Provider pricing can distinguish input modalities. Platform token billing has
four buckets: uncached input, cache-write input, cache-read input and output.
Separate text, image, audio or video prices are not available. Gemini modality
breakdowns and OpenAI audio-token details are not split into new billing buckets;
valid aggregate usage is settled at the configured bucket prices. Image responses
may retain supported text/image usage details in the public response while
settlement uses aggregate buckets. Per-request prices remain fixed. These totals
do not reproduce a provider's modality-specific invoice.

## Responses Chat

Configure the complete API mount, for example `https://api.openai.com/v1`. Authentication uses Bearer credentials. Every request sends `store:false` and requests `reasoning.encrypted_content`. Caller `store:true`, previous-response IDs, conversation references, background execution, remote file IDs and built-in tools are incompatible.

Chat messages map to Responses input messages and function-call/output items. Supported controls include function tools, tool choice, parallel tools, temperature, top-p, reasoning effort, cache key/retention and text/JSON/JSON-schema response formats. Omitted function `strict` remains false instead of acquiring the Responses default. Chat output-token limits map to `max_output_tokens`. Stop sequences, penalties, logit bias and multiple candidates are incompatible. User images may use HTTPS or data URLs; the platform does not fetch them. Native extensions support only `reasoning.summary` and `text.verbosity`.

Responses reasoning items are returned in the bounded project extension `message.extra_content.openai.reasoning`. This is an array of complete `reasoning` items containing their ID, summary and optional encrypted content/status. Each encrypted string is limited to 64 KiB and the extension to 1 MiB/128 items. A client must retain this extension for a stateless reasoning/tool round trip. It is inserted before that assistant's text and function calls on the next request. Reasoning appearing after ordinary output cannot be faithfully projected and fails. These fields are accepted only by Responses candidates and are never stored as a platform conversation. `reasoning_content`, when returned, is the concatenated summary; replay must match the retained reasoning items.

Output supports assistant text/refusal, function calls and reasoning summaries. Unsupported output items, annotations requiring another modality or malformed tool arguments fail instead of disappearing. Streaming validates response identity, increasing sequence numbers, item/content indexes and add/delta/done lifecycles. An item-done event may omit its index when its unique ID identifies an already added item. A valid `response.completed` or `response.incomplete` may omit object/status/output: the terminal kind supplies object/status, and completed matching item snapshots supply omitted output. Explicit empty output stays empty; nonempty terminal output must match collected items. `response.completed` maps to `stop` or `tool_calls`; `response.incomplete` still needs a supported reason to map to `length` or `content_filter`. A missing reason cannot be invented for Chat. Failed events, missing terminal events and open items at termination fail. The connector emits the caller's Chat terminator only after validation.

Usage uses `input_tokens - cached_tokens - cache_write_tokens`, separate cache buckets and `output_tokens`. Reasoning tokens are already included in output and are not added again. Invalid bucket details or overflow make usage unknown. A contradictory total is a diagnostic flag, not a substitute for valid buckets.

Failed JSON responses and SSE error/failed events retain independently valid usage while remaining failures. Missing usage is never inferred from visible text or reasoning.

## Anthropic thinking and continuation

Thinking remains an explicit native request-adaptation choice. JSON `thinking`/`redacted_thinking` blocks and SSE `thinking_delta`/`signature_delta` are supported. Actual readable thinking appears separately as `reasoning_content`; omitted and redacted text creates no fabricated explanation. Ordered text, thinking, redacted-thinking and tool-use blocks are retained in `message.extra_content.anthropic.content`, emitted once at stream completion. Replay validates visible text, reasoning, tool IDs/names and arguments against those blocks and preserves their order, signatures and redacted data. Signatures are opaque strings bounded to 64 KiB, redacted data to 1 MiB, and the content array to 4,096 blocks within the complete-body limits.

Clients must return complete thinking blocks on a thinking/tool round trip. Readable reasoning alone cannot restore a missing signature. Ordinary non-tool history can omit thinking under the upstream's rules. Known model/mode incompatibilities, invalid manual budgets and incompatible forced tool choices reject before dispatch; a manual budget is not applied as a universal constraint to adaptive models. `diagnostics:null`, supported usage speed metadata and null/empty citations are accepted; nonempty citations remain unsupported. Optional empty-choices usage frames are emitted only for `stream_options.include_usage:true`, while internal usage is always retained.

## Continuation source and history integrity

Generated opaque histories include `extra_content.nonbiri.continuation_id`, a bounded opaque source binding with no physical model, endpoint or credential identifiers. Retain it along with the provider namespace. Routing confines these histories to the permitted physical source and rejects tool flattening or history changes that invalidate the continuation. A compatible protocol alone does not guarantee that another model or account can reuse a signature. Older untagged third-party histories remain accepted within their protocol; callers are responsible for retaining a compatible source. Direct connector integrations without a binding policy cannot enforce platform route affinity.

Gateway Chat and bounded Responses histories use `extra_content.gateway.content`; see the [Gateway continuation contract](api-contract.md#24-native-ai-sdk-gateway-v3v4-compatibility) for its closed metadata mapping. Continuation data is carried by the caller and does not create a platform session store.

## Decisions

`POST /v1/decisions` accepts the native Decisions shape. A minimal request is:

```json
{
  "model": "provider/classifier",
  "input": "The screen is broken.",
  "questions": [
    {"type": "predicate", "name": "damaged", "instructions": "Is it damaged?"}
  ]
}
```

`input` is a string or supported message/function/custom-tool history. User messages support `input_text`, `input_image` and `input_json`; tool outputs support text and images. Developer/system messages accept input text, while assistant messages accept output text or refusals. Histories must pair call IDs with their outputs. Files, audio and item references are rejected.

Questions contain 1–400 predicate, choice or score entries. Each question's `instructions` is a string or a nonempty array of `input_text`, `input_image` and `input_json` parts, without a message role or cache breakpoint. The 128-image request limit combines shared input, tool outputs and all questions. Choices contain 2–255 unique typed string/boolean values; score questions contain 2–10 levels in their declared order. Names are optional and positional.

An `input_json` part has `type` and `data` fields. `data` is an object or an array of objects; nested values may use any JSON type. Each part allows at most 32 container levels, 10,000 values (including containers, excluding object keys) and 256 KiB of compact UTF-8 JSON. Numbers retain their JSON representation. Keys such as `role` and `image_url` inside `data` remain ordinary data and do not create messages or images. JSON parts do not accept cache breakpoints.

Cache key/options follow the native API with `implicit`/`explicit` mode and `30m` TTL. Explicit breakpoints remain on shared text/image parts; questions are outside the reusable prefix. The platform replaces `model` and `safety_identifier` at dispatch. Omitted `stream` is normal; `stream:false` is a local convenience and is stripped. `stream:true`, `stream:null`, unknown top-level fields and native extensions are rejected before dispatch.

The response contains `model`, `answers` and valid native `usage` when available. Answer count, ordering, names, types, choice values, score levels and probabilities are checked against the request. Per-question refusals are valid answers. The public model name replaces the physical one. Usage follows the same four-bucket accounting as Responses: cache-read/write tokens are subsets of input and are subtracted once to obtain ordinary input. Missing or malformed usage uses existing conservative settlement. Pricing remains configured by the deployment; upstream price changes do not update platform prices automatically.

All questions in one request constitute one logical platform call. Existing per-call or token pricing, quotas, reservation, donor reward, idempotent settlement and account-deletion rules apply; there is no new per-question price. Chat minimum-content penalties, transport conversion and tool flattening do not apply to Decisions. Original caller-rule auditing still precedes business rejection and adaptation. Debug dry interception remains a 422 diagnostic result, without upstream traffic or accounting.

## Configuration and lifecycle

Personal endpoints and models retain all five request-adaptation partitions: forwarded headers, fixed headers, body defaults, forced body values and declared native paths. Model sections inherit or replace the chosen endpoint section. Charity model/binding configuration follows its existing scope. Required inputs, identity, protocol state, authorization and output budgets cannot be overwritten through native paths. Sensitive values actually sent upstream participate in response-reflection checks.

All connectors preserve existing acceptance and charging checkpoints. An accepted charity HTTP 200 stops silent retries and resets the key's failure streak even if later decoding fails. Physical SSE is chargeable at accepted headers; JSON becomes complete only after successful validation. Caller delivery, upstream acceptance and protocol completion remain separate facts. Disconnect cancels upstream work. New route kinds are `openai_decisions` and `charity_decisions`; existing log retention, exports and deletion apply. Continuation signatures, reasoning items and request/response bodies are not added to ordinary logs or account exports.

Protocol references: [Google GenerateContent](https://ai.google.dev/api/generate-content), [thought signatures](https://ai.google.dev/gemini-api/docs/generate-content/thought-signatures), [Responses create](https://developers.openai.com/api/reference/resources/responses/methods/create), [Responses streaming events](https://developers.openai.com/api/reference/resources/responses/streaming-events), and [Decisions create](https://developers.openai.com/api/reference/resources/decisions/methods/create). Compatibility is verified with official structures and controlled HTTP fixtures; this does not establish a provider's model availability or commercial pricing.
