# Gateway model controls

`ai-sdk-gateway-v3` uses the `LanguageModelV3` wire protocol. Gateway implementations can use different provider adapters and model settings. A successful HTTP response alone does not prove that a provider honored an option.

## Output budgets

Both `max_completion_tokens` and `max_tokens` become `maxOutputTokens`, preserving the number. For example, `128000` remains `128000`. Values must be JSON integers from 1 to 2147483647. Null means absent. If both fields are present, their non-null values must match. Conflicts, invalid values and budgets above a configured model limit return `400 invalid_request` before reservation or credential access. No replacement budget is inserted.

## Configure a verified model

The administrator can set `NONBIRI_GATEWAY_MODEL_CAPABILITIES` in the startup environment. It accepts one JSON object, up to 64 KiB and 128 model entries. Changes take effect after restart. Omit it to leave model-specific reasoning and storage controls disabled.

Each entry matches the exact stored endpoint base URL and complete upstream model ID. Trailing URL slashes are ignored; other URL and model text must match. No prefix, wildcard or provider-name inference applies. Matching entries govern both personal and charity bindings for that target.

Example structure for an endpoint whose OpenAI Responses implementation and model capabilities have been verified:

```json
{
  "models": [
    {
      "base_url": "https://gateway.example/native/v3/ai",
      "model": "openai/verified-model",
      "adapter": "openai_responses",
      "efforts": ["low", "medium", "high"],
      "max_output_tokens": 128000,
      "storage": "openai"
    }
  ]
}
```

Replace the example with evidence for the actual gateway, provider implementation, model and route. Verify the receiving provider request or obtain an explicit gateway capability contract. Model discovery and the public SDK types do not establish execution support. No Runable model is enabled automatically.

| Field | Meaning |
| --- | --- |
| `base_url`, `model` | Required exact target |
| `adapter` | Required profile from the table below |
| `efforts` | Verified levels for this model; defaults to empty |
| `max_output_tokens` | Verified maximum preserving the requested budget; positive and at most 2147483647. Required for Anthropic profiles because the provider may clamp known-model budgets. Optional elsewhere; omitted or zero adds no local model ceiling. |
| `storage` | `reject` by default, `openai`, or explicit lossy `omit_false` |

## Reasoning

The caller sends `reasoning_effort`. Its value must belong to both the profile vocabulary and that model's configured `efforts`. An unsupported value rejects explicitly. It never becomes a token budget or falls back to another effort.

| Adapter | Profile vocabulary | Native provider options |
| --- | --- | --- |
| `openai_chat` | none, minimal, low, medium, high, xhigh | `openai.reasoningEffort` |
| `openai_responses` | none, minimal, low, medium, high, xhigh, max | `openai.reasoningEffort` |
| `anthropic_effort` | low, medium, high, max | `anthropic.effort` |
| `anthropic_adaptive` | low, medium, high, max | `anthropic.effort` and `anthropic.thinking:{"type":"adaptive"}` |
| `anthropic_always_adaptive` | low, medium, high, xhigh, max | The same namespace; adaptive thinking on every request |

The first four profiles describe the [pinned provider implementation](https://github.com/vercel/ai/tree/035d5ad533901406c81cfe58494d6ea8579d68d4/packages), not every model. The configured model subset is authoritative. In particular, the pinned OpenAI Chat adapter rejects `max`; the Responses adapter can express it, but the selected model and gateway must execute it.

For OpenAI profiles, a nonempty effort list declares a reasoning model. All its requests send `openai.forceReasoning:true` so an adapter's model-name recognizer cannot silently discard the control, including requests using legacy `max_tokens`. `openai.systemMessageMode:"system"` preserves the roles already produced by the platform role policy. Configuring only storage does not declare a reasoning model.

The always-adaptive profile follows [Anthropic provider 3.0.127, still LanguageModelV3](https://github.com/vercel/ai/blob/def2c64454c32abc35b03b412f8127b8f360ce5e/packages/anthropic/src/anthropic-messages-language-model.ts). It supports the newer effort vocabulary, rejects sampling on every request, and rejects required/named tool choices that the provider would weaken to auto. Native Bedrock Anthropic uses this canonical namespace too; Bedrock Converse uses a different mapping and is not covered by this profile.

Anthropic effort-only does not turn on thinking. The adaptive profile enables it when an effort is requested; the always-adaptive profile enables it on every request. Fixed thinking budgets are not synthesized or added to the caller's output budget. Configure the actual preserving maximum, including any lower adapter limit.

Explicit fields that the configured provider would discard or clamp reject before dispatch. OpenAI Chat rejects top-k; reasoning profiles also reject temperature, top-p, and presence/frequency penalties. Responses rejects top-k, seed, stop sequences and presence/frequency penalties; reasoning profiles also reject temperature and top-p. These conservative profiles do not model special sampling exceptions for individual OpenAI models. Anthropic rejects presence/frequency penalties and seed; without adaptive thinking it requires temperature in 0–1 and rejects simultaneous temperature and top-p. With adaptive thinking it rejects temperature, top-p and top-k.

## Storage

| Policy | Explicit caller `store` behavior |
| --- | --- |
| `reject` | Both boolean values reject because equivalent behavior has not been verified. |
| `openai` | Sends the boolean as `providerOptions.openai.store`; allowed only on a verified OpenAI profile. |
| `omit_false` | Accepts and omits `false`; rejects `true`. The administrator explicitly accepts loss of the caller's storage control. |

Omitting `store` leaves upstream defaults in effect. The existing physical-key “request no storage” setting remains OpenAI-compatible only; it does not enable Gateway storage controls.

OpenAI storage control governs the provider's documented storage behavior. It does not guarantee removal of abuse-monitoring records or control intermediary retention. `omit_false`, an omitted field, and Vercel's gateway ZDR option do not establish Runable-wide zero retention. See [OpenAI data controls](https://platform.openai.com/docs/guides/your-data) and [Vercel Gateway ZDR](https://vercel.com/docs/ai-gateway/security-and-compliance/zdr) for their respective scopes.

## Existing adaptation and diagnostics

Role policies, intentional field exclusions and body replacement rules still run before target validation. Unknown fields remain unsupported unless covered by an existing explicit native-extension declaration. `reasoning_effort` cannot be declared as a native root: it must pass the model check. Native extensions that would overwrite translated budgets, effort, thinking mode or storage control reject, including Bedrock native overrides of canonical Anthropic options; unrelated native extensions keep their existing behavior.

Candidate checks use the final adapted request. A compatible candidate can remain eligible when another model rejects it. If no candidate is compatible, the response identifies the supported field category and reason. Authorized live Debug traces also identify the rejection stage. These reasons contain fixed text, without field values, message bodies, endpoint/model identities or credentials. Existing response warnings and stream validation remain active.
