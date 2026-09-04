package reports

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/httpmw"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
)

type publicReportWire struct {
	ConnectorType string          `json:"connector_type"`
	BaseURL       string          `json:"base_url"`
	Secret        json.RawMessage `json:"secret"`
	Note          string          `json:"note"`
}

type adminApproveWire struct {
	ExpectedMaterialVersion string `json:"expected_material_version"`
	ExpectedTargetVersion   string `json:"expected_target_version"`
	Reason                  string `json:"reason"`
	Confirmation            bool   `json:"confirmation"`
}

type adminRejectWire struct {
	ExpectedMaterialVersion string `json:"expected_material_version"`
	ExpectedTargetVersion   string `json:"expected_target_version"`
	Reason                  string `json:"reason"`
}

type adminResumeWire struct {
	ExpectedTargetVersion string `json:"expected_target_version"`
}

// RegisterRoutes mounts the exact public and administrator report surfaces.
// No L5 route is registered by this package.
func (repository *Repository) RegisterRoutes(registrar RouteRegistrar) error {
	if repository == nil || nilInterface(registrar) {
		return ErrUnavailable
	}
	if err := registrar.RegisterOptionalUserRoute(http.MethodPost, publicRoute, repository.publicReportHTTP); err != nil {
		return err
	}
	for _, route := range []struct {
		method  string
		pattern string
		handler http.HandlerFunc
	}{
		{http.MethodGet, adminBadgeRoute, repository.badgeHTTP},
		{http.MethodGet, adminCasesRoute, repository.casesHTTP},
		{http.MethodGet, adminCaseRoute, repository.caseDetailHTTP},
		{http.MethodGet, adminTargetsRoute, repository.targetsHTTP},
		{http.MethodGet, adminTargetDonationsRoute, repository.targetDonationsHTTP},
		{http.MethodPost, adminApproveRoute, repository.approveHTTP},
		{http.MethodPost, adminRejectRoute, repository.rejectHTTP},
		{http.MethodPost, adminResumeRoute, repository.resumeHTTP},
	} {
		if err := registrar.RegisterAdminRoute(route.method, route.pattern, route.handler); err != nil {
			return err
		}
	}
	return nil
}

func (repository *Repository) publicReportHTTP(
	writer http.ResponseWriter,
	request *http.Request,
	principal *auth.OptionalUserPrincipal,
) {
	if !requireNoQuery(request) {
		writeReportError(writer, ErrInvalidRequest)
		return
	}
	var wire publicReportWire
	body, err := readStrictJSON(writer, request, &wire, maxPublicRequestBodyBytes)
	if err != nil {
		clear(wire.Secret)
		wire.Secret = nil
		writeReportError(writer, err)
		return
	}
	secretValue, err := decodeJSONString(wire.Secret)
	clear(wire.Secret)
	wire.Secret = nil
	clear(body)
	if err != nil {
		writeReportError(writer, ErrInvalidRequest)
		return
	}
	validatedConnector, err := repository.connectors.MustValidate(connectorcontract.Type(wire.ConnectorType))
	if err != nil || string(validatedConnector) != wire.ConnectorType {
		clear(secretValue)
		writeReportError(writer, ErrInvalidRequest)
		return
	}
	canonicalBaseURL, err := repository.baseURLs.ValidateBaseURL(wire.BaseURL)
	if err != nil || canonicalBaseURL == "" {
		clear(secretValue)
		writeReportError(writer, ErrInvalidRequest)
		return
	}
	idempotencyKey, ok := reportIdempotencyKey(request)
	if !ok {
		clear(secretValue)
		writeReportError(writer, ErrInvalidRequest)
		return
	}
	clientAddress, err := netip.ParseAddr(httpmw.ClientIP(request))
	if err != nil {
		clear(secretValue)
		writeReportError(writer, ErrInvalidRequest)
		return
	}
	submission := PublicSubmission{
		ConnectorType: wire.ConnectorType, CanonicalBaseURL: canonicalBaseURL,
		Secret: secretValue, Note: wire.Note, IdempotencyKey: idempotencyKey,
		SourceIP: clientAddress.As16(),
	}
	if principal != nil {
		actor, present := auth.ActorFromContext(request.Context())
		if !present || actor.Kind != authz.ActorUserSession || actor.UserID != principal.UserID {
			submission.clear()
			writeReportError(writer, ErrUnauthorized)
			return
		}
		submission.Reporter = &actor
	} else if _, present := auth.ActorFromContext(request.Context()); present {
		submission.clear()
		writeReportError(writer, ErrInvariant)
		return
	}
	if err := repository.AcceptCredentialTheft(request.Context(), submission); err != nil {
		if request.Context().Err() == nil {
			writeReportError(writer, err)
		}
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("X-Nonbiri-Report-Accepted", "1")
	writer.WriteHeader(http.StatusAccepted)
	_, _ = writer.Write(acceptedResponseBody)
}

func requireNoQuery(request *http.Request) bool {
	if request == nil || request.URL == nil || request.URL.RawQuery != "" {
		return false
	}
	return true
}

func requireNoBody(request *http.Request) bool {
	if request == nil || request.ContentLength > 0 || request.ContentLength < 0 || len(request.TransferEncoding) != 0 {
		return false
	}
	return request.Body == nil || request.Body == http.NoBody
}

func reportIdempotencyKey(request *http.Request) (string, bool) {
	if request == nil {
		return "", false
	}
	values := request.Header.Values("Idempotency-Key")
	if len(values) != 1 {
		return "", false
	}
	if _, err := idempotency.KeyHash(values[0]); err != nil {
		return "", false
	}
	return values[0], true
}

func adminActor(request *http.Request) (authz.Actor, error) {
	if request == nil {
		return authz.Actor{}, ErrUnauthorized
	}
	actor, ok := auth.ActorFromContext(request.Context())
	if !ok || actor.Kind != authz.ActorAdminSession {
		return authz.Actor{}, ErrUnauthorized
	}
	return actor, nil
}

func writeReportJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value)
}

