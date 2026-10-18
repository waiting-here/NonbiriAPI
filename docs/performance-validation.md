# Performance validation

Run synthetic experiments against temporary databases and a local mock upstream.
Never point these tests at a deployed instance. Default admission limits remain
enabled; a rejected request is not counted as accepted throughput.

```sh
NONBIRI_LOAD_TEST=1 CGO_ENABLED=0 go test ./internal/app \
  -run '^TestApplicationLakeMixedLoad$' -count=1 -v -timeout=8m
```

This experiment includes 220 models, 600 bindings, 70,000 historical fishing
batches and 385,000 outcomes. It offers 15 or 30 page requests per second, a
25- or 50-request directory burst, a simultaneous model-completion burst, and
4, 8, 16 or 32 active Lake Notes players. Every player's checkpoint arrivals
remain two seconds apart, even when a previous request queues. Each player
maintains its confirmed controller state and uses the real HTTP handler and
rules. A new cast receives only its initial control edge until wall time passes.
The test also varies stored basket size and active activity subscriptions.
It publishes current activity snapshots every two seconds to measure projection
fan-out separately from game settlement.

`LAKE_LOAD_RESULT` reports planned and submitted work, HTTP results, model
admission, queue delay, both pool waits, connection-holding phases, process CPU
time and memory. Game profiles must match their committed revisions and JSON;
model claims, reservations and ledger recovery must converge. Transaction rows
overlap their phase rows and must not be summed with them. The operation window
contains only samples since the observer's last drain.

The default measurement window is 30 seconds per case. For fixture diagnostics,
`NONBIRI_LOAD_SECONDS` accepts an even value between 6 and 60. Short diagnostic
runs are not release performance evidence. The existing
`TestApplicationCatalogLoad` and `TestApplicationSyntheticLoad` cover page-only,
long-stream, concentrated completion, slow-reader and cancellation scenarios.

Compare identical seeds, data, build inputs, toolchain and machine. Record the
commit/tree and real exit status alongside each result. Report workload mix and
admitted/completed work, not only latency. Release targets are evaluated in the
result report, rather than enforced as machine-speed assertions in unit tests.
