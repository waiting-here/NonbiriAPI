# Chat and Decisions connectors

The connector selected for an endpoint determines its upstream protocol. The same base URL can be registered with different connector types, including mainstream channels. Keys remain bound to their endpoint protocol and encrypted context. The platform does not infer a protocol from a hostname or model name.

| Connector | Platform operation | Upstream resource appended to the versioned base |
| --- | --- | --- |
| `google-gemini` | `/v1/chat/completions` | `/models/{model}:generateContent`, or `:streamGenerateContent?alt=sse` |
| `openai-responses` | `/v1/chat/completions` | `/responses` |
| `openai-decisions` | `/v1/decisions` | `/decisions` |

Gemini and Responses do not serve embeddings, image generation or Decisions. There is no platform `/v1/responses` endpoint or stored upstream conversation. The existing OpenAI-compatible connector does not acquire Decisions support. A platform model must enable the corresponding `model_types` value, and its binding must support that operation.

All three connectors use the shared outbound boundary, cancellation, timeouts, concurrency limits and credential protections. Responses are bounded to 8 MiB for JSON or 32 MiB cumulatively for SSE, with 256 KiB lines and 1 MiB events, or a smaller configured shared limit. Translated output also respects those bounds. Upstream IDs and model metadata are projected to local completion IDs and the requested platform model; explicit continuation fields below retain the opaque information needed by that protocol.

## Gemini Chat

Gemini rejects tool-call flattening during candidate preflight because the text tool format cannot retain Google thought signatures. Both JSON and SSE calls require this option to be disabled.

Configure the complete API mount, for example `https://generativelanguage.googleapis.com/v1beta`. The connector authenticates with `x-goog-api-key`. An explicit operator-configured [Cloudflare route](cloudflare-egress.md) can move this credential into a private outbound query parameter when required by the gateway. Direct requests retain header authentication. Model IDs are one path segment, optionally prefixed once with `models/`.

Supported Chat input includes leading system/developer text, user/assistant text, function definitions and matched tool results, temperature, top-p, stop sequences, one candidate, output-token limits, and streaming. Images must be bounded PNG, JPEG, GIF or WebP base64 data URLs. Arbitrary remote image URLs, audio, files, server-executed tools, `store`, `reasoning_effort`, multiple candidates, strict function schemas and `parallel_tool_calls:false` with tools are incompatible. `response_format` supports `text` and `json_object`; JSON-schema formats are incompatible.

Function arguments must be JSON objects. Tool-call IDs are preserved or generated locally when absent upstream. Every tool result must match an unconsumed earlier call; parallel results retain their IDs and function names. JSON-object results remain objects; ordinary string results use `{ "result": "..." }` in the native function response.

Google thought signatures are retained in `tool_calls[i].extra_content.google.thought_signature`. A signature attached to the final text part uses `message.extra_content.google.thought_signature`; streaming emits the corresponding delta when it arrives. Clients must return these fields unchanged in the assistant history. Signatures are bounded to 64 KiB each and validated as base64. A response requiring multiple separately signed text parts, or another order that the Chat history cannot reproduce, fails explicitly. The platform does not fabricate signatures or use a validation-bypass value. Requests carrying these fields can use only Gemini candidates.

Thought text is returned as `reasoning_content`. Native extensions are limited to `generationConfig.thinkingConfig` (`includeThoughts`, `thinkingBudget` or `thinkingLevel`) and `safetySettings`. Declare these paths in the existing request-adaptation configuration. Output budgets must use Chat token-limit fields; native changes to contents, tool configuration, platform identity or output limits are rejected. The platform pseudonym is sent as a lowercase `labels.safety_identifier` value.

A successful stream requires a recognized candidate finish reason. `STOP` maps to `stop` or `tool_calls`, `MAX_TOKENS` to `length`, and supported safety blocks to `content_filter`. Missing/unknown finish reasons, malformed function calls, content after termination and truncated streams fail. Usage-only tail frames are allowed; EOF alone does not complete a request.

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

Output supports assistant text/refusal, function calls and reasoning summaries. Unsupported output items, annotations requiring another modality or malformed tool arguments fail instead of disappearing. Streaming validates response identity, increasing sequence numbers, item/content indexes and add/delta/done lifecycles. Final snapshots must match the emitted items. `response.completed` maps to `stop` or `tool_calls`; supported `response.incomplete` reasons map to `length` or `content_filter`. Failed events, missing terminal events and open items at termination fail. The connector emits the caller's Chat terminator only after validation.

Usage uses `input_tokens - cached_tokens - cache_write_tokens`, separate cache buckets and `output_tokens`. Reasoning tokens are already included in output and are not added again. Invalid bucket details or overflow make usage unknown. A contradictory total is a diagnostic flag, not a substitute for valid buckets.

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

`input` is a string or supported message/function/custom-tool history. Messages support text and user/tool images; files, audio and item references are rejected. Histories must pair call IDs with their outputs. Questions contain 1–200 predicate, choice or score entries. Choices contain 2–255 unique typed string/boolean values; score levels retain their declared order. Names are optional and positional. Cache key/options follow the native API. The platform replaces `model` and `safety_identifier` at dispatch. Omitted `stream` is normal; `stream:false` is a local convenience and is stripped. `stream:true`, `stream:null`, unknown top-level fields and native extensions are rejected before dispatch.

The response contains `model`, `answers` and valid native `usage` when available. Answer count, ordering, names, types, choice values, score levels and probabilities are checked against the request. Per-question refusals are valid answers. The public model name replaces the physical one. Usage follows the same four-bucket accounting as Responses; missing or malformed usage uses existing conservative settlement.

All questions in one request constitute one logical platform call. Existing per-call or token pricing, quotas, reservation, donor reward, idempotent settlement and account-deletion rules apply; there is no new per-question price. Chat minimum-content penalties, transport conversion and tool flattening do not apply to Decisions. Original caller-rule auditing still precedes business rejection and adaptation. Debug dry interception remains a 422 diagnostic result, without upstream traffic or accounting.

## Configuration and lifecycle

Personal endpoints and models retain all five request-adaptation partitions: forwarded headers, fixed headers, body defaults, forced body values and declared native paths. Model sections inherit or replace the chosen endpoint section. Charity model/binding configuration follows its existing scope. Required inputs, identity, protocol state, authorization and output budgets cannot be overwritten through native paths. Sensitive values actually sent upstream participate in response-reflection checks.

All connectors preserve existing acceptance and charging checkpoints. An accepted charity HTTP 200 stops silent retries and resets the key's failure streak even if later decoding fails. Physical SSE is chargeable at accepted headers; JSON becomes complete only after successful validation. Caller delivery, upstream acceptance and protocol completion remain separate facts. Disconnect cancels upstream work. New route kinds are `openai_decisions` and `charity_decisions`; existing log retention, exports and deletion apply. Continuation signatures, reasoning items and request/response bodies are not added to ordinary logs or account exports.

Protocol references: [Google GenerateContent](https://ai.google.dev/api/generate-content), [thought signatures](https://ai.google.dev/gemini-api/docs/generate-content/thought-signatures), [Responses create](https://developers.openai.com/api/reference/resources/responses/methods/create), [Responses streaming events](https://developers.openai.com/api/reference/resources/responses/streaming-events), and [Decisions create](https://developers.openai.com/api/reference/resources/decisions/methods/create). Compatibility is verified with official structures and controlled HTTP fixtures; this does not establish a provider's model availability or commercial pricing.
