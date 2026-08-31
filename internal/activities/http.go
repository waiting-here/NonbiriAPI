package activities

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/strictjson"
)

const (
	routeActivities            = "/api/activities"
	routeWelfareClaims         = "/api/activities/welfare/claims"
	routeThursday              = "/api/activities/thursday"
	routeThursdayContributions = "/api/activities/thursday/contributions"
	routeAdminPools            = "/admin/api/pools"
	routeAdminPoolAdjustment   = "/admin/api/pools/{poolId}/adjustments"
	routeAdminActivityConfig   = "/admin/api/activities/config"
	routeAdminThursday         = "/admin/api/activities/thursday"
	routeAdminThursdayNext     = "/admin/api/activities/thursday/next"
	routeAdminThursdayResume   = "/admin/api/activities/thursday/{periodId}/resume"
)

type httpAPI struct{ service *Service }

func RegisterRoutes(users UserRouteRegistrar, admins AdminRouteRegistrar, service *Service) error {
	if isNilInterface(users) || isNilInterface(admins) || service == nil || service.repository == nil {
		return errors.New("activities: route registrars and service are required")
	}
	api := &httpAPI{service: service}
	userRoutes := []struct {
		method, pattern string
		handler         AuthorizedUserHandler
	}{
		{http.MethodGet, routeActivities, api.getActivities},
		{http.MethodPost, routeWelfareClaims, api.claimWelfare},
		{http.MethodGet, routeThursday, api.getThursday},
		{http.MethodPost, routeThursdayContributions, api.contributeThursday},
	}
	for _, route := range userRoutes {
		if err := users.RegisterUserRoute(route.method, route.pattern, route.handler); err != nil {
			return fmt.Errorf("activities: register %s %s: %w", route.method, route.pattern, err)
		}
	}
	adminRoutes := []struct {
		method, pattern string
		handler         AuthorizedAdminHandler
	}{
		{http.MethodGet, routeAdminPools, api.listPools},
		{http.MethodPost, routeAdminPoolAdjustment, api.adjustPool},
		{http.MethodGet, routeAdminActivityConfig, api.getConfig},
		{http.MethodPatch, routeAdminActivityConfig, api.patchConfig},
		{http.MethodGet, routeAdminThursday, api.getAdminThursday},
		{http.MethodPut, routeAdminThursdayNext, api.putThursdayNext},
		{http.MethodPost, routeAdminThursdayResume, api.resumeThursday},
	}
	for _, route := range adminRoutes {
		if err := admins.RegisterAdminRoute(route.method, route.pattern, route.handler); err != nil {
			return fmt.Errorf("activities: register %s %s: %w", route.method, route.pattern, err)
		}
	}
	return nil
}

func (api *httpAPI) getActivities(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	if !requireReadRequest(writer, request) {
		return
	}
	value, err := api.service.GetActivities(request.Context(), principal.UserID)
	if err != nil {
		writeActivitiesError(writer, err)
		return
	}
	writeActivitiesJSON(writer, http.StatusOK, value)
}

func (api *httpAPI) getThursday(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	if !requireReadRequest(writer, request) {
		return
	}
	value, err := api.service.GetThursday(request.Context(), principal.UserID)
	if err != nil {
		writeActivitiesError(writer, err)
		return
	}
	writeActivitiesJSON(writer, http.StatusOK, value)
}

func (api *httpAPI) claimWelfare(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	mutation, ok := noBodyControlMutation(writer, request, routeWelfareClaims)
	if !ok {
		return
	}
	result, err := api.service.ClaimWelfare(request.Context(), principal.UserID, mutation)
	if err != nil {
		writeActivitiesError(writer, err)
		return
	}
	writeActivitiesMutation(writer, result.Status, result.Body)
}

type contributionWire struct {
	PeriodID         requestField[string] `json:"period_id"`
	ExpectedRevision requestField[string] `json:"expected_revision"`
}

