# Database access and model discovery

The service uses SQLite WAL with one business connection and two read-only
connections. `Store.DB()` owns authentication touches, mutations, dispatch,
accounting and lifecycle work. `Store.ReadDB()` opens the same validated database
with `mode=ro` and `query_only`; repositories must not send writes to it. Both
pools belong to the store and close with the application.

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
