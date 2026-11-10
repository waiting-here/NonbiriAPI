package db

// MaterializePage completes a bounded selection before exposing its first row.
// This keeps a sparse scan inside QueryContext's cancellation scope. The caller
// supplies trusted SQL with a result limit and an outer order over its columns;
// user values must remain query parameters.
func MaterializePage(selection, order string) string {
	return `WITH query_page AS MATERIALIZED (` + selection + `) SELECT * FROM query_page ` + order
}
