package adminalerts

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/strictjson"
)

type BulkResolution struct {
	ResolvedCount int `json:"resolved_count"`
}

func (repository *Repository) ResolveMany(ctx context.Context, adminID int64, ids []int64) (BulkResolution, error) {
	if len(ids) == 0 || len(ids) > 100 {
		return BulkResolution{}, ErrInvalidRequest
	}
	args := make([]any, 0, len(ids))
	seen := make(map[int64]bool, len(ids))
	for _, id := range ids {
		if id <= 0 || seen[id] {
			return BulkResolution{}, ErrInvalidRequest
		}
		seen[id] = true
		args = append(args, id)
	}
	now, err := repository.nowUnix()
	if err != nil {
		return BulkResolution{}, err
	}
	tx, err := repository.beginAuthorized(ctx, adminID, false)
	if err != nil {
		return BulkResolution{}, err
	}
	defer tx.Rollback()
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	var count int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM admin_alerts WHERE id IN ("+placeholders+")", args...).Scan(&count); err != nil {
		return BulkResolution{}, err
	}
	if count != len(ids) {
		return BulkResolution{}, ErrNotFound
	}
	updateArgs := append([]any{now}, args...)
	if _, err = tx.ExecContext(ctx, "UPDATE admin_alerts SET resolved=1,resolved_at=? WHERE resolved=0 AND id IN ("+placeholders+")", updateArgs...); err != nil {
		return BulkResolution{}, err
	}
	if err = tx.Commit(); err != nil {
		return BulkResolution{}, err
	}
	return BulkResolution{ResolvedCount: count}, nil
}

func (api *httpAPI) resolveMany(w http.ResponseWriter, r *http.Request, principal AdminPrincipal) {
	if !requireEmptyQuery(w, r) {
		return
	}
	if r.Body == nil {
		writeError(w, ErrInvalidRequest)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxResolveBodyBytes))
	if err != nil {
		httperr.WriteError(w, httperr.New(httperr.CodePayloadTooLarge, "request body is too large"))
		return
	}
	if strictjson.ValidateObject(body) != nil {
		writeError(w, ErrInvalidRequest)
		return
	}
	var wire struct {
		IDs []string `json:"ids"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&wire) != nil || len(wire.IDs) == 0 || len(wire.IDs) > 100 {
		writeError(w, ErrInvalidRequest)
		return
	}
	ids := make([]int64, len(wire.IDs))
	for i, raw := range wire.IDs {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value <= 0 || strconv.FormatInt(value, 10) != raw {
			writeError(w, ErrInvalidRequest)
			return
		}
		ids[i] = value
	}
	result, err := api.repository.ResolveMany(r.Context(), principal.UserID, ids)
	if err != nil {
		writeError(w, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, result)
}
