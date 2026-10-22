package resources

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

// Connection mutations share the existing final authorization and receipt
// boundary. The apply function owns only domain changes in this transaction.
func ownerResourceMutation[T any](ctx context.Context, r *Repository, userID int64, mutation ControlMutation, status int, stage string,
	apply func(context.Context, *sql.Tx, int64) (T, ResourceOperationResult, error),
) (MutationResult[T], error) {
	now, err := r.nowUnix()
	if err != nil {
		return MutationResult[T]{}, err
	}
	tx, err := r.beginAuthorizedTx(ctx, userID)
	if err != nil {
		return MutationResult[T]{}, err
	}
	committed := false
	defer finishTx(tx, &committed)
	decision, err := beginControlMutation(ctx, tx, userID, mutation, now)
	if err != nil {
		return MutationResult[T]{}, err
	}
	if decision.Kind == idempotency.Replay {
		return replayMutation[T](decision)
	}
	item, receipt, err := apply(ctx, tx, now)
	if err != nil {
		return MutationResult[T]{}, err
	}
	var out MutationResult[T]
	if status == http.StatusNoContent {
		empty, err := finishEmptyMutation(ctx, tx, decision, status)
		if err != nil {
			return MutationResult[T]{}, err
		}
		out = MutationResult[T]{Value: item, Status: empty.Status, Body: empty.Body}
	} else {
		out, err = finishJSONMutation(ctx, tx, decision, status, item)
		if err != nil {
			return MutationResult[T]{}, err
		}
	}
	if stage != "" {
		if err := recordResourceOperation(ctx, tx, userID, mutation, idempotency.ScopeControlMutation, stage, receipt); err != nil {
			return MutationResult[T]{}, err
		}
	}
	if err := commitTx(tx, &committed); err != nil {
		return MutationResult[T]{}, err
	}
	return out, nil
}

type connectionListWindow struct {
	Page   *pagination.Request
	Limit  int
	Cursor string
}

func parseConnectionList(writer http.ResponseWriter, request *http.Request, fields ...string) (url.Values, connectionListWindow, bool) {
	values, ok := requestQuery(writer, request)
	if !ok {
		return nil, connectionListWindow{}, false
	}
	page, mode, err := pagination.Parse(values)
	if err != nil {
		writeResourceError(writer, ErrInvalidRequest)
		return nil, connectionListWindow{}, false
	}
	if mode {
		if !exactQuery(values, append([]string{"page", "page_size"}, fields...)...) {
			writeResourceError(writer, ErrInvalidRequest)
			return nil, connectionListWindow{}, false
		}
		return values, connectionListWindow{Page: &page}, true
	}
	if !exactQuery(values, append([]string{"cursor", "limit"}, fields...)...) {
		writeResourceError(writer, ErrInvalidRequest)
		return nil, connectionListWindow{}, false
	}
	window := connectionListWindow{Limit: defaultPageLimit, Cursor: values.Get("cursor")}
	if values.Has("cursor") && window.Cursor == "" {
		writeResourceError(writer, ErrInvalidRequest)
		return nil, connectionListWindow{}, false
	}
	if values.Has("limit") {
		window.Limit, err = strconv.Atoi(values.Get("limit"))
		if err != nil || !validPageLimit(window.Limit) {
			writeResourceError(writer, ErrInvalidRequest)
			return nil, connectionListWindow{}, false
		}
	}
	return values, window, true
}

// The complete owner/filter arguments are part of the signed cursor scope.
// Queries supplied here are internal and always end in a WHERE predicate.
func readConnectionList[T any](ctx context.Context, tx *sql.Tx, r *Repository, window connectionListWindow, namespace, query, identity string,
	args []any, scan func(pageScanner) (T, error), id func(T) string,
) (Page[T], error) {
	if window.Page != nil {
		return readNumberedPage(ctx, tx, *window.Page, query, identity+" DESC", args, scan)
	}
	if !validPageLimit(window.Limit) {
		return Page[T]{}, ErrInvalidRequest
	}
	now, err := r.nowUnix()
	if err != nil {
		return Page[T]{}, err
	}
	body, err := json.Marshal(args)
	if err != nil {
		return Page[T]{}, ErrInvalidRequest
	}
	digest := sha256.Sum256(body)
	scope := hex.EncodeToString(digest[:])
	var after uint64
	if window.Cursor != "" {
		atoms, err := r.cursors.decode(window.Cursor, namespace, scope, uint64(now), db.CursorUint)
		if err != nil {
			return Page[T]{}, err
		}
		after = atoms[0].Uint
		if after == 0 || after > uint64(^uint64(0)>>1) {
			return Page[T]{}, ErrInvalidRequest
		}
	}
	params := append([]any(nil), args...)
	if after != 0 {
		query += " AND " + identity + "<?"
		params = append(params, after)
	}
	params = append(params, window.Limit+1)
	rows, err := tx.QueryContext(ctx, query+" ORDER BY "+identity+" DESC LIMIT ?", params...)
	if err != nil {
		return Page[T]{}, err
	}
	defer rows.Close()
	page := Page[T]{Data: make([]T, 0, window.Limit+1)}
	for rows.Next() {
		item, err := scan(rows)
		if err != nil {
			return Page[T]{}, err
		}
		page.Data = append(page.Data, item)
	}
	if err := rows.Err(); err != nil {
		return Page[T]{}, err
	}
	if len(page.Data) > window.Limit {
		last, err := parseDecimalID(id(page.Data[window.Limit-1]))
		if err != nil {
			return Page[T]{}, ErrUnavailable
		}
		next, err := r.cursors.encode(namespace, scope, uint64(now+cursorLifetime), []db.CursorAtom{{Kind: db.CursorUint, Uint: uint64(last)}})
		if err != nil {
			return Page[T]{}, err
		}
		page.NextCursor = &next
		page.Data = page.Data[:window.Limit]
	}
	return page, nil
}

func optionalDecimal(value sql.NullInt64) (*string, error) {
	if !value.Valid {
		return nil, nil
	}
	id, err := decimalID(value.Int64)
	if err != nil {
		return nil, ErrUnavailable
	}
	return &id, nil
}

func validResourceMutation(mutation ControlMutation, method, route string, ids ...int64) bool {
	return mutation.Method == method && mutation.Route == route && mutation.Query == "" && mutationPathIDs(mutation, ids...)
}

func optionalResourceID(field requestField[string]) (*int64, bool) {
	if !field.Set {
		return nil, true
	}
	id, err := parseDecimalID(field.Value)
	if err != nil {
		return nil, false
	}
	return &id, true
}

func optionalIDValue(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}
