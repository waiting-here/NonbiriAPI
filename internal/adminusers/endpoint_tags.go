package adminusers

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/endpointtags"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/managementlog"
)

const routeEndpointTags = routeEndpointOverview + "/tags"

type endpointTagsMutation struct {
	BaseURLs []string `json:"base_urls"`
	Tag      string   `json:"tag"`
	Add      bool     `json:"add"`
}

func validEndpointTag(tag string) bool {
	return endpointtags.Valid(tag)
}

func validEndpointTagFilter(tag string) bool {
	return tag == "" || tag == "untagged" || validEndpointTag(tag)
}

func (s *Service) setEndpointTags(ctx context.Context, adminID int64, control ControlMutation, input endpointTagsMutation) (MutationResult[struct{}], error) {
	return s.setManagedEndpointTags(ctx, adminID, roleAdmin, control, input)
}
func (s *Service) setManagedEndpointTags(ctx context.Context, adminID int64, role managementRole, control ControlMutation, input endpointTagsMutation) (MutationResult[struct{}], error) {
	empty := MutationResult[struct{}]{}
	if !validEndpointTag(input.Tag) || len(input.BaseURLs) == 0 || len(input.BaseURLs) > 100 {
		return empty, ErrInvalidRequest
	}
	for _, baseURL := range input.BaseURLs {
		if baseURL == "" || len(baseURL) > 4096 || strings.ContainsAny(baseURL, "\r\n\x00") {
			return empty, ErrInvalidRequest
		}
	}
	tx, err := s.beginEndpointManagement(ctx, adminID, role, false, "endpoints.tags")
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	// Recheck ownership by rules before receipts: an old successful manual write
	// cannot bypass a rule that now controls the address.
	for _, baseURL := range input.BaseURLs {
		var managed bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM endpoint_tag_rule_matches WHERE base_url=?)`, baseURL).Scan(&managed); err != nil {
			return empty, err
		}
		if managed {
			return empty, ErrEndpointTagsManaged
		}
	}
	decision, err := beginControlMutation(ctx, tx, adminID, role, control, s.now().Unix())
	if err != nil {
		return empty, err
	}
	if decision.Kind == idempotency.Replay {
		if err = tx.Commit(); err != nil {
			return empty, err
		}
		return replayJSON[struct{}](decision)
	}
	items := make([]managementlog.Item, 0, len(input.BaseURLs))
	for _, baseURL := range input.BaseURLs {
		var exists bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM endpoints e JOIN users u ON u.id=e.user_id AND u.is_admin=0 WHERE e.base_url=?)`, baseURL).Scan(&exists); err != nil {
			return empty, err
		}
		if !exists {
			return empty, ErrNotFound
		}
		var result sql.Result
		operation := "deleted"
		if input.Add {
			operation = "created"
			result, err = tx.ExecContext(ctx, `INSERT INTO admin_endpoint_tags(base_url,tag) VALUES(?,?) ON CONFLICT DO NOTHING`, baseURL, input.Tag)
		} else {
			result, err = tx.ExecContext(ctx, `DELETE FROM admin_endpoint_tags WHERE base_url=? AND tag=?`, baseURL, input.Tag)
		}
		if err != nil {
			return empty, err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return empty, err
		}
		outcome := managementlog.Succeeded
		if affected == 0 {
			outcome = managementlog.NoChange
		}
		// Addresses can contain private path/query values. Retain a stable
		// identifier and hostname while recording only the controlled tag name.
		digest := sha256.Sum256([]byte(baseURL))
		name := ""
		if parsed, err := url.Parse(baseURL); err == nil {
			name = parsed.Hostname()
		}
		items = append(items, managementlog.Item{Action: "tags.update", TargetKind: "endpoint_address", TargetID: hex.EncodeToString(digest[:]), TargetName: name, Outcome: outcome, Changes: []managementlog.Change{{Field: "tags." + input.Tag, Operation: operation}}})
	}
	managementlog.SetItemsResult(ctx, items)
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
	control, ok := makeControl(w, r, api.role.route(routeEndpointTags), 0, input)
	if !ok {
		return
	}
	control.PathIDs = []string{}
	result, err := api.service.setManagedEndpointTags(r.Context(), p.UserID, api.role, control, input)
	if err != nil {
		writeError(w, err)
		return
	}
	writeMutation(w, result.Status, result.Body)
}