func writeMutationResult(writer http.ResponseWriter, result MutationResult) {
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(result.Status)
	_, _ = writer.Write(result.Body)
}

func writeReportError(writer http.ResponseWriter, err error) {
	code := httperr.CodeInternal
	message := "report request failed"
	switch {
	case errors.Is(err, errPayloadTooLarge):
		code, message = httperr.CodePayloadTooLarge, "request body too large"
	case errors.Is(err, ErrInvalidRequest):
		code, message = httperr.CodeInvalidRequest, "invalid request"
	case errors.Is(err, ErrUnauthorized):
		code, message = httperr.CodeUnauthorized, "authentication required"
	case errors.Is(err, ErrForbidden):
		code, message = httperr.CodeForbidden, "access forbidden"
	case errors.Is(err, ErrNotFound):
		code, message = httperr.CodeNotFound, "report not found"
	case errors.Is(err, ErrConflict):
		code, message = httperr.CodeConflict, "report state changed"
	case errors.Is(err, ErrRateLimited):
		code, message = httperr.CodeRateLimited, "report rate limit reached"
	case errors.Is(err, ErrUnavailable), errors.Is(err, ErrClosed):
		code, message = httperr.CodeServiceUnavailable, "report service unavailable"
	}
	httperr.WriteError(writer, httperr.New(code, message))
}

func parseReportQuery(request *http.Request, allowed ...string) (url.Values, error) {
	if request == nil || request.URL == nil {
		return nil, ErrInvalidRequest
	}
	values, err := url.ParseQuery(request.URL.RawQuery)
	if err != nil {
		return nil, ErrInvalidRequest
	}
	allow := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		allow[key] = struct{}{}
	}
	for key, list := range values {
		if _, ok := allow[key]; !ok || len(list) != 1 {
			return nil, ErrInvalidRequest
		}
	}
	return values, nil
}

