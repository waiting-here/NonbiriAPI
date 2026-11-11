# Shared query mechanics

Keep business SQL and authorization in their owning domain. Use this package to
share the parts that must behave consistently across domains:

* `Snapshot` starts the read budget before pool acquisition, authorizes on that
  transaction, reads, and commits. Use the existing read pool; never open another
  pool per request. A path that actually expires or mutates state still needs its
  domain's write transaction.
* `Window` counts the complete filtered selection and clamps an exact numbered
  page. `Page` adds the bounded window, or a cursor lookahead row. Both require
  the same transaction for count and data. Cursor identity, retention and all
  authorization predicates belong in the domain's selection.
* Prefer narrow IDs for a costly page, followed by bounded detail/projection
  queries. `Page` accepts an empty outer order only when its result is embedded
  in the domain's own materialized selection with an explicit final order.
* `Collect` closes and checks a bounded page before returning it. It does not
  impose a limit itself; do not use it to load an unbounded export into memory.
  Stream exports with their original limits, context, and `Rows.Err` handling.

SQL fragments come from trusted code. Bind every user value. Use stable ordering
with a unique tie breaker at both the selection and final result. Do not assume
SQLite preserves the order of a CTE or of an `IN` list. Batch only selected-page
IDs; keep grant and ownership decisions inside the current transaction.

The SQLite adapter now checks cancellation during later `Rows.Next` VM steps,
including non-materialized readers. Keep efficient plans nonetheless: a result
limit does not bound a sparse scan, a grouping, or a sort. Full materialization
of an export would trade an interruption problem for memory and temporary I/O.

For a new or changed search:

1. Generate the source inventory with `internal/citools/queryaudit`; identify
   every separate list, summary, count and export route. Expand dynamic wrappers.
2. Review the owning predicates, pool, transaction and deadline. Inspect the
   actual SQLite plan under broad, sparse, owner and time-window filters.
3. Measure the complete result read at more than one relevant size. Check that
   page projections do not grow with unrelated data. Preserve negative results.
4. Exercise cancellation after the first row, connection reuse, current
   authorization after a pool wait, and exact count/order/window behavior.
5. Connect each frontend query to its own initial and retained-result feedback.
   A successful event list does not imply that an independent summary succeeded.

The inventory and common helpers are review tools, not a guarantee that any
arbitrary filter and dataset completes within a fixed budget. Do not substitute
approximate counts, hidden truncation, or a larger timeout for an efficient plan.
