# SQLite adapter cancellation patch

This package is the Go `database/sql` adapter from **modernc.org/sqlite v1.61.1**,
under the accompanying BSD license. `upstream.json` records the original hashes.
The SQLite engine, libc, and virtual-table API remain the pinned upstream Go
modules; the generated engine is not copied or modified here.

Local changes are deliberately limited to removing canonical import comments
and teaching `rows.step` to check cancellation during VM execution. Upstream
v1.61.1 watches lock waits in `Rows.Next`, but its CPU interrupt watcher ends when
`QueryContext` returns the first row. Sparse scans can then occupy a connection
well beyond the caller's deadline. A progress callback checks the current row's
context every 1,000 VM instructions. The connection registers once for this
callback and removes that registration on close. The callback runs on the executing goroutine and is
removed before returning, including on errors. Interleaved cursors do not share
an asynchronous cancellation watcher. Existing lock-wait cancellation is kept.

Use this package for the adapter, function registration, error types, and
connectors. Do not also import the upstream root package: both register the
`sqlite` driver. Imports of upstream `lib` and `vtab` remain intentional.

The patch does not preempt a Go SQL function while that function is executing,
nor promise bounded filesystem I/O. Application functions must remain bounded.
Cancellation is a resource-safety backstop, not a substitute for efficient SQL.

On an upstream upgrade, compare all hashes, rebase this small patch, keep the
engine/libc versions aligned, and run the adapter, DB cache/locking/cancellation,
upgrade, full application, and affected race tests. Remove the local adapter
once upstream provides equivalent row-step cancellation and those tests pass.

`local_files` in the manifest locks the reviewed adapter contents. The provenance
test detects extra files and drift; updating hashes alone does not verify a patch.

The standard Go gate runs all vet analyzers on application code. Only for this
adapter it excludes `unsafeptr`, which flags the inherited C ABI conversions
throughout modernc's wrapper; all other analyzers still run. The new row-context
patch introduces no unsafe pointer conversion. Keep this exception confined to
the adapter, and retain provenance checks and the affected race tests.
