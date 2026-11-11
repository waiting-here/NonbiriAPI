# Query inventory

Run `go run ./internal/citools/queryaudit . /path/to/inventory.json` from the
repository root. This inventories production Go query sites, enclosing functions,
calls, explicit deadlines, and HTTP route **declarations** using the Go AST.
Tests, CI tools and the third-party SQLite adapter are excluded.

Use this with the query performance review, not as a pass/fail performance score:

* Expand dynamic route loops and domain wrappers; declarations are not unique
  HTTP endpoints. Review summary and event routes independently.
* Separate indexed point reads, hard-capped configurations, page selections,
  aggregates, background maintenance and bounded exports.
* Trace the actual pool, authorization snapshot, deadline and all result reads.
* Inspect full builders and constants for dynamic SQL. `{{...}}` marks unresolved
  expressions; compound assignments and runtime branches need manual review.
  Method-name discovery can include non-SQL methods called `Query` or `Prepare`.
* Use actual SQLite plans and paired scale tests for growth-sensitive queries.
  An inventory entry is not evidence that a query is fast or has been reviewed.

New pageable reads should use `queryread.Page` / `Window` / `Collect`; pure reads
can use `queryread.Snapshot` with their domain authorizer. Select narrow IDs before
fetching large payloads, batch selected-page projections, and preserve explicit
unique ordering. Keep legitimate lazy writes on their write transaction.