func (api *httpAPI) contributeThursday(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	var body contributionWire
	if !decodeStrictObject(writer, request, &body) {
		return
	}
	if !body.PeriodID.Set || !body.ExpectedRevision.Set || !db.ValidateOpaqueID(body.PeriodID.Value, "thu_") {
		writeActivitiesError(writer, ErrInvalidRequest)
		return
	}
	revision, ok := parseRevision(body.ExpectedRevision.Value)
	if !ok {
		writeActivitiesError(writer, ErrInvalidRequest)
		return
	}
	canonical := map[string]any{"period_id": body.PeriodID.Value, "expected_revision": body.ExpectedRevision.Value}
	mutation, ok := bodyControlMutation(writer, request, routeThursdayContributions, nil, canonical)
	if !ok {
		return
	}
	result, err := api.service.ContributeThursday(request.Context(), principal.UserID, mutation, ThursdayContributionInput{
		PeriodID: body.PeriodID.Value, ExpectedRevision: revision,
	})
	if err != nil {
		writeActivitiesError(writer, err)
		return
	}
	writeActivitiesMutation(writer, result.Status, result.Body)
}

func (api *httpAPI) listPools(writer http.ResponseWriter, request *http.Request, _ AdminPrincipal) {
	if !requireNoBody(writer, request) {
		return
	}
	values, ok := strictQuery(writer, request)
	if !ok {
		return
	}
	if !exactQuery(values, "pool_type", "state", "cursor", "limit") {
		writeActivitiesError(writer, ErrInvalidRequest)
		return
	}
	query := PoolListQuery{}
	if entries, set := values["pool_type"]; set {
		if entries[0] != PoolTypeWelfare && entries[0] != PoolTypeThursday {
			writeActivitiesError(writer, ErrInvalidRequest)
			return
		}
		query.PoolType = entries[0]
	}
	if entries, set := values["state"]; set {
		if entries[0] != PoolStateOpen && entries[0] != PoolStateClosed {
			writeActivitiesError(writer, ErrInvalidRequest)
			return
		}
		query.State = entries[0]
	}
	if entries, set := values["cursor"]; set {
		if entries[0] == "" {
			writeActivitiesError(writer, ErrInvalidRequest)
			return
		}
		query.Cursor = entries[0]
	}
	if entries, set := values["limit"]; set {
		limit, err := strconv.Atoi(entries[0])
		if err != nil || strconv.Itoa(limit) != entries[0] {
			writeActivitiesError(writer, ErrInvalidRequest)
			return
		}
		query.Limit = limit
	}
	page, err := api.service.ListPools(request.Context(), query)
	if err != nil {
		writeActivitiesError(writer, err)
		return
	}
	writeActivitiesJSON(writer, http.StatusOK, page)
}

type adjustmentWire struct {
	Direction        requestField[string] `json:"direction"`
	Amount           requestField[string] `json:"amount"`
	Reason           requestField[string] `json:"reason"`
	ExpectedRevision requestField[string] `json:"expected_revision"`
	Confirmation     requestField[string] `json:"confirmation"`
}

