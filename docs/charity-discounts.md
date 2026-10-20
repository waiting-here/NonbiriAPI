# Charity discounts

Charity models and complete price templates support a fixed time window and a
recurring weekly schedule. Percentages are the amount payable: 100 means full
price, 50 means half price, and 0 makes the usage charge free. A separate context
penalty is never discounted.

An enabled fixed-window discount takes priority for the entire time it remains
enabled. Inside its window, its percentage applies; outside the window, full
price applies. The recurring schedule stays paused even after the fixed window
ends. Turn off the fixed-window discount to resume the recurring schedule.

## Weekly configuration

The model and template object accepts this top-level field:

```json
{
  "recurring_discount": {
    "enabled": true,
    "time_zone": "Asia/Shanghai",
    "default_percent": 50,
    "periods": [
      {
        "weekdays": [1, 2, 3, 4, 5],
        "start_time": "09:00",
        "end_time": "12:00",
        "percent": 100
      },
      {
        "weekdays": [1, 2, 3, 4, 5],
        "start_time": "14:00",
        "end_time": "18:00",
        "percent": 100
      }
    ]
  }
}
```

This example charges full price during two weekday windows and half price at all
other times. It applies to any model. There is no provider-specific behavior or
holiday calendar; special dates can be handled with the fixed-window discount.

- `time_zone` uses the application's embedded IANA time-zone list.
- `weekdays` contains unique integers, with Monday 1 through Sunday 7.
- At most 16 periods are allowed. Every period requires all four fields.
- Times use exact `HH:mm`. Start times range from `00:00` to `23:59`; end times
  additionally accept `24:00`. Use `00:00`–`24:00` for a whole day.
- A period whose end is earlier than its start crosses midnight and belongs to
  its starting weekday. For example, Monday `22:00`–`02:00` includes early Tuesday.
- Starts are inclusive and ends exclusive. Equal start/end values and overlapping
  periods are rejected, including overlaps across Sunday and Monday. Adjacent
  periods are allowed. All percentages are integers from 0 through 100.
- `default_percent` applies outside the configured periods. An empty list applies
  that percentage throughout the week.

Schedules follow local wall-clock time. During a daylight-saving fall-back, both
occurrences of a repeated hour match. A skipped hour in a spring-forward has no
matching instant. Disabling a schedule preserves its valid configuration.

The default is
`{"enabled":false,"time_zone":"UTC","default_percent":100,"periods":[]}`.
Model creation uses that default when the field is omitted. Model PATCH preserves
the existing value when omitted and replaces the whole object when supplied.
Template POST and PUT use the default when omitted: PUT is a complete replacement,
so an older client omitting this field clears the previous recurring schedule.
All object fields are required when supplied; null, duplicate keys and unknown
fields are rejected. The configuration is limited to 4 KiB, within the existing
request-body limit. Disabled configurations are validated too.

Administrators and level-6 stewards use their existing model pricing permissions.
Level 5 cannot change these settings. Template maintenance remains administrator
only, with enabled-template reads for level-6 sessions. Applying a template copies
both discount configurations completely; later template edits do not change a
model.

## Current prices and settlement

Public capability and catalog models return `recurring_discount` and
`effective_discount`:

```json
{"source":"recurring","percent":50,"next_change_at":1802998800}
```

`source` is `none`, `fixed` or `recurring`. An enabled fixed discount outside its
window still reports `fixed`, with percentage 100. `next_change_at` is the next
Unix second at which the payable percentage actually changes, or null when there
is no future change. Equal-percentage boundaries are skipped.

Pricing returns `current_user_price_milli` for request pricing or
`current_user_prices_milli` for the four Token buckets, with the other branch
null. Long-context pricing has the same current-price fields. These values use
the response's `server_now` and the same integer ceiling as billing.

The existing `discounted_user_price_milli` and `discounted_user_prices_milli`
fields retain their historical meaning: prices calculated with the configured
fixed percentage, independently of whether that window is currently active.
Use `current_user_*` for the amount currently payable. Management model
projections also return `effective_discount` and `server_now`; templates return
configuration only. Idempotent write retries return their original price snapshot.
Older model receipts gain the disabled recurring default and an effective-discount
projection at their original `updated_at`; stored receipt bytes and expiry stay
unchanged. Fetch the model again to obtain its current price.

Acceptance freezes the effective percentage. Crossing a schedule boundary,
editing settings, retrying or recovering a request does not change its captured
price. The discount applies once to the selected base or long-context usage
price. Per-request credit reservations use the effective price. Token reservation
amounts and output reservation floors remain undiscounted. Donor rewards,
donated-key quota accounting, context penalties and unknown-usage settlement
retain their existing rules.
