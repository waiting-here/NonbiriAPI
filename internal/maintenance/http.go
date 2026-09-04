package maintenance

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/strictjson"
)

const (
	stewardStateRoute  = "/api/steward/maintenance"
	stewardEnableRoute = "/api/steward/maintenance/enable"
	adminStateRoute    = "/admin/api/maintenance"
	adminEnableRoute   = "/admin/api/maintenance/enable"
	adminDisableRoute  = "/admin/api/maintenance/disable"
	maxHTTPBodyBytes   = 8 * 1024
)

var errHTTPPayloadTooLarge = errors.New("maintenance HTTP payload too large")

type HTTPPrincipal struct {
	Actor authz.Actor
}

type AuthorizedHTTPHandler func(http.ResponseWriter, *http.Request, HTTPPrincipal)

type StewardRouteRegistrar interface {
	RegisterStewardRoute(method, pattern string, handler AuthorizedHTTPHandler) error
}

type AdminRouteRegistrar interface {
	RegisterAdminRoute(method, pattern string, handler AuthorizedHTTPHandler) error
}

type HTTPOptions struct {
	Database            *sql.DB
	Service             *Service
	GenerateOperationID func(string) (string, error)
}

type httpAPI struct {
	database *sql.DB
	service  *Service
	generate func(string) (string, error)
}

type stateWire struct {
	Enabled  bool   `json:"enabled"`
	Revision string `json:"revision"`
}

type enableWire struct {
	ExpectedRevision string `json:"expected_revision"`
	Reason           string `json:"reason"`
	Confirmation     bool   `json:"confirmation"`
}

type disableWire struct {
	ExpectedRevision string `json:"expected_revision"`
	Reason           string `json:"reason"`
}

func RegisterRoutes(stewards StewardRouteRegistrar, admins AdminRouteRegistrar, options HTTPOptions) error {
	if stewards == nil || admins == nil || options.Database == nil || options.Service == nil {
		return errors.New("maintenance HTTP dependencies are required")
	}
	generate := options.GenerateOperationID
	if generate == nil {
		generate = db.GenerateOpaqueID
	}
	api := &httpAPI{database: options.Database, service: options.Service, generate: generate}
	for _, route := range []struct {
		method, pattern string
		handler         AuthorizedHTTPHandler
	}{
		{http.MethodGet, stewardStateRoute, api.readSteward},
		{http.MethodPost, stewardEnableRoute, api.enableSteward},
	} {
		if err := stewards.RegisterStewardRoute(route.method, route.pattern, route.handler); err != nil {
			return fmt.Errorf("register maintenance %s %s: %w", route.method, route.pattern, err)
		}
	}
	for _, route := range []struct {
		method, pattern string
		handler         AuthorizedHTTPHandler
	}{
		{http.MethodGet, adminStateRoute, api.readAdmin},
		{http.MethodPost, adminEnableRoute, api.enableAdmin},
		{http.MethodPost, adminDisableRoute, api.disableAdmin},
	} {
		if err := admins.RegisterAdminRoute(route.method, route.pattern, route.handler); err != nil {
			return fmt.Errorf("register maintenance %s %s: %w", route.method, route.pattern, err)
		}
	}
	return nil
}

func stateResponse(state State) (stateWire, error) {
	if !state.valid() {
		return stateWire{}, ErrInvariant
	}
	return stateWire{Enabled: state.Enabled, Revision: strconv.FormatInt(state.Revision, 10)}, nil
}

func (api *httpAPI) authorize(ctx context.Context, tx *sql.Tx, actor authz.Actor, role authz.Role) error {
	if api == nil || api.service == nil || api.service.authorizer == nil || ctx == nil || tx == nil {
		return ErrInvariant
	}
	principal, err := api.service.authorizer.Authorize(ctx, tx, actor, authz.Requirement{Role: role})
	if err != nil {
		return err
	}
	_, _, err = maintenanceActorSnapshot(principal)
	return err
}

func (api *httpAPI) read(writer http.ResponseWriter, request *http.Request, principal HTTPPrincipal, role authz.Role) {
	if !validReadRequest(request) {
		writeHTTPError(writer, ErrInvalidMutation)
		return
	}
	tx, err := api.database.BeginTx(request.Context(), nil)
	if err != nil {
		writeHTTPError(writer, err)
		return
	}
	defer func() { _ = tx.Rollback() }()
	if err := api.authorize(request.Context(), tx, principal.Actor, role); err != nil {
		writeHTTPError(writer, err)
		return
	}
	state, err := loadState(request.Context(), tx)
	if err != nil {
		writeHTTPError(writer, err)
		return
	}
	wire, err := stateResponse(state)
	if err != nil {
		writeHTTPError(writer, err)
		return
	}
	if err := tx.Commit(); err != nil {
		writeHTTPError(writer, err)
		return
	}
	writeHTTPJSON(writer, http.StatusOK, wire)
}