func (api *httpAPI) adjustPool(writer http.ResponseWriter, request *http.Request, principal AdminPrincipal) {
	poolID := request.PathValue("poolId")
	if !db.ValidateOpaqueID(poolID, "pol_") {
		writeActivitiesError(writer, ErrNotFound)
		return
	}
	var body adjustmentWire
	if !decodeStrictObject(writer, request, &body) {
		return
	}
	if !body.Direction.Set || !body.Amount.Set || !body.Reason.Set || !body.ExpectedRevision.Set || !body.Confirmation.Set {
		writeActivitiesError(writer, ErrInvalidRequest)
		return
	}
	revision, ok := parseRevision(body.ExpectedRevision.Value)
	if !ok {
		writeActivitiesError(writer, ErrInvalidRequest)
		return
	}
	canonical := map[string]any{
		"direction": body.Direction.Value, "amount": body.Amount.Value, "reason": body.Reason.Value,
		"expected_revision": body.ExpectedRevision.Value, "confirmation": body.Confirmation.Value,
	}
	mutation, ok := bodyControlMutation(writer, request, routeAdminPoolAdjustment, []string{poolID}, canonical)
	if !ok {
		return
	}
	result, err := api.service.AdjustPool(request.Context(), principal.UserID, poolID, mutation, PoolAdjustment{
		Direction: body.Direction.Value, Amount: body.Amount.Value, Reason: body.Reason.Value,
		ExpectedRevision: revision, Confirmation: body.Confirmation.Value,
	})
	if err != nil {
		writeActivitiesError(writer, err)
		return
	}
	writeActivitiesMutation(writer, result.Status, result.Body)
}

func (api *httpAPI) getConfig(writer http.ResponseWriter, request *http.Request, _ AdminPrincipal) {
	if !requireReadRequest(writer, request) {
		return
	}
	config, err := api.service.GetActivitiesConfig(request.Context())
	if err != nil {
		writeActivitiesError(writer, err)
		return
	}
	writeActivitiesJSON(writer, http.StatusOK, config)
}

func (api *httpAPI) getAdminThursday(writer http.ResponseWriter, request *http.Request, principal AdminPrincipal) {
	if !requireReadRequest(writer, request) {
		return
	}
	state, err := api.service.GetAdminThursday(request.Context(), principal.UserID)
	if err != nil {
		writeActivitiesError(writer, err)
		return
	}
	writeActivitiesJSON(writer, http.StatusOK, state)
}

type welfarePatchWire struct {
	Enabled   requestField[bool]   `json:"enabled"`
	Threshold requestField[string] `json:"threshold"`
	Cap       requestField[string] `json:"cap"`
}

type thursdayPatchWire struct {
	Enabled requestField[bool] `json:"enabled"`
}

type configPatchWire struct {
	ExpectedRevision requestField[string]            `json:"expected_revision"`
	MasterEnabled    requestField[bool]              `json:"master_enabled"`
	Welfare          requestField[welfarePatchWire]  `json:"welfare"`
	Thursday         requestField[thursdayPatchWire] `json:"thursday"`
}

func (api *httpAPI) patchConfig(writer http.ResponseWriter, request *http.Request, principal AdminPrincipal) {
	var body configPatchWire
	if !decodeStrictObject(writer, request, &body) {
		return
	}
	if !body.ExpectedRevision.Set {
		writeActivitiesError(writer, ErrInvalidRequest)
		return
	}
	revision, ok := parseRevision(body.ExpectedRevision.Value)
	if !ok {
		writeActivitiesError(writer, ErrInvalidRequest)
		return
	}
	patch := ActivitiesConfigPatch{ExpectedRevision: revision}
	canonical := map[string]any{"expected_revision": body.ExpectedRevision.Value}
	if body.MasterEnabled.Set {
		value := body.MasterEnabled.Value
		patch.MasterEnabled = &value
		canonical["master_enabled"] = value
	}
	if body.Welfare.Set {
		wire := body.Welfare.Value
		if !wire.Enabled.Set && !wire.Threshold.Set && !wire.Cap.Set {
			writeActivitiesError(writer, ErrInvalidRequest)
			return
		}
		item := WelfareConfigPatch{}
		nested := map[string]any{}
		if wire.Enabled.Set {
			value := wire.Enabled.Value
			item.Enabled, nested["enabled"] = &value, value
		}
		if wire.Threshold.Set {
			value := wire.Threshold.Value
			item.Threshold, nested["threshold"] = &value, value
		}
		if wire.Cap.Set {
			value := wire.Cap.Value
			item.Cap, nested["cap"] = &value, value
		}
		patch.Welfare, canonical["welfare"] = &item, nested
	}
	if body.Thursday.Set {
		wire := body.Thursday.Value
		if !wire.Enabled.Set {
			writeActivitiesError(writer, ErrInvalidRequest)
			return
		}
		value := wire.Enabled.Value
		patch.Thursday = &ThursdayConfigPatch{Enabled: &value}
		canonical["thursday"] = map[string]any{"enabled": value}
	}
	if patch.MasterEnabled == nil && patch.Welfare == nil && patch.Thursday == nil {
		writeActivitiesError(writer, ErrInvalidRequest)
		return
	}
	mutation, ok := bodyControlMutation(writer, request, routeAdminActivityConfig, nil, canonical)
	if !ok {
		return
	}
	result, err := api.service.PatchActivitiesConfig(request.Context(), principal.UserID, mutation, patch)
	if err != nil {
		writeActivitiesError(writer, err)
		return
	}
	writeActivitiesMutation(writer, result.Status, result.Body)
}

