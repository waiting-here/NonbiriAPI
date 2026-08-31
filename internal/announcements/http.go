package announcements

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/strictjson"
)

type httpAPI struct {
	service *Service
}

func RegisterRoutes(users UserRouteRegistrar, admins AdminRouteRegistrar, service *Service) error {
	if isNilInterface(users) || isNilInterface(admins) || service == nil || service.repository == nil {
		return errors.New("announcements: route registrars and service are required")
	}
	api := &httpAPI{service: service}
	userRoutes := []struct {
		method, pattern string
		handler         AuthorizedUserHandler
	}{
		{http.MethodGet, routeUserAnnouncements, api.listUser},
		{http.MethodGet, routeUserAnnouncement, api.getUser},
	}
	for _, route := range userRoutes {
		if err := users.RegisterUserRoute(route.method, route.pattern, route.handler); err != nil {
			return fmt.Errorf("announcements: register %s %s: %w", route.method, route.pattern, err)
		}
	}
	adminRoutes := []struct {
		method, pattern string
		handler         AuthorizedAdminHandler
	}{
		{http.MethodGet, routeAdminAnnouncements, api.listAdmin},
		{http.MethodGet, routeAdminAnnouncement, api.getAdmin},
		{http.MethodPost, routeAdminAnnouncements, api.create},
		{http.MethodPatch, routeAdminAnnouncement, api.edit},
		{http.MethodPost, routeAdminPreview, api.preview},
		{http.MethodPost, routeAdminPublish, api.publish},
		{http.MethodPost, routeAdminWithdraw, api.withdraw},
		{http.MethodDelete, routeAdminAnnouncement, api.delete},
	}
	for _, route := range adminRoutes {
		if err := admins.RegisterAdminRoute(route.method, route.pattern, route.handler); err != nil {
			return fmt.Errorf("announcements: register %s %s: %w", route.method, route.pattern, err)
		}
	}
	return nil
}

func (api *httpAPI) getAdmin(writer http.ResponseWriter, request *http.Request, principal AdminPrincipal) {
	if !requireNoBody(writer, request) || !requireEmptyQuery(writer, request) {
		return
	}
	id := request.PathValue("id")
	response, err := api.service.GetAdmin(request.Context(), principal.UserID, id)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, response)
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

type nullableTimeField struct {
	Value *int64
	Set   bool
}

func (field *nullableTimeField) UnmarshalJSON(data []byte) error {
	if field == nil {
		return errors.New("nil field")
	}
	field.Set = true
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		field.Value = nil
		return nil
	}
	var value int64
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON")
	}
	field.Value = &value
	return nil
}

type draftRequest struct {
	TitleZH     requestField[string] `json:"title_zh"`
	BodyZH      requestField[string] `json:"body_zh"`
	TitleEN     requestField[string] `json:"title_en"`
	BodyEN      requestField[string] `json:"body_en"`
	Severity    requestField[string] `json:"severity"`
	Pinned      requestField[bool]   `json:"pinned"`
	Dismissible requestField[bool]   `json:"dismissible"`
	ExpiresAt   nullableTimeField    `json:"expires_at"`
}

type previewRequest struct {
	ExpectedRevision requestField[string] `json:"expected_revision"`
	TitleZH          requestField[string] `json:"title_zh"`
	BodyZH           requestField[string] `json:"body_zh"`
	TitleEN          requestField[string] `json:"title_en"`
	BodyEN           requestField[string] `json:"body_en"`
}

type editRequest struct {
	ExpectedRevision requestField[string] `json:"expected_revision"`
	TitleZH          requestField[string] `json:"title_zh"`
	BodyZH           requestField[string] `json:"body_zh"`
	TitleEN          requestField[string] `json:"title_en"`
	BodyEN           requestField[string] `json:"body_en"`
	Severity         requestField[string] `json:"severity"`
	Pinned           requestField[bool]   `json:"pinned"`
	Dismissible      requestField[bool]   `json:"dismissible"`
	ExpiresAt        nullableTimeField    `json:"expires_at"`
}

type expectedRevisionRequest struct {
	ExpectedRevision requestField[string] `json:"expected_revision"`
}

