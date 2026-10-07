package adminusers

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
)

const routeEndpointTags = routeEndpointOverview + "/tags"

type endpointTagsMutation struct {
	BaseURLs []string `json:"base_urls"`
	Tag      string   `json:"tag"`
	Add      bool     `json:"add"`
}

func validEndpointTag(tag string) bool {
	return tag == "abusive_third_party" || tag == "community_charity"
}

func validEndpointTagFilter(tag string) bool {
	return tag == "" || tag == "untagged" || validEndpointTag(tag)
}

func (s *Service) setEndpointTags(ctx context.Context, adminID int64, control ControlMutation, input endpointTagsMutation) (MutationResult[struct{}], error) {
	empty := MutationResult[struct{}]{}
	if !validEndpointTag(input.Tag) || len(input.BaseURLs) == 0 || len(input.BaseURLs) > 100 {
		return empty, ErrInvalidRequest
	}
	for _, baseURL := range input.BaseURLs {
		if baseURL == "" || len(baseURL) > 4096 || strings.ContainsAny(baseURL, "\r\n\x00") {
			return empty, ErrInvalidRequest
		}
	}
	tx, err := s.beginManagement(ctx, adminID, roleAdmin, false)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	decision, err := beginControlMutation(ctx, tx, adminID, roleAdmin, control, s.now().Unix())
	if err != nil {
		return empty, err
	}
	if decision.Kind == idempotency.Replay {
		if err = tx.Commit(); err != nil {
			return empty, err
		}
		return replayJSON[struct{}](decision)
	}
	for _, baseURL := range input.BaseURLs {
		var exists bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM endpoints e JOIN users u ON u.id=e.user_id AND u.is_admin=0 WHERE e.base_url=?)`, baseURL).Scan(&exists); err != nil {
			return empty, err
		}
		if !exists {
			return empty, ErrNotFound
		}
		if input.Add {
			_, err = tx.ExecContext(ctx, `INSERT INTO admin_endpoint_tags(base_url,tag) VALUES(?,?) ON CONFLICT DO NOTHING`, baseURL, input.Tag)
		} else {
			_, err = tx.ExecContext(ctx, `DELETE FROM admin_endpoint_tags WHERE base_url=? AND tag=?`, baseURL, input.Tag)
		}
		if err != nil {
			return empty, err
		}
	}
	if err = idempotency.Complete(ctx, tx, decision, http.StatusNoContent, []byte{}); err != nil {
		return empty, err
	}
	if err = tx.Commit(); err != nil {
		return empty, err
	}
	return MutationResult[struct{}]{Status: http.StatusNoContent, Body: []byte{}}, nil
}

func (api *httpAPI) patchEndpointTags(w http.ResponseWriter, r *http.Request, p AdminPrincipal) {
	object, ok := decodeStrictObject(w, r)
	if !ok {
		return
	}
	var input endpointTagsMutation
	if !exactObject(object, "base_urls", "tag", "add") || json.Unmarshal(object["base_urls"], &input.BaseURLs) != nil || json.Unmarshal(object["tag"], &input.Tag) != nil || string(object["add"]) == "null" || json.Unmarshal(object["add"], &input.Add) != nil {
		writeError(w, ErrInvalidRequest)
		return
	}
	slices.Sort(input.BaseURLs)
	input.BaseURLs = slices.Compact(input.BaseURLs)
	control, ok := makeControl(w, r, routeEndpointTags, 0, input)
	if !ok {
		return
	}
	control.PathIDs = []string{}
	result, err := api.service.setEndpointTags(r.Context(), p.UserID, control, input)
	if err != nil {
		writeError(w, err)
		return
	}
	writeMutation(w, result.Status, result.Body)
}
