package adminusers

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"slices"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/endpointtags"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

const routeEndpointTagRules = routeEndpointOverview + "/tag-rules"

type EndpointTagRule struct {
	Pattern string   `json:"pattern"`
	Action  string   `json:"action"`
	Tags    []string `json:"tags"`
}

func readEndpointTagRules(ctx context.Context, tx *sql.Tx, selection string, args ...any) ([]EndpointTagRule, error) {
	rows, err := tx.QueryContext(ctx, `SELECT r.domain,r.action,(SELECT COALESCE(group_concat(tag,','),'') FROM
 (SELECT tag FROM endpoint_tag_rule_tags WHERE domain=r.domain ORDER BY tag)) FROM (`+selection+`) r`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []EndpointTagRule{}
	for rows.Next() {
		item := EndpointTagRule{Tags: []string{}}
		var domain, tags string
		if err := rows.Scan(&domain, &item.Action, &tags); err != nil {
			return nil, err
		}
		item.Pattern = "*." + domain
		if tags != "" {
			item.Tags = strings.Split(tags, ",")
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Service) endpointTagRules(ctx context.Context, actorID int64, role managementRole, q string, requested pagination.Request) (Page[EndpointTagRule], error) {
	if q != "" && !validFilter(q) || !requested.Valid() {
		return Page[EndpointTagRule]{}, ErrInvalidRequest
	}
	ctx, cancel := context.WithTimeout(ctx, pageReadTimeout)
	defer cancel()
	tx, err := s.beginEndpointManagement(ctx, actorID, role, true, "endpoints.read")
	if err != nil {
		return Page[EndpointTagRule]{}, err
	}
	defer tx.Rollback()
	var total int64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM endpoint_tag_rules WHERE instr(domain,?)>0`, strings.TrimPrefix(strings.ToLower(q), "*.")).Scan(&total); err != nil {
		return Page[EndpointTagRule]{}, err
	}
	metadata, offset, err := requested.Window(total)
	if err != nil {
		return Page[EndpointTagRule]{}, err
	}
	data, err := readEndpointTagRules(ctx, tx, `SELECT domain,action FROM endpoint_tag_rules WHERE instr(domain,?)>0 ORDER BY domain LIMIT ? OFFSET ?`, strings.TrimPrefix(strings.ToLower(q), "*."), requested.Size, offset)
	if err != nil {
		return Page[EndpointTagRule]{}, err
	}
	if err := tx.Commit(); err != nil {
		return Page[EndpointTagRule]{}, err
	}
	return Page[EndpointTagRule]{Data: data, Pagination: &metadata}, nil
}

func (s *Service) mutateEndpointTagRule(ctx context.Context, actorID int64, role managementRole, control ControlMutation, input EndpointTagRule, remove bool) (MutationResult[struct{}], error) {
	empty := MutationResult[struct{}]{}
	pattern := endpointtags.Pattern(input.Pattern)
	if pattern == "" || !remove && (input.Action != "add" && input.Action != "remove" || input.Tags == nil || len(input.Tags) > 9 || input.Action == "add" && len(input.Tags) == 0 || input.Action == "remove" && len(input.Tags) != 0) {
		return empty, ErrInvalidRequest
	}
	for _, tag := range input.Tags {
		if !endpointtags.Valid(tag) {
			return empty, ErrInvalidRequest
		}
	}
	tx, err := s.beginEndpointManagement(ctx, actorID, role, false, "endpoints.tags")
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	decision, err := beginControlMutation(ctx, tx, actorID, role, control, s.now().Unix())
	if err != nil {
		return empty, err
	}
	if decision.Kind == idempotency.Replay {
		if err := tx.Commit(); err != nil {
			return empty, err
		}
		return replayJSON[struct{}](decision)
	}
	domain := pattern[2:]
	if remove {
		if _, err := tx.ExecContext(ctx, `DELETE FROM endpoint_tag_rules WHERE domain=?`, domain); err != nil {
			return empty, err
		}
	} else {
		if _, err := tx.ExecContext(ctx, `DELETE FROM endpoint_tag_rule_tags WHERE domain=?`, domain); err != nil {
			return empty, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO endpoint_tag_rules(domain,action) VALUES(?,?) ON CONFLICT(domain) DO UPDATE SET action=excluded.action`, domain, input.Action); err != nil {
			return empty, err
		}
		for _, tag := range input.Tags {
			if _, err := tx.ExecContext(ctx, `INSERT INTO endpoint_tag_rule_tags(domain,tag) VALUES(?,?) ON CONFLICT DO NOTHING`, domain, tag); err != nil {
				return empty, err
			}
		}
	}
	if err := idempotency.Complete(ctx, tx, decision, http.StatusNoContent, []byte{}); err != nil {
		return empty, err
	}
	if err := tx.Commit(); err != nil {
		return empty, err
	}
	return MutationResult[struct{}]{Status: http.StatusNoContent, Body: []byte{}}, nil
}

func (api *httpAPI) getEndpointTagRules(w http.ResponseWriter, r *http.Request, p AdminPrincipal) {
	if !requireNoBody(w, r) {
		return
	}
	values, ok := strictQuery(w, r, "q", "page", "page_size")
	if !ok {
		return
	}
	page, _, err := pagination.Parse(values)
	if err != nil {
		writeError(w, ErrInvalidRequest)
		return
	}
	result, err := api.service.endpointTagRules(r.Context(), p.UserID, api.role, values.Get("q"), page)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (api *httpAPI) writeEndpointTagRule(w http.ResponseWriter, r *http.Request, p AdminPrincipal) {
	object, ok := decodeStrictObject(w, r)
	if !ok {
		return
	}
	remove := r.Method == http.MethodDelete
	var input EndpointTagRule
	fields := []string{"pattern", "action", "tags"}
	if remove {
		fields = []string{"pattern"}
	}
	if !exactObject(object, fields...) || json.Unmarshal(object["pattern"], &input.Pattern) != nil {
		writeError(w, ErrInvalidRequest)
		return
	}
	if !remove && (json.Unmarshal(object["action"], &input.Action) != nil || json.Unmarshal(object["tags"], &input.Tags) != nil) {
		writeError(w, ErrInvalidRequest)
		return
	}
	input.Pattern = endpointtags.Pattern(input.Pattern)
	slices.Sort(input.Tags)
	input.Tags = slices.Compact(input.Tags)
	control, ok := makeControl(w, r, api.role.route(routeEndpointTagRules), 0, input)
	if !ok {
		return
	}
	control.PathIDs = []string{}
	result, err := api.service.mutateEndpointTagRule(r.Context(), p.UserID, api.role, control, input, remove)
	if err != nil {
		writeError(w, err)
		return
	}
	writeMutation(w, result.Status, result.Body)
}
