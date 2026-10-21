# Direct inference

This contract describes the v1.1.0 source. An instance must run that version to
provide these additions. All routes use the existing CallerKey, ownership,
model selection, outbound protection, cancellation, accounting and log rules in
the [HTTP contract](api-contract.md).

## Connector selection

Choose the endpoint's protocol explicitly. The registry contains these 12 types;
it does not infer a type from a hostname or retry another API after an upstream
failure. Each upstream model must also support the requested operation.

| Connector type | Supported platform operations |
| --- | --- |
| `openai-compatible` | Native Chat, embeddings, image generation, Responses/compact/input-token counting, legacy completions, image edits/variations, speech/transcription/translation, moderation and compatible rerank. |
| `openai-chat-completions` | Native Chat and the bounded Responses-to-Chat conversion below. |
| `openai-responses` | Native Responses/compact/input-token counting and the bounded Chat-to-Responses conversion below. |
| `anthropic-compatible` | The supported Chat-to-Messages subset. |
| `ai-sdk-gateway-v3` | The supported Gateway v3 Chat and embedding subsets. |
| `google-gemini` | The supported Chat-to-GenerateContent subset. |
| `openai-decisions` | Native Decisions. |
| `typesafe-systemone` | Native System One, with separate model discovery. |
| `cohere-rerank` | Cohere rerank request, result and billed-unit format. |
| `jina-rerank` | Jina rerank request, result and token format. |
| `siliconflow-rerank` | SiliconFlow rerank request, result and token format. |
| `voyage-rerank` | Voyage rerank request, result and token format. |

For OpenAI-shaped direct calls, configure the complete upstream API mount: a base
such as `https://provider.example/v1` receives `/responses`, `/rerank` or the
corresponding resource suffix. The platform does not insert another `/v1` or
accept arbitrary upstream paths. Rerank providers may use a different versioned
mount; select both their explicit connector and their correct base.

Model `model_types` remains an array. Chat Completions and Responses form one
service selection: either legacy name enables both, and responses return both
`chat_completions` and `responses`. Creation without `model_types` uses this pair;
PATCH omission preserves the selection. Other operations remain separate and
may be selected together, for example `images_generations` and `images_edits`.
An enabled service does not add capabilities to its connector. The operation
names are the path names below with `/v1/` removed and remaining slashes replaced
by underscores, except `chat/completions`, which is `chat_completions`.

## HTTP operations

`GET /v1/models` lists routable platform models; `GET /v1/models/{model}` looks up
one exact platform model. Neither exposes physical credentials or proves that
every operation is supported. POST bodies use the platform model name, including
the reserved `[公益]` prefix for charity models.

| POST path | Input and response transport |
| --- | --- |
| `/v1/chat/completions` | Chat JSON; JSON or SSE response. |
| `/v1/responses` | Inline Responses JSON; JSON or SSE response. |
| `/v1/responses/compact` | Inline Responses JSON; JSON response. |
| `/v1/responses/input_tokens` | Inline Responses JSON; JSON token-count response. |
| `/v1/completions` | Legacy completion JSON; JSON or SSE response. |
| `/v1/embeddings` | Embedding JSON; JSON response. |
| `/v1/images/generations` | Generation JSON; JSON or supported image SSE response. |
| `/v1/images/edits` | Inline image URL objects in JSON or multipart image/mask uploads; JSON or supported image SSE response. |
| `/v1/images/variations` | Multipart image upload; JSON response. |
| `/v1/audio/speech` | Text and built-in voice name in JSON; binary audio or `stream_format:"sse"`. |
| `/v1/audio/transcriptions` | Multipart audio file; JSON, text/subtitles, or supported transcription SSE. |
| `/v1/audio/translations` | Multipart audio file; JSON or text/subtitles. |
| `/v1/moderations` | Text/image moderation JSON; JSON response. |
| `/v1/rerank` | Query and documents in JSON; normalized JSON response. |
| `/v1/decisions` | Native Decisions JSON; JSON response. |
| `/v1/systemone` | Native System One JSON or multipart image input; JSON response. |

The shared request limit defaults to 10 MiB and is configurable from 1–64 MiB.
Multipart framing and file bytes count toward the same complete-body limit;
uploads exist only for that request. Normal direct JSON responses are bounded
to 8 MiB and SSE to 32 MiB, with 256 KiB lines and 1 MiB events. Image operations
and speech use their 64 MiB media bound, including cumulative image/speech SSE;
a smaller configured outbound limit still applies. Each logical request keeps
the existing 1,200-second deadline, including retries and streaming, and up to
900 seconds for upstream response headers.

SSE must reach its operation's valid terminal event. HTTP 200, an empty stream
or clean EOF alone does not establish completion. A failed/incomplete generation
may still report valid usage: the outcome remains failed where required by that
protocol, while credible usage remains available for settlement and logs.
Missing usage is not estimated from text, images, audio duration or document
count. Binary and subtitle responses without usage retain unknown usage.
The result of `/v1/responses/input_tokens` counts an input; it is not evidence
that those tokens were consumed by a generation.

For example, a native stateless Responses request body is:

```json
{"model":"provider/model","input":"Explain this in one sentence.","store":false,"background":false,"stream":false}
```

