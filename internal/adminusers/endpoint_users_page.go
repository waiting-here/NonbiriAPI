package adminusers

import (
	"context"
	"database/sql"
	"net/http"
	"strconv"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

const endpointOverviewUsersSelection = `
SELECT e.user_id,COUNT(*),
 COALESCE(SUM((SELECT COUNT(*) FROM endpoint_keys k WHERE k.endpoint_id=e.id)),0),
 COALESCE(SUM(e.enabled),0)
FROM endpoints e JOIN users u ON u.id=e.user_id AND u.is_admin=0
WHERE e.base_url=? GROUP BY e.user_id`

func readEndpointOverviewUsers(ctx context.Context, tx *sql.Tx, selection string, args []any) ([]EndpointOverviewUser, error) {
	rows, err := tx.QueryContext(ctx, selection, args...)
	if err != nil {
		return nil, classifyDatabaseError("list endpoint overview users", err)
	}
	defer rows.Close()
	data := []EndpointOverviewUser{}
	for rows.Next() {
		var userID, endpoints, keys, enabled int64
		if err := rows.Scan(&userID, &endpoints, &keys, &enabled); err != nil {
			return nil, classifyDatabaseError("scan endpoint overview user", err)
		}
		data = append(data, EndpointOverviewUser{UserID: strconv.FormatInt(userID, 10), EndpointCount: strconv.FormatInt(endpoints, 10), KeyCount: strconv.FormatInt(keys, 10), EnabledCount: strconv.FormatInt(enabled, 10)})
	}
	if err := rows.Err(); err != nil {
		return nil, classifyDatabaseError("iterate endpoint overview users", err)
	}
	if err := rows.Close(); err != nil {
		return nil, classifyDatabaseError("close endpoint overview users", err)
	}
	return data, nil
}

// EndpointOverviewUsers reads the complete user grouping behind the bounded
// preview returned by a numbered endpoint overview page.
func (service *Service) EndpointOverviewUsers(ctx context.Context, adminID int64, baseURL string, requested pagination.Request) (Page[EndpointOverviewUser], error) {
	if !requested.Valid() || baseURL == "" || len(baseURL) > 4096 || !utf8.ValidString(baseURL) {
		return Page[EndpointOverviewUser]{}, ErrInvalidRequest
	}
	if ctx == nil {
		return Page[EndpointOverviewUser]{}, ErrUnauthorized
	}
	ctx, cancel := context.WithTimeout(ctx, pageReadTimeout)
	defer cancel()
	tx, err := service.beginListRead(ctx, adminID, true)
	if err != nil {
		return Page[EndpointOverviewUser]{}, err
	}
	defer tx.Rollback()
	selection, args, metadata, err := listPageQuery(ctx, tx, endpointOverviewUsersSelection, ` ORDER BY e.user_id ASC`, []any{baseURL}, &requested, requested.Size)
	if err != nil {
		return Page[EndpointOverviewUser]{}, err
	}
	data, err := readEndpointOverviewUsers(ctx, tx, selection, args)
	if err != nil {
		return Page[EndpointOverviewUser]{}, err
	}
	if err := commitTx(tx, "commit endpoint overview users"); err != nil {
		return Page[EndpointOverviewUser]{}, err
	}
	return Page[EndpointOverviewUser]{Data: data, Pagination: metadata}, nil
}

func (api *httpAPI) getEndpointOverviewUsers(writer http.ResponseWriter, request *http.Request, principal AdminPrincipal) {
	if !requireNoBody(writer, request) {
		return
	}
	// A canonical 4096-byte URL may expand threefold in a query value.
	values, ok := strictQueryLimit(writer, request, 16*1024, "base_url", "page", "page_size")
	if !ok {
		return
	}
	baseURL, _ := singleQuery(values, "base_url")
	requested, _, err := pagination.Parse(values)
	if err != nil {
		writeError(writer, ErrInvalidRequest)
		return
	}
	page, err := api.service.EndpointOverviewUsers(request.Context(), principal.UserID, baseURL, requested)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, page)
}