type pumpsWire struct {
	Platform requestField[int] `json:"platform"`
	Welfare  requestField[int] `json:"welfare"`
	NextPool requestField[int] `json:"next_pool"`
}

type thursdayNextWire struct {
	ExpectedRevision requestField[string]    `json:"expected_revision"`
	PeriodKey        requestField[string]    `json:"period_key"`
	OpensAt          requestField[int64]     `json:"opens_at"`
	Literature       requestField[string]    `json:"literature"`
	Entry            requestField[string]    `json:"entry"`
	PerUserLimit     requestField[int]       `json:"per_user_limit"`
	PumpsBP          requestField[pumpsWire] `json:"pumps_bp"`
}

func (api *httpAPI) putThursdayNext(writer http.ResponseWriter, request *http.Request, principal AdminPrincipal) {
	var body thursdayNextWire
	if !decodeStrictObject(writer, request, &body) {
		return
	}
	if !body.ExpectedRevision.Set || !body.PeriodKey.Set || !body.OpensAt.Set || !body.Literature.Set ||
		!body.Entry.Set || !body.PerUserLimit.Set || !body.PumpsBP.Set ||
		!body.PumpsBP.Value.Platform.Set || !body.PumpsBP.Value.Welfare.Set || !body.PumpsBP.Value.NextPool.Set {
		writeActivitiesError(writer, ErrInvalidRequest)
		return
	}
	revision, ok := parseRevision(body.ExpectedRevision.Value)
	if !ok {
		writeActivitiesError(writer, ErrInvalidRequest)
		return
	}
	pumps := PumpsBP{Platform: body.PumpsBP.Value.Platform.Value, Welfare: body.PumpsBP.Value.Welfare.Value, NextPool: body.PumpsBP.Value.NextPool.Value}
	canonical := map[string]any{
		"expected_revision": body.ExpectedRevision.Value, "period_key": body.PeriodKey.Value,
		"opens_at": body.OpensAt.Value, "literature": body.Literature.Value,
		"entry": body.Entry.Value, "per_user_limit": body.PerUserLimit.Value,
		"pumps_bp": map[string]any{"platform": pumps.Platform, "welfare": pumps.Welfare, "next_pool": pumps.NextPool},
	}
	mutation, ok := bodyControlMutation(writer, request, routeAdminThursdayNext, nil, canonical)
	if !ok {
		return
	}
	result, err := api.service.PutThursdayNext(request.Context(), principal.UserID, mutation, ThursdayNextMutation{
		ExpectedRevision: revision, PeriodKey: body.PeriodKey.Value, OpensAt: body.OpensAt.Value,
		Literature: body.Literature.Value, Entry: body.Entry.Value, PerUserLimit: body.PerUserLimit.Value, PumpsBP: pumps,
	})
	if err != nil {
		writeActivitiesError(writer, err)
		return
	}
	writeActivitiesMutation(writer, result.Status, result.Body)
}

