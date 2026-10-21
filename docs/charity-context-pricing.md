# Charity usage and context pricing

Charity models and price templates support three billing modes. Configuration
uses decimal credit strings with at most three fractional digits; public
catalogue fields ending in `_milli` use integer strings in thousandths of a
credit. Counts and thresholds are canonical decimal strings.

| Mode | Management fields | Billing unit |
| --- | --- | --- |
| `per_request` | `user_price,donor_reward` | One chargeable logical request; donor rewards follow chargeable attempts. |
| `per_token` | `user_prices,donor_rewards` | Credits per million tokens, with `uncached_input,cache_write_input,cache_read_input,output`. |
| `per_search_unit` | `user_price,donor_reward` | Credits per reported search unit. |

Search units have exact millionth-unit precision. A credible search-unit report
can be billed and rewarded even when token usage is unknown; token quotas still
use their conservative reservation in that case. Missing units are not zero and
are never inferred from document count. Unknown billable usage retains the
existing conservative charge and no usage-based reward. Discounts apply only to
caller charges.

`per_search_unit` model configuration requires `model_types:["rerank"]` without
other services. For example, its complete scalar pricing value can be
`{"mode":"per_search_unit","user_price":"3","donor_reward":"2"}`.
These are per-unit prices, not amounts per million units.

## Independent input and output modality prices

Token mode optionally accepts `user_modalities` and `donor_modalities`.
Each has required booleans `input_enabled,output_enabled` and optional
`text,image,audio,video` entries. Each supplied entry contains the same complete
four-price object as `user_prices`. Explicit zero is a price; a missing entry
is not. Both ordinary four-bucket price objects remain required as fallbacks.

The following is a complete `pricing` value, to place in an otherwise valid
model/template request:

```json
{
  "mode": "per_token",
  "user_prices": {"uncached_input":"10","cache_write_input":"12","cache_read_input":"1","output":"30"},
  "donor_rewards": {"uncached_input":"5","cache_write_input":"6","cache_read_input":"0.5","output":"15"},
  "user_modalities": {
    "input_enabled": true,
    "output_enabled": false,
    "text": {"uncached_input":"10","cache_write_input":"12","cache_read_input":"1","output":"30"},
    "image": {"uncached_input":"20","cache_write_input":"24","cache_read_input":"2","output":"60"}
  },
  "donor_modalities": null,
  "long_context": null,
  "context_penalty": null
}
```

Only standard upstream classifications can select modality prices. Each side
must have complete, valid counts aligned to its aggregate and a price for every
used modality. Input additionally needs the explicit cross-split between
modality and uncached/cache-write/cache-read tokens; separate modality totals
and cache totals cannot be apportioned. Reasoning tokens follow the protocol's
inclusion rules and are not counted twice. A report of zero with no
classification remains missing classification.

Input and output are evaluated independently for charges and rewards. An
unusable input classification falls back to the three ordinary input prices
while valid output classification can still use modality prices, and vice
versa. A missing user price does not force the donor side to fall back.
Classification failure neither changes credible aggregate counts nor makes an
otherwise valid response fail. Across chargeable attempts, every report must
support the corresponding classification; missing details are not filled from
another attempt.

The caller's charge uses exact sums across chargeable attempts, then rounds up
once to a thousandth of a credit. The frozen discount is applied with the
existing ceiling rule. Donor rewards use each attempt's own valid usage and
reward settings. Existing balance, maximum-charge and reservation constraints
remain in force.

## Long context and extra-long penalty

`long_context` and `context_penalty` are independent optional settings.
Thresholds count all reported input tokens, including cache writes and reads;
equality activates the option. Output does not count. Unknown usage is never
estimated from the request and does not by itself trigger a threshold.

Long-context pricing replaces the applicable prices for all input and output,
including tokens below the threshold. Token mode requires alternate
`user_prices`; request mode requires `user_price`. Optional
`donor_rewards` (token mode) or `donor_reward` (request mode) replaces the base
reward at that tier. Omission preserves the original reward behavior. Token
tiers may also set `user_modalities` and `donor_modalities` independently.
The selected tier's ordinary prices remain its classification fallbacks.
A tier without user modality settings uses its aggregate prices. If neither
donor override is supplied, base reward settings, including base modalities,
remain effective. Search-unit mode rejects a non-null long-context tier with
400; it can retain a context penalty, which still requires reported input tokens
to reach its threshold.