type withdrawRequest struct {
	ExpectedRevision requestField[string] `json:"expected_revision"`
	Reason           requestField[string] `json:"reason"`
}

type deleteRequest struct {
	ExpectedRevision requestField[string] `json:"expected_revision"`
	Confirmation     requestField[string] `json:"confirmation"`
	Reason           requestField[string] `json:"reason"`
}

func (api *httpAPI) listUser(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	if !requireNoBody(writer, request) {
		return
	}
	values, ok := strictQuery(writer, request, "cursor", "limit")
	if !ok {
		return
	}
	query, ok := pageQuery(writer, values)
	if !ok {
		return
	}
	response, err := api.service.ListUser(request.Context(), principal.UserID, query)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, response)
}

func (api *httpAPI) getUser(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	if !requireNoBody(writer, request) || !requireEmptyQuery(writer, request) {
		return
	}
	id := request.PathValue("id")
	response, err := api.service.GetUser(request.Context(), principal.UserID, id)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, response)
}

func (api *httpAPI) listAdmin(writer http.ResponseWriter, request *http.Request, principal AdminPrincipal) {
	if !requireNoBody(writer, request) {
		return
	}
	values, ok := strictQuery(writer, request, "state", "severity", "cursor", "limit")
	if !ok {
		return
	}
	page, ok := pageQuery(writer, values)
	if !ok {
		return
	}
	query := AdminListQuery{Cursor: page.Cursor, Limit: page.Limit}
	if value, present := values["state"]; present {
		query.State = value[0]
		if query.State == "" {
			writeError(writer, ErrInvalidRequest)
			return
		}
	}
	if value, present := values["severity"]; present {
		query.Severity = value[0]
		if query.Severity == "" {
			writeError(writer, ErrInvalidRequest)
			return
		}
	}
	response, err := api.service.ListAdmin(request.Context(), principal.UserID, query)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, response)
}

func (api *httpAPI) create(writer http.ResponseWriter, request *http.Request, principal AdminPrincipal) {
	if !requireEmptyQuery(writer, request) {
		return
	}
	var body draftRequest
	if !decodeStrictBody(writer, request, &body) {
		return
	}
	if !allCreateDraftFields(body) {
		writeError(writer, ErrInvalidRequest)
		return
	}
	patch, canonical := body.patchAndCanonical()
	mutation, ok := requestMutation(writer, request, routeAdminAnnouncements, nil, canonical)
	if !ok {
		return
	}
	result, err := api.service.Create(request.Context(), principal.UserID, mutation, patch)
	writeMutation(writer, result, err)
}

func (api *httpAPI) edit(writer http.ResponseWriter, request *http.Request, principal AdminPrincipal) {
	if !requireEmptyQuery(writer, request) {
		return
	}
	id := request.PathValue("id")
	if !dbAnnouncementID(id) {
		writeError(writer, ErrNotFound)
		return
	}
	var body editRequest
	if !decodeStrictBody(writer, request, &body) {
		return
	}
	if !body.ExpectedRevision.Set {
		writeError(writer, ErrInvalidRequest)
		return
	}
	revision, ok := parseRevision(writer, body.ExpectedRevision.Value)
	if !ok {
		return
	}
	patch, canonical := body.draft().patchAndCanonical()
	canonical["expected_revision"] = body.ExpectedRevision.Value
	if !patchHasAny(patch) {
		writeError(writer, ErrInvalidRequest)
		return
	}
	mutation, ok := requestMutation(writer, request, routeAdminAnnouncement, []string{id}, canonical)
	if !ok {
		return
	}
	result, err := api.service.Edit(request.Context(), principal.UserID, id, mutation, revision, patch)
	writeMutation(writer, result, err)
}

func (body editRequest) draft() draftRequest {
	return draftRequest{
		TitleZH: body.TitleZH, BodyZH: body.BodyZH, TitleEN: body.TitleEN, BodyEN: body.BodyEN,
		Severity: body.Severity, Pinned: body.Pinned, Dismissible: body.Dismissible, ExpiresAt: body.ExpiresAt,
	}
}

