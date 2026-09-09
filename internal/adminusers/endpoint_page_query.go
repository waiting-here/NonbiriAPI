package adminusers

import (
	"context"
	"database/sql"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

const endpointOverviewGroups = `
SELECT e.base_url
FROM endpoints e JOIN users u ON u.id=e.user_id AND u.is_admin=0
WHERE (?='' OR instr(e.base_url,?)>0) AND e.base_url>?
GROUP BY e.base_url`

const endpointOverviewUserGroups = `
SELECT e.user_id
FROM endpoints e JOIN users u ON u.id=e.user_id AND u.is_admin=0
WHERE e.base_url=? GROUP BY e.user_id`

// Count and choose groups before reading their child-key totals. The materialized
// window keeps aggregates for skipped groups out of both numbered and cursor reads.
func endpointOverviewPageQuery(ctx context.Context, tx *sql.Tx, q, after string, requested *pagination.Request, limit int) (string, []any, *pagination.Metadata, error) {
	window, args, metadata, err := listPageQuery(ctx, tx, endpointOverviewGroups, ` ORDER BY e.base_url ASC`, []any{q, q, after}, requested, limit)
	if err != nil {
		return "", nil, nil, err
	}
	return `WITH endpoint_page AS MATERIALIZED (` + window + `)
SELECT p.base_url,COUNT(DISTINCT e.user_id),COUNT(*),
 COALESCE(SUM((SELECT COUNT(*) FROM endpoint_keys k WHERE k.endpoint_id=e.id)),0)
FROM endpoint_page p
CROSS JOIN endpoints e ON e.base_url=p.base_url
CROSS JOIN users u ON u.id=e.user_id AND u.is_admin=0
GROUP BY p.base_url ORDER BY p.base_url ASC`, args, metadata, nil
}

func endpointOverviewUsersPageQuery(ctx context.Context, tx *sql.Tx, baseURL string, requested pagination.Request) (string, []any, *pagination.Metadata, error) {
	window, args, metadata, err := listPageQuery(ctx, tx, endpointOverviewUserGroups, ` ORDER BY e.user_id ASC`, []any{baseURL}, &requested, requested.Size)
	if err != nil {
		return "", nil, nil, err
	}
	return `WITH user_page AS MATERIALIZED (` + window + `)
SELECT p.user_id,COUNT(*),
 COALESCE(SUM((SELECT COUNT(*) FROM endpoint_keys k WHERE k.endpoint_id=e.id)),0),
 COALESCE(SUM(e.enabled),0)
FROM user_page p
CROSS JOIN endpoints e ON e.base_url=? AND e.user_id=p.user_id
GROUP BY p.user_id ORDER BY p.user_id ASC`, append(args, baseURL), metadata, nil
}
