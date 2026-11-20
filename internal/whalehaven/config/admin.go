package config

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"maps"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/httpapi"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/managementlog"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
	"github.com/waiting-here/NonbiriAPI/internal/strictjson"
)

const RouteAdminConfig = "/admin/api/games/whale-haven/config"

var errInvalid = errors.New("invalid whale configuration")
var errRevision = errors.New("whale configuration revision conflict")

type AdminRegistrar interface {
	RegisterAdminRoute(string, string, http.Handler) error
}
type AdminAuthorizer interface {
	AuthorizeAdminMutation(context.Context, *sql.Tx) error
}

type adminAPI struct {
	reader, writer *sql.DB
	auth           AdminAuthorizer
	now            func() time.Time
}

func RegisterAdmin(registrar AdminRegistrar, readDB, writeDB *sql.DB, auth AdminAuthorizer, now func() time.Time) error {
	if registrar == nil || readDB == nil || writeDB == nil || auth == nil || now == nil {
		return errInvalid
	}
	admin := adminAPI{readDB, writeDB, auth, now}
	if err := registrar.RegisterAdminRoute(http.MethodGet, RouteAdminConfig, http.HandlerFunc(admin.get)); err != nil {
		return err
	}
	return registrar.RegisterAdminRoute(http.MethodPatch, RouteAdminConfig, http.HandlerFunc(admin.patch))
}

func wireResponse(s Settings, revision int64) ([]byte, error) {
	return json.Marshal(struct {
		Settings
		Revision string  `json:"revision"`
		Fields   []Field `json:"fields"`
	}{s, strconv.FormatInt(revision, 10), fields})
}

func respond(w http.ResponseWriter, body []byte, err error) {
	w.Header().Set("Cache-Control", "no-store")
	if err == nil {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
		return
	}
	code := httperr.CodeInternal
	message := "could not read or save Whale Haven settings"
	switch {
	case errors.Is(err, httpapi.ErrTooLarge):
		code, message = httperr.CodePayloadTooLarge, "request body is too large"
	case errors.Is(err, errInvalid):
		code, message = httperr.CodeInvalidRequest, err.Error()
	case errors.Is(err, errRevision), errors.Is(err, idempotency.ErrConflict), errors.Is(err, idempotency.ErrInProgress):
		code, message = httperr.CodeConflict, "configuration changed or request key already used"
	case errors.Is(err, resources.ErrMaintenance):
		code, message = httperr.CodeMaintenance, "maintenance mode"
	case errors.Is(err, resources.ErrForbidden):
		code, message = httperr.CodeForbidden, "forbidden"
	case errors.Is(err, resources.ErrUnauthorized):
		code, message = httperr.CodeUnauthorized, "authentication required"
	default:
		if stable := authz.StableCode(err); stable != "" {
			code, message = stable, "authorization failed"
		}
	}
	httperr.WriteError(w, httperr.New(code, message))
}

func (api adminAPI) get(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" || r.URL.ForceQuery || !httpapi.NoBody(r) {
		respond(w, nil, errInvalid)
		return
	}
	tx, err := api.reader.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		respond(w, nil, err)
		return
	}
	defer tx.Rollback()
	s, rev, err := Read(r.Context(), tx)
	if err != nil {
		respond(w, nil, err)
		return
	}
	body, err := wireResponse(s, rev)
	respond(w, body, err)
}

type update struct {
	ExpectedRevision string                                  `json:"expected_revision"`
	Enabled          httpapi.RequestField[bool]              `json:"enabled"`
	ExchangeEnabled  httpapi.RequestField[bool]              `json:"exchange_enabled"`
	Values           httpapi.RequestField[map[string]*int64] `json:"values"`
}