func (api *httpAPI) preview(writer http.ResponseWriter, request *http.Request, principal AdminPrincipal) {
	if !requireEmptyQuery(writer, request) {
		return
	}
	id := request.PathValue("id")
	if !dbAnnouncementID(id) {
		writeError(writer, ErrNotFound)
		return
	}
	var body previewRequest
	if !decodeStrictBody(writer, request, &body) {
		return
	}
	if !body.ExpectedRevision.Set {
		writeError(writer, ErrInvalidRequest)
		return
	}
	revision, ok := parseRevision(writer, body.ExpectedRevision.Value)
	if !ok {
		return
	}
	patch := DraftPatch{}
	if body.TitleZH.Set {
		patch.TitleZH = &body.TitleZH.Value
	}
	if body.BodyZH.Set {
		patch.BodyZH = &body.BodyZH.Value
	}
	if body.TitleEN.Set {
		patch.TitleEN = &body.TitleEN.Value
	}
	if body.BodyEN.Set {
		patch.BodyEN = &body.BodyEN.Value
	}
	response, err := api.service.Preview(request.Context(), principal.UserID, id, PreviewInput{ExpectedRevision: revision, Draft: patch})
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, response)
}

func (api *httpAPI) publish(writer http.ResponseWriter, request *http.Request, principal AdminPrincipal) {
	id, body, canonical, ok := api.expectedRevisionMutation(writer, request)
	if !ok {
		return
	}
	mutation, ok := requestMutation(writer, request, routeAdminPublish, []string{id}, canonical)
	if !ok {
		return
	}
	result, err := api.service.Publish(request.Context(), principal.UserID, id, mutation, body)
	writeMutation(writer, result, err)
}

func (api *httpAPI) withdraw(writer http.ResponseWriter, request *http.Request, principal AdminPrincipal) {
	if !requireEmptyQuery(writer, request) {
		return
	}
	id := request.PathValue("id")
	if !dbAnnouncementID(id) {
		writeError(writer, ErrNotFound)
		return
	}
	var body withdrawRequest
	if !decodeStrictBody(writer, request, &body) {
		return
	}
	if !body.ExpectedRevision.Set || !body.Reason.Set {
		writeError(writer, ErrInvalidRequest)
		return
	}
	revision, ok := parseRevision(writer, body.ExpectedRevision.Value)
	if !ok {
		return
	}
	canonical := map[string]any{"expected_revision": body.ExpectedRevision.Value, "reason": body.Reason.Value}
	mutation, ok := requestMutation(writer, request, routeAdminWithdraw, []string{id}, canonical)
	if !ok {
		return
	}
	result, err := api.service.Withdraw(request.Context(), principal.UserID, id, mutation, revision, body.Reason.Value)
	writeMutation(writer, result, err)
}

func (api *httpAPI) delete(writer http.ResponseWriter, request *http.Request, principal AdminPrincipal) {
	if !requireEmptyQuery(writer, request) {
		return
	}
	id := request.PathValue("id")
	if !dbAnnouncementID(id) {
		writeError(writer, ErrNotFound)
		return
	}
	var body deleteRequest
	if !decodeStrictBody(writer, request, &body) {
		return
	}
	if !body.ExpectedRevision.Set || !body.Confirmation.Set || !body.Reason.Set {
		writeError(writer, ErrInvalidRequest)
		return
	}
	revision, ok := parseRevision(writer, body.ExpectedRevision.Value)
	if !ok {
		return
	}
	canonical := map[string]any{
		"expected_revision": body.ExpectedRevision.Value, "confirmation": body.Confirmation.Value, "reason": body.Reason.Value,
	}
	mutation, ok := requestMutation(writer, request, routeAdminAnnouncement, []string{id}, canonical)
	if !ok {
		return
	}
	result, err := api.service.Delete(request.Context(), principal.UserID, id, mutation, revision, body.Confirmation.Value, body.Reason.Value)
	writeMutation(writer, result, err)
}

