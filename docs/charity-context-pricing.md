# Charity context pricing

A charity model can independently enable **Long context pricing** and **Extra-long penalty** in its pricing settings. Administrator and steward permissions follow the model's existing pricing permissions. Existing models have both options disabled.

Thresholds count all input tokens reported by the upstream: ordinary input, cache writes and cache reads. Reaching the threshold, including equality, activates the option. Output tokens do not count toward the threshold. Missing usage is never inferred from the request body.

- Long context pricing replaces the prices for **all** input and output, including tokens below the threshold. Token billing supplies four alternate prices; per-request billing supplies one alternate request price. Existing discounts and rounding apply.
- Extra-long penalty adds a separate one-time credit charge to the logical request. It is independent of long context pricing and receives no discount. It does not increase donor rewards or donated-key price consumption.
- Donor reward settings remain unchanged.

The request freezes both configurations and its discount when accepted. Reported input is accumulated using the existing logical-request usage rules; retries and recovery do not charge the fine twice. Requests without a chargeable upstream response retain the existing release behavior. Unknown usage keeps the existing conservative billing behavior and does not, by itself, trigger either threshold. Ordinary reservation amounts are unchanged; actual reported usage may cost more than the reservation.

The public model catalogue and capability response show enabled thresholds and applicable prices before use. Account export continues to include personal charity totals, incorporating the new charges without disclosing model, donor or other users' identifiers.

## Management API

The existing complete `pricing` object accepts two optional fields:

```json
{
  "long_context": {
    "input_threshold": "100000",
    "user_prices": {
      "uncached_input": "20",
      "cache_write_input": "24",
      "cache_read_input": "2",
      "output": "60"
    }
  },
  "context_penalty": {
    "input_threshold": "200000",
    "fine": "500"
  }
}
```

This fragment belongs inside a complete token-pricing object; it is not a standalone PATCH body. Token prices are credits per million tokens, using the existing pricing dimensions and three-decimal precision. Per-request pricing uses `"user_price":"20"` instead of `user_prices`. Thresholds are canonical positive int64 decimal strings. Money fields use the existing canonical decimal-credit format and bounds.

Omitting or setting an option to null in a complete pricing replacement disables that option. Omitting the entire pricing object from a model PATCH preserves it. Normal expected-revision, idempotency, authorization, size and strict unknown-field checks apply.

Management responses return configured credit strings. Public pricing responses use optional `long_context` and `context_penalty` fields: the former contains `input_threshold`, original milli-prices, historical fixed-percentage `discounted_*` prices and effective `current_user_*` prices, with irrelevant mode fields null; the latter contains `input_threshold,fine_milli`. See [discount precedence and current-price fields](charity-discounts.md). Public responses expose no donor reward or reserve configuration.

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
long-context threshold/alternate prices, penalty threshold/fine, and every
fixed and recurring discount field. Disabling long-context pricing or the penalty
clears its old fields; switching mode
clears inactive prices. Null reserve means inherit; false, zero and null discount
values replace the form rather than being skipped. Both discount configurations
are copied completely, including a disabled recurring schedule's saved periods.
The form remains editable and
requires an explicit ordinary model save. Templates carry no model name, source
or binding, and later edits never update models automatically. Level 5 and
CallerKeys cannot use them. See [complete template fields and routes](api-contract.md#741-price-templates).