func (api adminAPI) patch(w http.ResponseWriter, r *http.Request) {
	keys := r.Header.Values("Idempotency-Key")
	if r.URL.RawQuery != "" || r.URL.ForceQuery || len(keys) != 1 {
		respond(w, nil, errInvalid)
		return
	}
	if _, err := idempotency.KeyHash(keys[0]); err != nil {
		respond(w, nil, errInvalid)
		return
	}
	var change update
	raw, err := httpapi.ReadJSON(w, r, &change, httpapi.BodyOptions{MaxBytes: idempotency.MaxControlBodyBytes, Validate: strictjson.ValidateObject})
	if errors.Is(err, httpapi.ErrTooLarge) {
		respond(w, nil, err)
		return
	}
	if err != nil || (!change.Enabled.Set && !change.ExchangeEnabled.Set && len(change.Values.Value) == 0) {
		respond(w, nil, errInvalid)
		return
	}
	expected, err := strconv.ParseInt(change.ExpectedRevision, 10, 64)
	if err != nil || expected < 1 || strconv.FormatInt(expected, 10) != change.ExpectedRevision {
		respond(w, nil, errInvalid)
		return
	}
	// Normalize key order without converting any numbers to float64.
	var input map[string]any
	if err := httpapi.DecodeJSONWithNumbers(raw, &input); err != nil {
		respond(w, nil, errInvalid)
		return
	}
	for key := range input {
		switch key {
		case "expected_revision", "enabled", "exchange_enabled", "values":
		default:
			respond(w, nil, errInvalid)
			return
		}
	}
	canonical, err := idempotency.CanonicalJSON(input)
	if err != nil {
		respond(w, nil, errInvalid)
		return
	}
	body, err := api.apply(r.Context(), keys[0], canonical, expected, change)
	respond(w, body, err)
}

func (api adminAPI) apply(ctx context.Context, key string, canonical []byte, expected int64, change update) ([]byte, error) {
	tx, err := api.writer.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := api.auth.AuthorizeAdminMutation(ctx, tx); err != nil {
		return nil, err
	}
	var adminID int64
	if err := tx.QueryRowContext(ctx, "SELECT id FROM users WHERE is_admin=1").Scan(&adminID); err != nil {
		return nil, err
	}
	actor, err := idempotency.ActorScopeHash("admin", strconv.FormatInt(adminID, 10))
	if err != nil {
		return nil, err
	}
	digest, err := idempotency.RequestDigest(idempotency.DigestInput{ActorScopeHash: actor, Method: http.MethodPatch, Route: RouteAdminConfig, Body: canonical})
	if err != nil {
		return nil, err
	}
	now := api.now()
	decision, err := idempotency.Begin(ctx, tx, idempotency.BeginInput{Scope: idempotency.ScopeControlMutation, ActorHash: actor, Key: key, RequestHash: digest, DecisionNow: now.Unix()})
	if err != nil {
		return nil, err
	}
	if decision.Kind == idempotency.Replay {
		if decision.HTTPStatus != http.StatusOK {
			return nil, idempotency.ErrState
		}
		return decision.ResponseBody, tx.Commit()
	}
	s, rev, err := Read(ctx, tx)
	if err != nil {
		return nil, err
	}
	if expected != rev {
		return nil, errRevision
	}
	edited := map[string]any{}
	values := maps.Clone(s.Values)
	for name, value := range change.Values.Value {
		before, known := values[name]
		if !known || value == nil {
			return nil, errInvalid
		}
		if before != *value {
			values[name] = *value
			edited[name] = *value
		}
	}
	s.Values = values
	if change.Enabled.Set && s.Enabled != change.Enabled.Value {
		s.Enabled = change.Enabled.Value
		edited["enabled"] = s.Enabled
	}
	if change.ExchangeEnabled.Set && s.ExchangeEnabled != change.ExchangeEnabled.Value {
		s.ExchangeEnabled = change.ExchangeEnabled.Value
		edited["exchange_enabled"] = s.ExchangeEnabled
	}
	if err := s.validate(); err != nil {
		return nil, err
	}
	if len(edited) > 0 {
		if rev == math.MaxInt64 {
			return nil, errRevision
		}
		document, err := json.Marshal(s)
		if err != nil {
			return nil, err
		}
		// Keep timing monotonic after clock corrections; the writer pool serializes these appends.
		_, err = tx.ExecContext(ctx, `INSERT INTO whale_haven_settings(revision,effective_at,document) VALUES(?,MAX(?,COALESCE((SELECT effective_at FROM whale_haven_settings ORDER BY revision DESC LIMIT 1),0)),?)`, rev+1, now.UnixMilli(), string(document))
		if err != nil {
			return nil, err
		}
		rev++
		changes, err := json.Marshal(edited)
		if err != nil {
			return nil, err
		}
		managementlog.ChangedFields(ctx, changes)
	}
	body, err := wireResponse(s, rev)
	if err != nil {
		return nil, err
	}
	if err := idempotency.Complete(ctx, tx, decision, http.StatusOK, body); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return body, nil
}