func (api *httpAPI) expectedRevisionMutation(writer http.ResponseWriter, request *http.Request) (string, int64, map[string]any, bool) {
	if !requireEmptyQuery(writer, request) {
		return "", 0, nil, false
	}
	id := request.PathValue("id")
	if !dbAnnouncementID(id) {
		writeError(writer, ErrNotFound)
		return "", 0, nil, false
	}
	var body expectedRevisionRequest
	if !decodeStrictBody(writer, request, &body) {
		return "", 0, nil, false
	}
	if !body.ExpectedRevision.Set {
		writeError(writer, ErrInvalidRequest)
		return "", 0, nil, false
	}
	revision, ok := parseRevision(writer, body.ExpectedRevision.Value)
	if !ok {
		return "", 0, nil, false
	}
	return id, revision, map[string]any{"expected_revision": body.ExpectedRevision.Value}, true
}

func (body draftRequest) patchAndCanonical() (DraftPatch, map[string]any) {
	patch := DraftPatch{ExpiresAt: NullableTime{Set: body.ExpiresAt.Set, Value: cloneInt64(body.ExpiresAt.Value)}}
	canonical := make(map[string]any, 8)
	if body.TitleZH.Set {
		patch.TitleZH = &body.TitleZH.Value
		canonical["title_zh"] = body.TitleZH.Value
	}
	if body.BodyZH.Set {
		patch.BodyZH = &body.BodyZH.Value
		canonical["body_zh"] = body.BodyZH.Value
	}
	if body.TitleEN.Set {
		patch.TitleEN = &body.TitleEN.Value
		canonical["title_en"] = body.TitleEN.Value
	}
	if body.BodyEN.Set {
		patch.BodyEN = &body.BodyEN.Value
		canonical["body_en"] = body.BodyEN.Value
	}
	if body.Severity.Set {
		patch.Severity = &body.Severity.Value
		canonical["severity"] = body.Severity.Value
	}
	if body.Pinned.Set {
		patch.Pinned = &body.Pinned.Value
		canonical["pinned"] = body.Pinned.Value
	}
	if body.Dismissible.Set {
		patch.Dismissible = &body.Dismissible.Value
		canonical["dismissible"] = body.Dismissible.Value
	}
	if body.ExpiresAt.Set {
		canonical["expires_at"] = body.ExpiresAt.Value
	}
	return patch, canonical
}

func allCreateDraftFields(body draftRequest) bool {
	if !body.TitleZH.Set || !body.BodyZH.Set || !body.TitleEN.Set || !body.BodyEN.Set ||
		!body.Severity.Set || !body.Pinned.Set || !body.Dismissible.Set {
		return false
	}
	return true
}

func decodeStrictBody(writer http.ResponseWriter, request *http.Request, destination any) bool {
	if request == nil || request.Body == nil || !jsonContentType(request.Header.Get("Content-Type")) {
		writeError(writer, ErrInvalidRequest)
		return false
	}
	limited := http.MaxBytesReader(writer, request.Body, idempotency.MaxControlBodyBytes)
	body, err := io.ReadAll(limited)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			httperr.WriteError(writer, httperr.New(httperr.CodePayloadTooLarge, "request body is too large"))
		} else {
			writeError(writer, ErrInvalidRequest)
		}
		return false
	}
	if len(body) == 0 || strictjson.ValidateObject(body) != nil {
		writeError(writer, ErrInvalidRequest)
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writeError(writer, ErrInvalidRequest)
		return false
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		writeError(writer, ErrInvalidRequest)
		return false
	}
	return true
}

func jsonContentType(value string) bool {
	mediaType, _, err := mime.ParseMediaType(value)
	return err == nil && strings.EqualFold(mediaType, "application/json")
}

func requestMutation(writer http.ResponseWriter, request *http.Request, route string, pathIDs []string, canonical any) (ControlMutation, bool) {
	keys := request.Header.Values("Idempotency-Key")
	if len(keys) != 1 {
		writeError(writer, ErrInvalidRequest)
		return ControlMutation{}, false
	}
	if _, err := idempotency.KeyHash(keys[0]); err != nil {
		writeError(writer, ErrInvalidRequest)
		return ControlMutation{}, false
	}
	body, err := idempotency.CanonicalJSON(canonical)
	if err != nil {
		writeError(writer, ErrInvalidRequest)
		return ControlMutation{}, false
	}
	return ControlMutation{
		IdempotencyKey: keys[0], Method: request.Method, Route: route,
		PathIDs: append([]string(nil), pathIDs...), CanonicalBody: body,
	}, true
}

