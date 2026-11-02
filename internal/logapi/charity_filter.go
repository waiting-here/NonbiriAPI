package logapi

import (
	"context"
	"database/sql"
	"strings"
)

const (
	maxCharityModelPairs   = 4096
	maxCharityModelMatches = 64
)

// Walk distinct historical names through the existing (route_kind,model)
// index. Each recursive step seeks past all repetitions of the previous name.
// Keep this CTE single-use and unsorted so LIMIT bounds the enumeration.
var charityModelNamesQuery = `WITH RECURSIVE routes(route_kind) AS (VALUES ` +
	strings.ReplaceAll(charityRouteKindsSQL, ",", "),(") + `),
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

func charityModelFilter(ctx context.Context, reader logReadQueryer, filter ListFilter) (string, []any, error) {
	if filter.CharityModel == nil {
		return "", nil, nil
	}
	fallback := func() (string, []any, error) {
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
	names, complete, err := matchingCharityModelNames(ctx, reader, *filter.CharityModel)
	if err != nil {
		return "", nil, err
	}
	if !complete {
		return fallback()
	}
	predicate := " AND l.route_kind IN " + charityRouteKindsSQL + " AND l.model IN ("
	if len(names) == 0 {
		return predicate + "NULL)", nil, nil
	}
	args := make([]any, len(names))
	for i, name := range names {
		args[i] = name
	}
	return predicate + strings.TrimSuffix(strings.Repeat("?,", len(names)), ",") + ")", args, nil
}

func matchingCharityModelNames(ctx context.Context, reader logReadQueryer, pattern string) (names []string, complete bool, err error) {
	finish := observeLogRead(ctx, "models")
	defer func() { finish(err) }()
	rows, err := reader.QueryContext(ctx, charityModelNamesQuery, pattern, maxCharityModelPairs+1)
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