func queryLimit(values url.Values, key string) (int, error) {
	raw := values.Get(key)
	if raw == "" {
		return defaultPageLimit, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || !validPageLimit(value) || strconv.Itoa(value) != raw {
		return 0, ErrInvalidRequest
	}
	return value, nil
}

func reportCaseID(request *http.Request) (string, error) {
	if request == nil {
		return "", ErrInvalidRequest
	}
	id := request.PathValue("id")
	if !db.ValidateOpaqueID(id, "rpc_") {
		return "", ErrInvalidRequest
	}
	return id, nil
}

func reportTargetID(request *http.Request) (string, error) {
	if request == nil {
		return "", ErrInvalidRequest
	}
	id := request.PathValue("targetId")
	if !db.ValidateOpaqueID(id, "rpt_") {
		return "", ErrInvalidRequest
	}
	return id, nil
}

func (repository *Repository) targetDonationsHTTP(writer http.ResponseWriter, request *http.Request) {
	if !requireNoBody(request) {
		writeReportError(writer, ErrInvalidRequest)
		return
	}
	id, err := reportCaseID(request)
	if err != nil {
		writeReportError(writer, err)
		return
	}
	targetID, err := reportTargetID(request)
	if err != nil {
		writeReportError(writer, err)
		return
	}
	values, err := parseReportQuery(request, "cursor", "limit")
	if err != nil {
		writeReportError(writer, err)
		return
	}
	limit, err := queryLimit(values, "limit")
	if err != nil {
		writeReportError(writer, err)
		return
	}
	actor, err := adminActor(request)
	if err != nil {
		writeReportError(writer, err)
		return
	}
	response, err := repository.TargetDonations(request.Context(), actor, id, targetID, values.Get("cursor"), limit)
	if err != nil {
		writeReportError(writer, err)
		return
	}
	writeReportJSON(writer, http.StatusOK, response)
}

func (repository *Repository) badgeHTTP(writer http.ResponseWriter, request *http.Request) {
	if !requireNoQuery(request) || !requireNoBody(request) {
		writeReportError(writer, ErrInvalidRequest)
		return
	}
	actor, err := adminActor(request)
	if err != nil {
		writeReportError(writer, err)
		return
	}
	response, err := repository.Badge(request.Context(), actor)
	if err != nil {
		writeReportError(writer, err)
		return
	}
	writeReportJSON(writer, http.StatusOK, response)
}

func (repository *Repository) casesHTTP(writer http.ResponseWriter, request *http.Request) {
	if !requireNoBody(request) {
		writeReportError(writer, ErrInvalidRequest)
		return
	}
	values, err := parseReportQuery(request, "status", "cursor", "limit")
	if err != nil {
		writeReportError(writer, err)
		return
	}
	limit, err := queryLimit(values, "limit")
	if err != nil {
		writeReportError(writer, err)
		return
	}
	actor, err := adminActor(request)
	if err != nil {
		writeReportError(writer, err)
		return
	}
	response, err := repository.ListCases(request.Context(), actor, values.Get("status"), values.Get("cursor"), limit)
	if err != nil {
		writeReportError(writer, err)
		return
	}
	writeReportJSON(writer, http.StatusOK, response)
}

func (repository *Repository) caseDetailHTTP(writer http.ResponseWriter, request *http.Request) {
	if !requireNoBody(request) {
		writeReportError(writer, ErrInvalidRequest)
		return
	}
	id, err := reportCaseID(request)
	if err != nil {
		writeReportError(writer, err)
		return
	}
	values, err := parseReportQuery(request, "materials_cursor", "materials_limit")
	if err != nil {
		writeReportError(writer, err)
		return
	}
	limit, err := queryLimit(values, "materials_limit")
	if err != nil {
		writeReportError(writer, err)
		return
	}
	actor, err := adminActor(request)
	if err != nil {
		writeReportError(writer, err)
		return
	}
	response, err := repository.CaseDetail(request.Context(), actor, id, values.Get("materials_cursor"), limit)
	if err != nil {
		writeReportError(writer, err)
		return
	}
	writeReportJSON(writer, http.StatusOK, response)
}

func (repository *Repository) targetsHTTP(writer http.ResponseWriter, request *http.Request) {
	if !requireNoBody(request) {
		writeReportError(writer, ErrInvalidRequest)
		return
	}
	id, err := reportCaseID(request)
	if err != nil {
		writeReportError(writer, err)
		return
	}
	values, err := parseReportQuery(request, "cursor", "limit")
	if err != nil {
		writeReportError(writer, err)
		return
	}
	limit, err := queryLimit(values, "limit")
	if err != nil {
		writeReportError(writer, err)
		return
	}
	actor, err := adminActor(request)
	if err != nil {
		writeReportError(writer, err)
		return
	}
	response, err := repository.Targets(request.Context(), actor, id, values.Get("cursor"), limit)
	if err != nil {
		writeReportError(writer, err)
		return
	}
	writeReportJSON(writer, http.StatusOK, response)
}

func (repository *Repository) approveHTTP(writer http.ResponseWriter, request *http.Request) {
	id, actor, key, ok := mutationHTTPBoundary(writer, request)
	if !ok {
		return
	}
	command, err := readAdminApproveCommand(writer, request, key)
	if err != nil {
		writeReportError(writer, err)
		return
	}
	result, err := repository.Approve(request.Context(), actor, id, command)
	if err != nil {
		writeReportError(writer, err)
		return
	}
	writeMutationResult(writer, result)
}

func readAdminApproveCommand(writer http.ResponseWriter, request *http.Request, key string) (ApproveCommand, error) {
	var wire adminApproveWire
	body, err := readStrictJSON(writer, request, &wire, maxAdminRequestBodyBytes)
	if err != nil {
		return ApproveCommand{}, err
	}
	clear(body)
	materialVersion, err := parseReportRevision(wire.ExpectedMaterialVersion)
	if err != nil {
		return ApproveCommand{}, err
	}
	targetVersion, err := parseReportRevision(wire.ExpectedTargetVersion)
	if err != nil {
		return ApproveCommand{}, err
	}
	return ApproveCommand{
		ExpectedMaterialVersion: materialVersion,
		ExpectedTargetVersion:   targetVersion,
		Reason:                  wire.Reason, Confirmation: wire.Confirmation, IdempotencyKey: key,
	}, nil
}

func (repository *Repository) rejectHTTP(writer http.ResponseWriter, request *http.Request) {
	id, actor, key, ok := mutationHTTPBoundary(writer, request)
	if !ok {
		return
	}
	command, err := readAdminRejectCommand(writer, request, key)
	if err != nil {
		writeReportError(writer, err)
		return
	}
	result, err := repository.Reject(request.Context(), actor, id, command)
	if err != nil {
		writeReportError(writer, err)
		return
	}
	writeMutationResult(writer, result)
}

func readAdminRejectCommand(writer http.ResponseWriter, request *http.Request, key string) (RejectCommand, error) {
	var wire adminRejectWire
	body, err := readStrictJSON(writer, request, &wire, maxAdminRequestBodyBytes)
	if err != nil {
		return RejectCommand{}, err
	}
	clear(body)
	materialVersion, err := parseReportRevision(wire.ExpectedMaterialVersion)
	if err != nil {
		return RejectCommand{}, err
	}
	targetVersion, err := parseReportRevision(wire.ExpectedTargetVersion)
	if err != nil {
		return RejectCommand{}, err
	}
	return RejectCommand{
		ExpectedMaterialVersion: materialVersion,
		ExpectedTargetVersion:   targetVersion,
		Reason:                  wire.Reason, IdempotencyKey: key,
	}, nil
}

func (repository *Repository) resumeHTTP(writer http.ResponseWriter, request *http.Request) {
	id, actor, key, ok := mutationHTTPBoundary(writer, request)
	if !ok {
		return
	}
	command, err := readAdminResumeCommand(writer, request, key)
	if err != nil {
		writeReportError(writer, err)
		return
	}
	result, err := repository.Resume(request.Context(), actor, id, command)
	if err != nil {
		writeReportError(writer, err)
		return
	}
	writeMutationResult(writer, result)
}

func readAdminResumeCommand(writer http.ResponseWriter, request *http.Request, key string) (ResumeCommand, error) {
	var wire adminResumeWire
	body, err := readStrictJSON(writer, request, &wire, maxAdminRequestBodyBytes)
	if err != nil {
		return ResumeCommand{}, err
	}
	clear(body)
	targetVersion, err := parseReportRevision(wire.ExpectedTargetVersion)
	if err != nil {
		return ResumeCommand{}, err
	}
	return ResumeCommand{
		ExpectedTargetVersion: targetVersion, IdempotencyKey: key,
	}, nil
}

func parseReportRevision(value string) (int64, error) {
	if value == "" || value[0] == '0' {
		return 0, ErrInvalidRequest
	}
	for _, digit := range value {
		if digit < '0' || digit > '9' {
			return 0, ErrInvalidRequest
		}
	}
	revision, err := strconv.ParseInt(value, 10, 64)
	if err != nil || revision < 1 || strconv.FormatInt(revision, 10) != value {
		return 0, ErrInvalidRequest
	}
	return revision, nil
}

func mutationHTTPBoundary(
	writer http.ResponseWriter,
	request *http.Request,
) (string, authz.Actor, string, bool) {
	if !requireNoQuery(request) {
		writeReportError(writer, ErrInvalidRequest)
		return "", authz.Actor{}, "", false
	}
	id, err := reportCaseID(request)
	if err != nil {
		writeReportError(writer, err)
		return "", authz.Actor{}, "", false
	}
	actor, err := adminActor(request)
	if err != nil {
		writeReportError(writer, err)
		return "", authz.Actor{}, "", false
	}
	key, ok := reportIdempotencyKey(request)
	if !ok {
		writeReportError(writer, ErrInvalidRequest)
		return "", authz.Actor{}, "", false
	}
	return id, actor, key, true
}