type resumeWire struct {
	ExpectedRevision requestField[string] `json:"expected_revision"`
}

func (api *httpAPI) resumeThursday(writer http.ResponseWriter, request *http.Request, principal AdminPrincipal) {
	periodID := request.PathValue("periodId")
	if !db.ValidateOpaqueID(periodID, "thu_") {
		writeActivitiesError(writer, ErrNotFound)
		return
	}
	var body resumeWire
	if !decodeStrictObject(writer, request, &body) {
		return
	}
	if !body.ExpectedRevision.Set {
		writeActivitiesError(writer, ErrInvalidRequest)
		return
	}
	revision, ok := parseRevision(body.ExpectedRevision.Value)
	if !ok {
		writeActivitiesError(writer, ErrInvalidRequest)
		return
	}
	canonical := map[string]any{"expected_revision": body.ExpectedRevision.Value}
	mutation, ok := bodyControlMutation(writer, request, routeAdminThursdayResume, []string{periodID}, canonical)
	if !ok {
		return
	}
	result, err := api.service.ResumeThursday(request.Context(), principal.UserID, periodID, mutation, revision)
	if err != nil {
		writeActivitiesError(writer, err)
		return
	}
	writeActivitiesMutation(writer, result.Status, result.Body)
}

type requestField[T any] struct {
	Value T
	Set   bool
}

func (field *requestField[T]) UnmarshalJSON(data []byte) error {
	if field == nil || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return errors.New("null is not allowed")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&field.Value); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON")
	}
	field.Set = true
	return nil
}

func decodeStrictObject[T any](writer http.ResponseWriter, request *http.Request, destination *T) bool {
	if request == nil || request.Body == nil || destination == nil {
		writeActivitiesError(writer, ErrInvalidRequest)
		return false
	}
	if !requireEmptyQuery(writer, request) {
		return false
	}
	limited := http.MaxBytesReader(writer, request.Body, idempotency.MaxControlBodyBytes)
	body, err := io.ReadAll(limited)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			httperr.WriteError(writer, httperr.New(httperr.CodePayloadTooLarge, "request body is too large"))
		} else {
			writeActivitiesError(writer, ErrInvalidRequest)
		}
		return false
	}
	if len(body) == 0 || strictjson.ValidateObject(body) != nil {
		writeActivitiesError(writer, ErrInvalidRequest)
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writeActivitiesError(writer, ErrInvalidRequest)
		return false
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		writeActivitiesError(writer, ErrInvalidRequest)
		return false
	}
	return true
}

func bodyControlMutation(writer http.ResponseWriter, request *http.Request, route string, pathIDs []string, canonical any) (ControlMutation, bool) {
	key, ok := requireIdempotencyKey(writer, request)
	if !ok {
		return ControlMutation{}, false
	}
	body, err := idempotency.CanonicalJSON(canonical)
	if err != nil {
		writeActivitiesError(writer, ErrInvalidRequest)
		return ControlMutation{}, false
	}
	return ControlMutation{IdempotencyKey: key, Method: request.Method, Route: route, PathIDs: pathIDs, CanonicalBody: body}, true
}

func noBodyControlMutation(writer http.ResponseWriter, request *http.Request, route string, pathIDs ...string) (ControlMutation, bool) {
	if !requireEmptyQuery(writer, request) || !requireNoBody(writer, request) {
		return ControlMutation{}, false
	}
	key, ok := requireIdempotencyKey(writer, request)
	if !ok {
		return ControlMutation{}, false
	}
	return ControlMutation{IdempotencyKey: key, Method: request.Method, Route: route, PathIDs: pathIDs}, true
}

func requireIdempotencyKey(writer http.ResponseWriter, request *http.Request) (string, bool) {
	values := request.Header.Values("Idempotency-Key")
	if len(values) != 1 {
		writeActivitiesError(writer, ErrInvalidRequest)
		return "", false
	}
	if _, err := idempotency.KeyHash(values[0]); err != nil {
		writeActivitiesError(writer, ErrInvalidRequest)
		return "", false
	}
	return values[0], true
}