For example, these optional fields belong in a complete token-pricing value:

```json
{
  "long_context": {
    "input_threshold": "100000",
    "user_prices": {
      "uncached_input": "20",
      "cache_write_input": "24",
      "cache_read_input": "2",
      "output": "60"
    },
    "donor_rewards": {
      "uncached_input": "10",
      "cache_write_input": "12",
      "cache_read_input": "1",
      "output": "30"
    }
  },
  "context_penalty": {
    "input_threshold": "200000",
    "fine": "500"
  }
}
```

The penalty adds one separate charge to the logical request, after the ordinary
discounted charge. It receives no discount and increases neither donor rewards
nor donated-key price consumption. Retries and recovery do not apply it twice.

## Reservation, replacement and public projections

Acceptance freezes prices, modality settings, context settings and discount.
Token and search-unit modes use the model's optional `token_reserve_credits`
or the global Token reserve. Request mode reserves its discounted request price.
For a supplied output-token limit in token mode, output reservation considers
the largest enabled user output rate across ordinary, modality and long-context
prices. Donor rates and disabled output-modality prices do not enlarge that
floor. Known actual usage may cost more than the reservation.

A failed generation keeps its failure result. If it crossed the existing
chargeable-response checkpoint, valid standard usage still settles the call,
including reasoning-only usage on an unsuccessful Gateway finish. An abnormal
finish is distinguished from malformed protocol structure. Credible usage is
retained independently of success.

Omitting or setting `user_modalities,donor_modalities,long_context` or
`context_penalty` to null in a complete pricing replacement removes that
option. Omitting the entire `pricing` field from a model PATCH preserves it.
Modality options are accepted only in token mode. Thresholds are positive
int64 strings; price/reward/fine values range from 0 through 9,000,000,000,000
credits inclusive, with at most three decimal places. Strict
unknown-field, expected-revision, authorization and idempotency checks apply.
Accepted snapshots and previously settled history are not repriced.

Public pricing exposes `user_modalities_milli`,
`discounted_user_modalities_milli` and `current_user_modalities_milli` when
configured, including within `long_context`. Their boolean switches and
modality/four-bucket shape match management configuration; prices are integer
milli-credit strings. Scalar modes use `user_price_milli` and its discounted/
current counterparts. Public responses omit donor rewards and reserve settings.
`context_penalty` exposes `input_threshold` and `fine_milli`; long-context
scalar/four-bucket prices use the same milli-credit and current-price fields.
See [discount precedence](charity-discounts.md).

Request and attempt logs can show exact search units, available modality counts
and the caller pricing decision. Side modes are `aggregate` or `modality`;
fallback reasons are the closed values `missing_details`, `invalid_details`,
`incomplete_details`, `unknown_modality`, `cache_details_missing`, `total_mismatch`,
`reasoning_details_missing` and `missing_price`. Older rows may have no details;
this does not mean zero usage. Reward decisions remain internal to settlement
and are not exposed in ordinary user logs. Recovery reuses frozen decisions and
accepts older empty decision metadata without repricing it. Account export
contains only the caller's safe combined usage, without per-model prices,
donor decisions or other users' identities.

## Price-template copying

The administrator charity workspace has a separate Price templates tab alongside
the review queue, charity models and source views. Create, edit, enable, disable
or delete templates there without opening a model. Search and state filters keep
the list manageable, with page-size and direct-page controls. The editor exposes
the complete pricing fields. A model draft remains available while visiting the
template manager and returning to that model. Model forms select an existing
template and keep their own draft values until explicitly saved.

Administrator-maintained price templates can fill the pricing section of a new
or existing model for administrators and current level-6 stewards. They copy the
billing mode, all active user prices and donor rewards, Token reserve override,
input/output modality settings, long-context threshold/alternate prices and
optional rewards, penalty threshold/fine, and every
fixed and recurring discount field. Disabling long-context pricing or the penalty
clears its old fields; switching mode
clears inactive prices. Null reserve means inherit; false, zero and null discount
values replace the form rather than being skipped. Both discount configurations
are copied completely, including a disabled recurring schedule's saved periods.
The form remains editable and
requires an explicit ordinary model save. Templates carry no model name, source
or binding, and later edits never update models automatically. Level 5 and
CallerKeys cannot use them. See [complete template fields and routes](api-contract.md#741-price-templates).
