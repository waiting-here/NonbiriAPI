package adminapi

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/gatewaypolicy"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
)

type GatewayModelInput struct {
	ExpectedRevision string               `json:"expected_revision"`
	Entry            *gatewaypolicy.Entry `json:"entry,omitempty"`
}
type GatewayModelList struct {
	Data []gatewaypolicy.Record `json:"data"`
}

func (r *SiteConfigRepository) ReadGatewayModels(ctx context.Context, adminID int64) (GatewayModelList, error) {
	tx, err := r.beginAuthorized(ctx, adminID, true)
	if err != nil {
		return GatewayModelList{}, err
	}
	defer tx.Rollback()
	rows, err := gatewaypolicy.ReadRecords(ctx, tx)
	if err != nil {
		return GatewayModelList{}, classifySiteConfigDatabase("read Gateway capabilities", err)
	}
	if err := tx.Commit(); err != nil {
		return GatewayModelList{}, classifySiteConfigDatabase("finish Gateway capability read", err)
	}
	return GatewayModelList{Data: rows}, nil
}

func (r *SiteConfigRepository) MutateGatewayModel(ctx context.Context, adminID, id int64, method, key string, input GatewayModelInput) (SiteConfigMutationResult, error) {
	bad := SiteConfigMutationResult{}
	expected, err := strconv.ParseInt(input.ExpectedRevision, 10, 64)
	if err != nil || expected < 0 || strconv.FormatInt(expected, 10) != input.ExpectedRevision ||
		method == http.MethodPost && (id != 0 || expected != 0) ||
		(method == http.MethodPut || method == http.MethodDelete) && (id <= 0 || expected == 0) {
		return bad, ErrSiteConfigInvalid
	}
	if method != http.MethodPost && method != http.MethodPut && method != http.MethodDelete {
		return bad, ErrSiteConfigInvalid
	}
	if method == http.MethodDelete {
		if input.Entry != nil {
			return bad, ErrSiteConfigInvalid
		}
	} else {
		if input.Entry == nil {
			return bad, ErrSiteConfigInvalid
		}
		normalized, _, err := gatewaypolicy.Validate(*input.Entry)
		if err != nil {
			return bad, err
		}
		input.Entry = &normalized
	}
	body, err := idempotency.CanonicalJSON(input)
	if err != nil {
		return bad, ErrSiteConfigInvalid
	}
	actorHash, err := idempotency.ActorScopeHash("admin", strconv.FormatInt(adminID, 10))
	if err != nil {
		return bad, ErrSiteConfigInvalid
	}
	route := RouteAdminGatewayModels
	var ids []string
	if id > 0 {
		route = RouteAdminGatewayModel
		ids = []string{strconv.FormatInt(id, 10)}
	}
	digest, err := idempotency.RequestDigest(idempotency.DigestInput{ActorScopeHash: actorHash, Method: method, Route: route, PathResourceIDs: ids, Body: body})
	if err != nil {
		return bad, ErrSiteConfigInvalid
	}
	tx, err := r.beginAuthorized(ctx, adminID, false)
	if err != nil {
		return bad, err
	}
	defer tx.Rollback()
	now := r.now().UTC().Unix()
	if now < 0 || now > maxSiteConfigUnixSecond-idempotency.ReplayWindowSeconds {
		return bad, ErrSiteConfigInvariant
	}
	decision, err := idempotency.Begin(ctx, tx, idempotency.BeginInput{Scope: idempotency.ScopeControlMutation, ActorHash: actorHash, Key: key, RequestHash: digest, DecisionNow: now})
	if err != nil {
		return bad, classifySiteConfigIdempotency(err)
	}
	if decision.Kind == idempotency.Replay {
		if err := tx.Commit(); err != nil {
			return bad, classifySiteConfigDatabase("finish Gateway replay", err)
		}
		return SiteConfigMutationResult{Status: decision.HTTPStatus, Body: decision.ResponseBody, Replayed: true}, nil
	}
	rows, err := gatewaypolicy.ReadRecords(ctx, tx)
	if err != nil {
		return bad, classifySiteConfigDatabase("read Gateway capabilities", err)
	}
	var current *gatewaypolicy.Record
	for i := range rows {
		if rows[i].ID == strconv.FormatInt(id, 10) {
			current = &rows[i]
		}
		if input.Entry != nil && rows[i].ID != strconv.FormatInt(id, 10) && rows[i].BaseURL == input.Entry.BaseURL && rows[i].Model == input.Entry.Model {
			return bad, ErrSiteConfigConflict
		}
	}
	if id != 0 {
		if current == nil {
			return bad, ErrSiteConfigNotFound
		}
		if current.Revision != input.ExpectedRevision || expected == math.MaxInt64 {
			return bad, ErrSiteConfigConflict
		}
	} else if len(rows) >= gatewaypolicy.MaxEntries {
		return bad, ErrSiteConfigConflict
	}
	var response any
	if method == http.MethodDelete {
		if _, err = tx.ExecContext(ctx, "DELETE FROM gateway_model_capabilities WHERE id=?", id); err != nil {
			return bad, classifySiteConfigDatabase("delete Gateway capability", err)
		}
		response = struct {
			ID      string `json:"id"`
			Deleted bool   `json:"deleted"`
		}{strconv.FormatInt(id, 10), true}
	} else {
		raw, err := json.Marshal(input.Entry.Policy)
		if err != nil {
			return bad, ErrSiteConfigInvariant
		}
		revision := expected + 1
		if method == http.MethodPost {
			result, err := tx.ExecContext(ctx, "INSERT INTO gateway_model_capabilities(base_url,model,policy_json,revision,updated_at) VALUES(?,?,?,1,?)", input.Entry.BaseURL, input.Entry.Model, string(raw), now)
			if err != nil {
				return bad, classifySiteConfigDatabase("create Gateway capability", err)
			}
			id, err = result.LastInsertId()
			if err != nil {
				return bad, classifySiteConfigDatabase("identify Gateway capability", err)
			}
		} else {
			if _, err = tx.ExecContext(ctx, "UPDATE gateway_model_capabilities SET base_url=?,model=?,policy_json=?,revision=?,updated_at=? WHERE id=?", input.Entry.BaseURL, input.Entry.Model, string(raw), revision, now, id); err != nil {
				return bad, classifySiteConfigDatabase("update Gateway capability", err)
			}
		}
		response = gatewaypolicy.Record{ID: strconv.FormatInt(id, 10), Revision: strconv.FormatInt(revision, 10), UpdatedAt: now, Entry: *input.Entry}
	}
	responseBody, err := json.Marshal(response)
	if err != nil {
		return bad, ErrSiteConfigInvariant
	}
	if err = idempotency.Complete(ctx, tx, decision, http.StatusOK, responseBody); err != nil {
		return bad, classifySiteConfigIdempotencyComplete(err)
	}
	if err = tx.Commit(); err != nil {
		return bad, classifySiteConfigDatabase("commit Gateway capability", err)
	}
	return SiteConfigMutationResult{Status: http.StatusOK, Body: responseBody}, nil
}

func gatewayValidationField(err error) string {
	var invalid *gatewaypolicy.ValidationError
	if errors.As(err, &invalid) {
		return invalid.Field
	}
	return ""
}