func requireReadRequest(writer http.ResponseWriter, request *http.Request) bool {
	return requireEmptyQuery(writer, request) && requireNoBody(writer, request)
}

func requireNoBody(writer http.ResponseWriter, request *http.Request) bool {
	if request == nil || request.Body == nil {
		return true
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, 1))
	if err != nil || len(body) != 0 {
		writeActivitiesError(writer, ErrInvalidRequest)
		return false
	}
	return true
}

func strictQuery(writer http.ResponseWriter, request *http.Request) (url.Values, bool) {
	if request == nil || request.URL == nil {
		writeActivitiesError(writer, ErrInvalidRequest)
		return nil, false
	}
	values, err := url.ParseQuery(request.URL.RawQuery)
	if err != nil {
		writeActivitiesError(writer, ErrInvalidRequest)
		return nil, false
	}
	return values, true
}

func exactQuery(values url.Values, allowed ...string) bool {
	allow := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		allow[key] = struct{}{}
	}
	for key, entries := range values {
		if _, ok := allow[key]; !ok || len(entries) != 1 {
			return false
		}
	}
	return true
}

func requireEmptyQuery(writer http.ResponseWriter, request *http.Request) bool {
	values, ok := strictQuery(writer, request)
	if !ok {
		return false
	}
	if len(values) != 0 {
		writeActivitiesError(writer, ErrInvalidRequest)
		return false
	}
	return true
}

func parseRevision(value string) (int64, bool) {
	if value == "" || len(value) > 1 && value[0] == '0' {
		return 0, false
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	return parsed, err == nil && parsed >= 1 && strconv.FormatInt(parsed, 10) == value
}

func writeActivitiesMutation(writer http.ResponseWriter, status int, body []byte) {
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_, _ = writer.Write(body)
}

func writeActivitiesJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Cache-Control", "no-store")
	httperr.WriteJSON(writer, status, value)
}

func writeActivitiesError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrInvalidRequest):
		httperr.WriteError(writer, httperr.New(httperr.CodeInvalidRequest, "invalid request"))
	case errors.Is(err, ErrUnauthorized):
		httperr.WriteError(writer, httperr.New(httperr.CodeUnauthorized, "authentication required"))
	case errors.Is(err, ErrForbidden):
		httperr.WriteError(writer, httperr.New(httperr.CodeForbidden, "access denied"))
	case errors.Is(err, ErrMaintenance):
		httperr.WriteError(writer, httperr.New(httperr.CodeMaintenance, "maintenance in progress"))
	case errors.Is(err, ErrFeatureDisabled):
		httperr.WriteError(writer, httperr.New(httperr.CodeFeatureDisabled, "feature is disabled"))
	case errors.Is(err, ErrInsufficientCredits):
		httperr.WriteError(writer, httperr.New(httperr.CodeInsufficientCredits, "insufficient credits"))
	case errors.Is(err, ErrNotFound):
		httperr.WriteError(writer, httperr.New(httperr.CodeNotFound, "not found"))
	case errors.Is(err, ErrConflict):
		httperr.WriteError(writer, httperr.New(httperr.CodeConflict, "request conflicts with current state"))
	case errors.Is(err, ErrResourceLimit):
		httperr.WriteError(writer, httperr.New(httperr.CodeResourceLimitExceeded, "resource limit exceeded"))
	case errors.Is(err, ErrUnavailable), errors.Is(err, ErrRetryable), errors.Is(err, ErrClosed):
		httperr.WriteError(writer, httperr.New(httperr.CodeServiceUnavailable, "service unavailable"))
	default:
		httperr.WriteError(writer, httperr.New(httperr.CodeInternal, "internal error"))
	}
}