Responses creation always sends `store:false` and `background:false`; omitted
or null lifecycle controls are normalized, and true is rejected. Compact and
input-token counting use their own native endpoints, without those creation
controls. Persistent IDs such as `previous_response_id`, `conversation` and
`file_id`, stored prompts and tools requiring hosted stored resources are
rejected before dispatch. Ordinary text and function parameter schemas are not
treated as resource references. Native stateless features remain subject to the
selected upstream's support. Existing Chat `store:true` compatibility is
unchanged, subject to the physical key's `force_store_false` policy.

## Chat and Responses conversion

Use a native route for features outside the conversion subset. The
`openai-compatible` type sends each protocol natively. The two dedicated types
choose one upstream protocol before dispatch; compact and input-token counting
never convert to Chat. JSON and SSE conversion preserve valid usage, tool-call
associations and protocol termination; unsupported input is rejected before
sending upstream.

With `openai-responses`, Chat messages support text, HTTPS/data images, function
tools and complete matched results, tool choice, parallel tools, temperature,
top-p, reasoning effort, cache key/retention and supported text/JSON formats.
Output limits map to `max_output_tokens`. Omitted Chat function `strict` remains
false. Stop sequences, penalties, logit bias, multiple candidates and built-in
tools cannot be converted. Bounded encrypted reasoning continuation uses the
explicit extension described in [Responses Chat](content-protocols.md#responses-chat).

With `openai-chat-completions`, Responses input supports inline text and URL/data
images, `instructions`, complete function-call/output history, function tools
and choice, `max_output_tokens`, `temperature`, `top_p`, `parallel_tool_calls`
and representable `text.format` values (`text`, `json_object`, `json_schema`).
Omitted Responses function `strict` uses supported strict-schema normalization;
schemas outside that subset need an explicit representable strict setting.
Reasoning/compaction items, audio/files, hosted tools, persistent references and
other unrepresentable fields are rejected. Tool flattening and Chat transport
overrides do not add capabilities to native direct operations.

## Rerank

`/v1/rerank` requires a nonempty `query` and 1–1,000 `documents`. Use only one of
`top_n` and `top_k`, from 1 through the document count. The adapter maps the name
to the selected protocol. Cohere, SiliconFlow and Voyage accept text documents
or single-field `{"text":"..."}` objects; Jina and compatible rerank also accept
document objects. Additional provider options must belong to that connector's
closed field set. Rerank has no SSE mode.

```json
{"model":"provider/reranker","query":"Where is the library?","documents":["The library is on Main Street.","The pool closes at six."],"top_n":1,"return_documents":true}
```

The normalized response uses platform model identity and unique source indexes:

```json
{"object":"list","model":"provider/reranker","results":[{"index":0,"relevance_score":0.95,"document":{"text":"The library is on Main Street."}}],"usage":{"search_units":1.25}}
```

`usage` is optional. Cohere reads `meta.billed_units.search_units`; SiliconFlow
reads `tokens.input_tokens` and `tokens.output_tokens`; Jina, Voyage and compatible
rerank read `usage.total_tokens`. Token reports normalize to
`prompt_tokens,completion_tokens,total_tokens`. Search units retain exact
millionth-unit precision, independently of token availability; invalid, negative,
overflowing or overprecise reports remain unknown. No unit is inferred from the
number of documents. Models may select `per_request`, `per_token` or
`per_search_unit`; see [pricing and fallback rules](charity-context-pricing.md).

## System One and Decisions

These are separate services and connectors. System One uses `state` and a named
`questions` object with `noul`, `choice`, `score` or `box` questions; Decisions
uses its native `input` and question array. They are not converted into each
other. System One discovery reads the upstream `models[].name` values.

```json
{"model":"provider/system-one","state":{"message":"The package arrived."},"questions":{"arrived":{"type":"noul","instructions":"Has the package arrived?"}}}
```

System One accepts structured state and inline PNG/JPEG assets, or a multipart
body whose first part is named `request` with `Content-Type: application/json`,
followed by named image parts referenced through `assets.*.part`.
Images and box questions require `TypeSafe-Danger-Zone-Images: true`.
`TypeSafe-Danger-Zone-Long-Context: true` is the other supported opt-in. Each must
be a single literal `true` value; arbitrary header values are not forwarded.
There is no streaming mode. Standard `usage.input_tokens` and
`usage.output_tokens` feed the existing accounting buckets.

## Admission and excluded resources

Embeddings, rerank, System One and Decisions are light requests; all other model
operations are heavy. Each user has independent light/heavy concurrency pools,
each shared by personal and charity calls. Defaults are 15 light and 5 heavy.
Charity RPM also has independent windows: defaults are 30 light and 60 heavy.
Both classes still share global user/site RPM and physical-key limits. The five
nullable user overrides, their immediate effect and administrator/strictly
higher steward authority are specified in [user limits](api-contract.md#71-session-configuration-users-and-observability)
and [configuration](configuration.md).

All WebSocket, Realtime, Realtime translation, Live and WebRTC interfaces are
deferred. The public surface excludes Files/Uploads resources, saved Chat or
Responses CRUD, Conversations, background jobs/Batches, vector stores,
containers, persistent agent/session management, organization/project/account
management, training/fine-tuning/evaluations, voice/consent management and
content-provenance checks. A file uploaded within a supported inference request
does not create a persistent file resource. Upstream model availability and
retired model lifetimes remain the provider's responsibility.