func parseRevision(writer http.ResponseWriter, value string) (int64, bool) {
	if value == "" || (len(value) > 1 && value[0] == '0') {
		writeError(writer, ErrInvalidRequest)
		return 0, false
	}
	for index := range value {
		if value[index] < '0' || value[index] > '9' {
			writeError(writer, ErrInvalidRequest)
			return 0, false
		}
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 1 {
		writeError(writer, ErrInvalidRequest)
		return 0, false
	}
	return parsed, true
}

func strictQuery(writer http.ResponseWriter, request *http.Request, allowed ...string) (url.Values, bool) {
	if request == nil || request.URL == nil || request.URL.ForceQuery {
		writeError(writer, ErrInvalidRequest)
		return nil, false
	}
	values, err := url.ParseQuery(request.URL.RawQuery)
	if err != nil {
		writeError(writer, ErrInvalidRequest)
		return nil, false
	}
	allow := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		allow[key] = struct{}{}
	}
	for key, entries := range values {
		if _, ok := allow[key]; !ok || len(entries) != 1 {
			writeError(writer, ErrInvalidRequest)
			return nil, false
		}
	}
	return values, true
}

func pageQuery(writer http.ResponseWriter, values url.Values) (PageQuery, bool) {
	query := PageQuery{}
	if cursor, present := values["cursor"]; present {
		if cursor[0] == "" || len(cursor[0]) > maxCursorBytes {
			writeError(writer, ErrInvalidRequest)
			return PageQuery{}, false
		}
		query.Cursor = cursor[0]
	}
	if limit, present := values["limit"]; present {
		parsed, err := strconv.Atoi(limit[0])
		if err != nil || parsed < 1 || parsed > maxPageLimit {
			writeError(writer, ErrInvalidRequest)
			return PageQuery{}, false
		}
		query.Limit = parsed
	}
	return query, true
}

func requireNoBody(writer http.ResponseWriter, request *http.Request) bool {
	if request == nil || request.Body == nil {
		return true
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, 1))
	if err != nil || len(body) != 0 {
		writeError(writer, ErrInvalidRequest)
		return false
	}
	return true
}

func requireEmptyQuery(writer http.ResponseWriter, request *http.Request) bool {
	values, ok := strictQuery(writer, request)
	return ok && len(values) == 0
}

func writeMutation[T any](writer http.ResponseWriter, result MutationResult[T], err error) {
	if err != nil {
		writeError(writer, err)
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	if len(result.Body) == 0 {
		writer.WriteHeader(result.Status)
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(result.Status)
	_, _ = writer.Write(result.Body)
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	httperr.WriteJSON(writer, status, value)
}

func writeError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrInvalidRequest):
		httperr.WriteError(writer, httperr.New(httperr.CodeInvalidRequest, "invalid request"))
	case errors.Is(err, ErrUnauthorized):
		httperr.WriteError(writer, httperr.New(httperr.CodeUnauthorized, "authentication required"))
	case errors.Is(err, ErrForbidden):
		httperr.WriteError(writer, httperr.New(httperr.CodeForbidden, "access forbidden"))
	case errors.Is(err, ErrNotFound):
		httperr.WriteError(writer, httperr.New(httperr.CodeNotFound, "resource not found"))
	case errors.Is(err, ErrConflict):
		httperr.WriteError(writer, httperr.New(httperr.CodeConflict, "resource state conflict"))
	case errors.Is(err, ErrPayloadTooLarge):
		httperr.WriteError(writer, httperr.New(httperr.CodePayloadTooLarge, "request body is too large"))
	case errors.Is(err, ErrResourceLimit):
		httperr.WriteError(writer, httperr.New(httperr.CodeResourceLimitExceeded, "resource limit exceeded"))
	case errors.Is(err, ErrUnavailable):
		httperr.WriteError(writer, httperr.New(httperr.CodeServiceUnavailable, "service unavailable"))
	default:
		httperr.WriteError(writer, httperr.New(httperr.CodeInternal, "internal error"))
	}
}