func (api *httpAPI) readSteward(writer http.ResponseWriter, request *http.Request, principal HTTPPrincipal) {
	api.read(writer, request, principal, authz.RoleSteward)
}

func (api *httpAPI) readAdmin(writer http.ResponseWriter, request *http.Request, principal HTTPPrincipal) {
	api.read(writer, request, principal, authz.RoleAdministrator)
}

func (api *httpAPI) enableSteward(writer http.ResponseWriter, request *http.Request, principal HTTPPrincipal) {
	api.enable(writer, request, principal, authz.RoleSteward, stewardEnableRoute)
}

func (api *httpAPI) enableAdmin(writer http.ResponseWriter, request *http.Request, principal HTTPPrincipal) {
	api.enable(writer, request, principal, authz.RoleAdministrator, adminEnableRoute)
}

func (api *httpAPI) enable(writer http.ResponseWriter, request *http.Request, principal HTTPPrincipal, role authz.Role, route string) {
	var body enableWire
	if err := decodeHTTPMutation(request, &body); err != nil {
		writeHTTPError(writer, err)
		return
	}
	if !body.Confirmation {
		writeHTTPError(writer, ErrInvalidMutation)
		return
	}
	revision, err := parseHTTPRevision(body.ExpectedRevision)
	if err != nil || !validReason(body.Reason) {
		writeHTTPError(writer, ErrInvalidMutation)
		return
	}
	canonical, err := idempotency.CanonicalJSON(body)
	if err != nil {
		writeHTTPError(writer, ErrInvalidMutation)
		return
	}
	api.mutate(writer, request, principal, role, route, canonical, func(tx *sql.Tx, operationID string, payloadHash [sha256.Size]byte) (Transition, error) {
		return api.service.EnableTx(request.Context(), tx, principal.Actor, EnableCommand{
			ExpectedRevision: revision,
			OperationID:      operationID,
			PayloadHash:      payloadHash,
			Reason:           body.Reason,
			Confirmed:        true,
		})
	})
}

func (api *httpAPI) disableAdmin(writer http.ResponseWriter, request *http.Request, principal HTTPPrincipal) {
	var body disableWire
	if err := decodeHTTPMutation(request, &body); err != nil {
		writeHTTPError(writer, err)
		return
	}
	revision, err := parseHTTPRevision(body.ExpectedRevision)
	if err != nil || !validReason(body.Reason) {
		writeHTTPError(writer, ErrInvalidMutation)
		return
	}
	canonical, err := idempotency.CanonicalJSON(body)
	if err != nil {
		writeHTTPError(writer, ErrInvalidMutation)
		return
	}
	api.mutate(writer, request, principal, authz.RoleAdministrator, adminDisableRoute, canonical, func(tx *sql.Tx, operationID string, _ [sha256.Size]byte) (Transition, error) {
		return api.service.DisableTx(request.Context(), tx, principal.Actor, DisableCommand{
			ExpectedRevision: revision,
			OperationID:      operationID,
			Reason:           body.Reason,
		})
	})
}

type transitionMutation func(*sql.Tx, string, [sha256.Size]byte) (Transition, error)

func (api *httpAPI) mutate(
	writer http.ResponseWriter,
	request *http.Request,
	principal HTTPPrincipal,
	role authz.Role,
	route string,
	canonicalBody []byte,
	apply transitionMutation,
) {
	key, ok := idempotencyKey(request)
	if !ok {
		writeHTTPError(writer, ErrInvalidMutation)
		return
	}
	tx, err := api.database.BeginTx(request.Context(), nil)
	if err != nil {
		writeHTTPError(writer, err)
		return
	}
	defer func() { _ = tx.Rollback() }()
	if err := api.authorize(request.Context(), tx, principal.Actor, role); err != nil {
		writeHTTPError(writer, err)
		return
	}
	actorKind := "steward"
	if role == authz.RoleAdministrator {
		actorKind = "admin"
	}
	actorHash, err := idempotency.ActorScopeHash(actorKind, strconv.FormatInt(principal.Actor.UserID, 10))
	if err != nil {
		writeHTTPError(writer, ErrInvalidMutation)
		return
	}
	requestHash, err := idempotency.RequestDigest(idempotency.DigestInput{
		ActorScopeHash: actorHash,
		Method:         http.MethodPost,
		Route:          route,
		Body:           canonicalBody,
	})
	if err != nil {
		writeHTTPError(writer, ErrInvalidMutation)
		return
	}
	decisionNow := api.service.now().Unix()
	decision, err := idempotency.Begin(request.Context(), tx, idempotency.BeginInput{
		Scope:       idempotency.ScopeMaintenance,
		ActorHash:   actorHash,
		Key:         key,
		RequestHash: requestHash,
		DecisionNow: decisionNow,
	})
	if err != nil {
		writeHTTPError(writer, err)
		return
	}
	if decision.Kind == idempotency.Replay {
		if decision.HTTPStatus != http.StatusOK || !validStateBody(decision.ResponseBody) {
			writeHTTPError(writer, ErrInvariant)
			return
		}
		if err := tx.Commit(); err != nil {
			writeHTTPError(writer, err)
			return
		}
		if err := api.observeCurrent(request.Context()); err != nil {
			writeHTTPError(writer, err)
			return
		}
		writeHTTPBody(writer, http.StatusOK, decision.ResponseBody)
		return
	}
	operationID, err := api.generate("op_")
	if err != nil || !db.ValidateOpaqueID(operationID, "op_") {
		writeHTTPError(writer, errors.New("generate maintenance operation identity"))
		return
	}
	transition, err := apply(tx, operationID, requestHash)
	if err != nil {
		writeHTTPError(writer, err)
		return
	}
	wire, err := stateResponse(transition.State())
	if err != nil {
		writeHTTPError(writer, err)
		return
	}
	response, err := json.Marshal(wire)
	if err != nil || !validStateBody(response) {
		writeHTTPError(writer, ErrInvariant)
		return
	}
	if err := idempotency.Complete(request.Context(), tx, decision, http.StatusOK, response); err != nil {
		writeHTTPError(writer, err)
		return
	}
	if err := tx.Commit(); err != nil {
		writeHTTPError(writer, err)
		return
	}
	if err := transition.ObserveAfterCommit(request.Context(), api.database); err != nil {
		writeHTTPError(writer, err)
		return
	}
	writeHTTPBody(writer, http.StatusOK, response)
}

