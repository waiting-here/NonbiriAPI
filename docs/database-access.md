# Database access and model discovery

The service uses SQLite WAL with one business connection and two read-only
connections. `Store.DB()` owns authentication touches, mutations, dispatch,
accounting and lifecycle work. `Store.ReadDB()` opens the same validated database
with `mode=ro` and `query_only`; repositories must not send writes to it. Both
pools belong to the store and close with the application.

## Scoped charity catalog checks

Charity-model foreign-key references in reservations, dispatch receipts,
affinities and managed-header sessions/uses have covering indexes. Deletion
locates the affected rows without scanning unrelated history; cascading and
retained-reference behavior still follows the existing foreign keys.

Binding, candidate, manual-entry and preset paths query the selected donation
member or key when checking catalog existence. They combine its automatic and
manual entries within the current scope instead of repeatedly aggregating the
global catalog. This shortens work inside binding transactions as unrelated
catalogs grow. Source eligibility, ordering, revisions, atomicity and current
permission checks remain in those transactions; no catalog-result cache or new
schema is introduced by this optimization.

## Prepared statements and configuration reads

Writable connections retain at most 128 prepared single-statement
`INSERT`, `UPDATE` or `DELETE` templates and 64 `SELECT` templates, each at most
16 KiB of SQL text. A
connection-local LRU closes evicted statements; execution errors discard the
affected statement, and closing or replacing a connection releases its complete
set. Preparation uses the already acquired physical connection, so it never
borrows a second writer during a transaction. The driver binds current arguments
and resets each execution. An open result cursor pins its SELECT statement;
nested use of the same SQL or a full set of pinned statements uses the ordinary
driver query path. Early close releases the pin, while query errors or cancellation
discard the statement. Column metadata and schema re-preparation retain driver
behavior. Scripts and explicitly prepared statements keep their normal ownership.
Transactions, constraints, triggers and SQLite
FULL synchronization remain unchanged; no results or parameter values are cached.

Independent `Store` configuration getters read the full current `site_config`
snapshot through the existing read pool. A store retains one privately owned
validated snapshot and reuses deterministic validation only when every key and
value still matches. Reads do not rely on a TTL or revision counter, and callers
receive separate maps. New, missing or invalid contents are checked immediately;
database failures cannot fall back to an older snapshot. Configuration mutations
continue to validate the proposed full snapshot in their writer transaction.
Domain readiness, authorization, quotas and model availability are not cached.

## Synchronous completion batches

Attempt and request completion share the writer in batches of at most four
operations. Each operation still checks current durable state and performs its
accounting in the transaction. A savepoint isolates each body when work is
batched; success is returned only after the outer commit succeeds. Failed outer
transactions release the connection before retrying each idempotent completion
independently. Acceptance, reservation, dispatch and response-start checkpoints
keep their original transaction boundaries.

There is no delay to fill a batch. With no waiting peer, the operation uses a
single transaction without savepoints. With a backlog, the worker stops taking
more items after eight milliseconds of connection holding, or four items,
whichever comes first; it cannot interrupt an individual SQL statement to meet
that budget. At most 256 tasks wait behind the worker. Overflow uses the original
synchronous transaction path and the caller's existing deadline, rather than
rejecting an already admitted settlement. Each caller retains its settlement
context. Cancellation observed before commit rolls back the batch; remaining
live callers retry with their own original contexts. Once commit has started,
a cancellation response cannot prove that no changes were committed. Subsequent
completion follows the persisted state and the existing idempotency rules.

Canceled waiting tasks are removed promptly. Shutdown stops new completion
submissions and joins both queued work and synchronous overflow before the store
closes. A timed-out shutdown can be awaited again. The queue holds no durable
acknowledgements or deferred accounting; crash recovery uses the existing stored
claim and ledger state.

## Inference preflight and snapshots

Personal and charity route preflight, candidate snapshots and request-policy
reads use the existing read-only pool. Final authorization, quota reservation
and dispatch still run through the business transaction and recheck current
state. This does not enlarge either pool or relax the dispatch checks.

Request-adaptation reads copy the bounded encrypted values, close the read
cursor and transaction, then decrypt and decode them. Batch reads retain one
database snapshot; decoding errors and cancellation still fail the batch.

A charity candidate snapshot reuses validated token budgets and recurring-quota
availability for the same physical key within that transaction. Model reserve
amounts and decision time remain fixed during the snapshot. Each candidate still
checks its own ownership, expiry, backoff, capacity and ordering. Results never
outlive the transaction or replace dispatch-time reservation checks.

## Model directories

`GET /v1/models` combines an owner-scoped personal-model read with a charity
read. The charity portion uses one transaction for the current level, feature
gate, selected model facts and candidate availability. The legacy
`GET /api/charity/models` also reads donation intake and batched recent-success
statistics in that snapshot. The paginated directory keeps its existing single
snapshot and five-second budget, including connection acquisition.

These are current reads, with no full-response cache, TTL or invalidation bus.
Six levels do not imply six interchangeable responses: personal models and the
exclusion of one's own donations depend on identity. The non-administrator
level-six exception remains in force. The public catalog's resource availability
is global; its level filter and caller-facing explanation are separate facts.

All three charity views share the existing set-based quota predicates, including
cumulative limits, recurring windows, pending reservations and split token
budgets. A single decision time is used throughout each charity response. The
caller lists count through the candidate ceiling and return the existing error
on overflow; the global catalog marks that resource set unavailable. Model-list
limits, ordering, fields and `Cache-Control: no-store` remain unchanged. SQL
failures propagate as errors rather than empty lists.

Browsing does not materialize donation expiry. Expiry timestamps exclude due
keys immediately, and existing lifecycle workers and dispatch paths advance
stored state. A later request observes committed model, binding, switch and
quota changes. A read already in progress completes against its own snapshot.

## Game checkpoints and activity snapshots

Lake Notes checkpoints reuse the profile, cast and eligibility/settings already
read or saved in the same transaction. The response keeps the newly committed
profile/cast revisions and omits the active cast after a terminal result. Wallet
balances are still read authoritatively. Request canonicalization and its keyed
digest run before connection acquisition; errors retain authorization precedence,
and replay reservation/completion remain in the business transaction.

Activity event projections use the existing read-only pool. Their configuration,
account facts and event revision come from one committed snapshot. Publisher
generation checks still reject stale publication plans. Reader waits are reported
alongside writer waits; this does not increase either pool size.

## Retention and measurement

Fishing cleanup selects `settled_at <= cutoff`, ordered by `settled_at,id`, using
the existing covering index. The schema requires `settled_at` to be NULL for
reserved batches. Each deletion still checks terminal state and cutoff within
the same transaction. Batch size (100), retention (30 days), retry rules and
worker frequency are unchanged; no schema or index migration is needed.

Pool waiting is distinct from transaction execution. Fixed operation labels
measure connection acquisition, work, failure and cancellation for session
authentication, model directories and affected maintenance workers. See
[runtime metrics](deployment.md) for window semantics and
[performance comparisons](../CONTRIBUTING.md#performance-comparisons) for the
temporary-database workload and its limits.
The [Lake Notes mixed-load experiment](performance-validation.md) adds real
checkpoints, save-size gradients and subscription fan-out.

A measured transaction owns an explicit connection until its finish callback.
Call that callback after commit or rollback and before a follow-up operation
uses the same pool, including capacity alerts. A transaction rollback alone
does not return an explicitly acquired connection.
