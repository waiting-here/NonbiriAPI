# Performance validation

Run synthetic experiments against temporary databases and a local mock upstream.
Never point these tests at a deployed instance. Default admission limits remain
enabled; a rejected request is not counted as accepted throughput.

For focused read-path measurements, start with the bounded SQLite benchmarks:

```sh
CGO_ENABLED=0 GOMAXPROCS=2 go test -p 1 ./internal/game/blackjack \
  -run '^$' -bench '^BenchmarkHistorySparseUser$' -benchtime=10x -count=1
CGO_ENABLED=0 GOMAXPROCS=2 go test -p 1 ./internal/riskaudit \
  -run '^$' -bench '^BenchmarkRiskUsersSQLite$' -benchtime=3x -count=1
CGO_ENABLED=0 GOMAXPROCS=2 go test -p 1 ./internal/riskaudit \
  -run '^$' -bench '^BenchmarkRiskSourceWatermarkSQLite$' -benchtime=10x -count=1
NONBIRI_PERFORMANCE_SAMPLE=1 CGO_ENABLED=0 GOMAXPROCS=2 go test -p 1 \
  ./internal/donation ./internal/resources \
  -run '^Test(DonationPage|CallerKey)PerformanceSample$' -benchtime=3x -count=1 -v
```

The history benchmark keeps three owned entries while unrelated retained tables
grow from 1,000 to 50,000. It measures the candidate query, excluding full history
projection. The users benchmark measures the repository method with 10,000 or
100,000 minute observations for 100 users. The watermark benchmark isolates the
two maximum-ID queries with equivalent indexes; it excludes scan creation and
its time-window count. Fixture creation is outside the timed work. Report these
scopes explicitly and compare the same inputs before and after a change.

The opt-in donation sample reads a full 100-row page with three sources per
donation, then adds 2,000 unrelated historical donations. It records query count,
elapsed time and allocations separately. The caller-key sample measures the two
independent lookups performed during request authentication; it does not model
writer contention.

Read-pool regressions separately verify that a held, authorized read snapshot
does not consume the writer, retains consistent counts and results through a
concurrent commit, and rechecks current permissions on the next request. An
uncontended lookup microbenchmark alone does not demonstrate reduced contention.

For streaming accumulation, ordered failure folding and writer ownership:

```sh
CGO_ENABLED=0 GOMAXPROCS=2 go test -p 1 ./internal/connector/openai \
  -run '^$' -bench '^BenchmarkStreamAccumulation$' -benchtime=3x -count=1
NONBIRI_PERFORMANCE_SAMPLE=1 CGO_ENABLED=0 GOMAXPROCS=2 go test -p 1 \
  ./internal/charity -run '^Test(Fold|ServiceFold)PerformanceSample$' \
  -benchtime=3x -count=1 -v
NONBIRI_PERFORMANCE_SAMPLE=1 CGO_ENABLED=0 GOMAXPROCS=2 go test -p 1 \
  ./internal/clientguard ./internal/game/ranking \
  -run '^Test(ClientGuard|Ranking)ReaderPerformanceSample$' \
  -benchtime=3x -count=1 -v
```

The stream benchmark exercises the real Responses and flattened-chat consumers,
including terminal output, with 64 KiB or 512 KiB of text in 32- or 512-byte
deltas. It excludes networking, upstream generation and fixture construction;
allocated bytes are cumulative allocations, not peak memory. The key-fold sample
uses 1, 32 or 256 contiguous completed results. The service-fold sample uses 32
real dispatch associations, completing the leading gap in the timed transaction.
Both roll back each iteration to preserve identical inputs and exclude unrelated
completion/ledger work.

Client-rule and ranking samples compare the same work on the writer and reader
pools, with 0/100 rules and 1,000/20,000 eligible users. They do not model
production contention. Separate regressions hold or observe the writer, verify
concurrent write progress and fresh permission checks, and ensure MFA response
output begins only after its snapshot is released. Keep these ownership results
separate from small uncontended timing differences.

For a broader mixed workload:

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

Set `NONBIRI_LOAD_OVERLAP=1` to add 1,001 expired terminal fishing batches at the
midpoint, create an actual broadcast reward through the administrator HTTP
handler, and run the normal recovery/retention order while checkpoints, pages
and model completions continue. The existing reward worker delivers the grant.
The report records seeded/remaining history, matched/successful recipients,
fixture-seeding time, maintenance-pass time and total overlap completion time;
all deliveries and accounting must converge. The artificial history insertion
also competes for the writer. Compare this mode only with another run that
enables the same overlap.

`LAKE_LOAD_RESULT` reports planned and submitted work, HTTP results, model
admission, queue delay, both pool waits, connection-holding phases, process CPU
time and memory. Model error codes and anonymous rejection-stage totals help
distinguish admission limits from unexpected service failures; HTTP 503 alone
does not establish a capacity rejection. Game profiles must match their committed revisions and JSON;
model claims, reservations and ledger recovery must converge. Transaction rows
overlap their phase rows and must not be summed with them. The operation window
contains only samples since the observer's last drain.

The default measurement window is 30 seconds per case. For fixture diagnostics,
`NONBIRI_LOAD_SECONDS` accepts an even value between 6 and 60. Short diagnostic
runs are not release performance evidence. The existing
`TestApplicationCatalogLoad` and `TestApplicationSyntheticLoad` cover page-only,
long-stream, concentrated completion, slow-reader and cancellation scenarios.

The streaming experiment reports `pages` grouped by phase and route, and
`pages_overall` grouped by route across the complete measurement window. A
completion phase may contain only a few page requests: its P95 can be its maximum
and must not be presented as a whole-window percentile. Always report the sample
count. CPU time includes the in-process load clients and mock upstream; Go heap
snapshots are neither process RSS nor a peak memory measurement.

Completion queue, execution, shared transaction and commit rows appear in
`last_window_operations`, alongside attempted batch-size counts, overflow and
independent recovery. This remains the observer's most recent window, not an
invented sum across its automatic minute drains. Inspect any emitted runtime
windows too when a case spans a drain. Queue and pool waiting overlap. Check
handler drain, caller percentiles and accounting convergence before attributing
an improvement to reduced waiting.

Compare identical seeds, data, build inputs, toolchain and machine. Record the
commit/tree and real exit status alongside each result. Report workload mix and
admitted/completed work, not only latency. Release targets are evaluated in the
result report, rather than enforced as machine-speed assertions in unit tests.