func (api *httpAPI) observeCurrent(ctx context.Context) error {
	state, err := LoadState(ctx, api.database)
	if err != nil {
		return err
	}
	return api.service.gate.observeCommitted(state)
}

func validReadRequest(request *http.Request) bool {
	return request != nil && request.URL != nil && !request.URL.ForceQuery && request.URL.RawQuery == "" &&
		request.ContentLength == 0 && len(request.TransferEncoding) == 0
}

func decodeHTTPMutation(request *http.Request, destination any) error {
	if request == nil || request.URL == nil || request.URL.ForceQuery || request.URL.RawQuery != "" || destination == nil {
		return ErrInvalidMutation
	}
	reader := http.MaxBytesReader(nil, request.Body, maxHTTPBodyBytes)
	body, err := io.ReadAll(reader)
	if err != nil {
		var maximum *http.MaxBytesError
		if errors.As(err, &maximum) {
			return errHTTPPayloadTooLarge
		}
		return ErrInvalidMutation
	}
	if len(body) == 0 || strictjson.ValidateObject(body) != nil {
		return ErrInvalidMutation
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return ErrInvalidMutation
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return ErrInvalidMutation
	}
	return nil
}

func parseHTTPRevision(value string) (int64, error) {
	if value == "" || value[0] == '0' {
		return 0, ErrInvalidMutation
	}
	revision, err := strconv.ParseInt(value, 10, 64)
	if err != nil || revision < 1 || revision >= maxInt64 || strconv.FormatInt(revision, 10) != value {
		return 0, ErrInvalidMutation
	}
	return revision, nil
}

func idempotencyKey(request *http.Request) (string, bool) {
	if request == nil {
		return "", false
	}
	values := request.Header.Values("Idempotency-Key")
	if len(values) != 1 {
		return "", false
	}
	_, err := idempotency.KeyHash(values[0])
	return values[0], err == nil
}

func validStateBody(body []byte) bool {
	if strictjson.ValidateObject(body) != nil {
		return false
	}
	var wire stateWire
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&wire) != nil || parseStateWireRevision(wire.Revision) == 0 {
		return false
	}
	return true
}

func parseStateWireRevision(value string) int64 {
	revision, err := parseHTTPRevision(value)
	if err != nil {
		return 0
	}
	return revision
}

func writeHTTPJSON(writer http.ResponseWriter, status int, value any) {
	body, err := json.Marshal(value)
	if err != nil {
		writeHTTPError(writer, ErrInvariant)
		return
	}
	writeHTTPBody(writer, status, body)
}

func writeHTTPBody(writer http.ResponseWriter, status int, body []byte) {
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_, _ = writer.Write(body)
}

func writeHTTPError(writer http.ResponseWriter, err error) {
	code, message := httperr.CodeInternal, "maintenance request failed"
	switch {
	case errors.Is(err, authz.ErrUnauthorized):
		code, message = httperr.CodeUnauthorized, "authentication required"
	case errors.Is(err, authz.ErrForbidden):
		code, message = httperr.CodeForbidden, "access forbidden"
	case errors.Is(err, ErrInvalidMutation):
		code, message = httperr.CodeInvalidRequest, "invalid request"
	case errors.Is(err, errHTTPPayloadTooLarge):
		code, message = httperr.CodePayloadTooLarge, "request body too large"
	case errors.Is(err, ErrConflict), errors.Is(err, idempotency.ErrConflict), errors.Is(err, idempotency.ErrInProgress):
		code, message = httperr.CodeConflict, "maintenance state changed"
	}
	httperr.WriteError(writer, httperr.New(code, message))
}
