package logapi

import (
	"context"
	"database/sql"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/requestkind"
)

const (
	maxCharityModelPairs   = 4096
	maxCharityModelMatches = 64
)

// Walk distinct historical names through the existing (route_kind,model)
// index. Each recursive step seeks past all repetitions of the previous name.
// Keep this CTE single-use and unsorted so LIMIT bounds the enumeration.
var charityModelNamesQuery = charityModelNamesSQL("VALUES " + strings.ReplaceAll(charityRouteKindsSQL, ",", "),("))

func charityModelNamesSQL(routeSeed string) string {
	return `WITH RECURSIVE routes(route_kind) AS (` + routeSeed + `),
names(route_kind,model) AS (
    SELECT route_kind,(SELECT MIN(model) FROM request_logs
        WHERE route_kind=r.route_kind AND route_kind IN ` + charityRouteKindsSQL + `)
    FROM routes r
    UNION ALL
    SELECT route_kind,(SELECT MIN(model) FROM request_logs
        WHERE route_kind=n.route_kind AND route_kind IN ` + charityRouteKindsSQL + ` AND model>n.model)
    FROM names n WHERE model IS NOT NULL
)
SELECT model,instr(lower(model),lower(?))>0 FROM names WHERE model IS NOT NULL LIMIT ?`
}

func charityModelFilter(ctx context.Context, reader logReadQueryer, filter ListFilter) (string, []any, error) {
	if filter.CharityModel == nil {
		return "", nil, nil
	}
	if filter.Operation == OperationDiscovery {
		return " AND 0", nil, nil
	}
	var kind RouteKind
	if filter.Operation != "" {
		kind = requestkind.ForOperation(filter.Operation, true)
	}
	fallback := func() (string, []any, error) {
		if kind != "" {
			return " AND l.route_kind=?" + charityModelPredicate, []any{kind, *filter.CharityModel}, nil
		}
		return charityModelPredicate, []any{*filter.CharityModel}, nil
	}
	// Keep selective date, identity and attempt queries on their existing path.
	// A direct reader cannot share a snapshot between discovery and selection.
	if _, ok := reader.(*sql.Tx); !ok || filter.From != nil || filter.To != nil ||
		filter.UserID != nil || filter.DiscordID != nil || filter.EndpointKeyID != nil ||
		filter.EndpointBaseURL != nil || filter.UpstreamModel != nil ||
		filter.Status != nil || filter.ErrorCode != nil || filter.UsageTotalMismatch != nil ||
		filter.Phase != "" || filter.ErrorCategory != "" || filter.ErrorPhase != "" || filter.Cursor != "" {
		return fallback()
	}
	names, complete, err := matchingCharityModelNames(ctx, reader, *filter.CharityModel, kind)
	if err != nil {
		return "", nil, err
	}
	if !complete {
		return fallback()
	}
	predicate := " AND l.route_kind IN " + charityRouteKindsSQL
	args := make([]any, 0, len(names)+1)
	if kind != "" {
		// Keep the partial-index predicate and make the selected operation's
		// narrower (route_kind,model) range visible to the query planner.
		predicate += " AND l.route_kind=?"
		args = append(args, kind)
	}
	predicate += " AND l.model IN ("
	if len(names) == 0 {
		return predicate + "NULL)", args, nil
	}
	for _, name := range names {
		args = append(args, name)
	}
	return predicate + strings.TrimSuffix(strings.Repeat("?,", len(names)), ",") + ")", args, nil
}

func matchingCharityModelNames(ctx context.Context, reader logReadQueryer, pattern string, kind RouteKind) (names []string, complete bool, err error) {
	finish := observeLogRead(ctx, "models")
	defer func() { finish(err) }()
	query := charityModelNamesQuery
	args := []any{pattern, maxCharityModelPairs + 1}
	if kind != "" {
		query = charityModelNamesSQL("VALUES (?)")
		args = append([]any{kind}, args...)
	}
	rows, err := reader.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, false, translateReadError(ctx, err)
	}
	defer rows.Close()
	seen := make(map[string]struct{})
	count := 0
	complete = true
	for rows.Next() {
		var name string
		var matches bool
		if err = rows.Scan(&name, &matches); err != nil {
			return nil, false, translateReadError(ctx, err)
		}
		count++
		if count > maxCharityModelPairs {
			complete = false
			break
		}
		if matches {
			if _, found := seen[name]; !found {
				seen[name] = struct{}{}
				names = append(names, name)
				if len(names) > maxCharityModelMatches {
					complete = false
					break
				}
			}
		}
	}
	if err = rows.Err(); err != nil {
		return nil, false, translateReadError(ctx, err)
	}
	if err = rows.Close(); err != nil {
		return nil, false, translateReadError(ctx, err)
	}
	if err = ctx.Err(); err != nil {
		return nil, false, translateReadError(ctx, err)
	}
	return names, complete, nil
}
