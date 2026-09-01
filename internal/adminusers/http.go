package adminusers

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/strictjson"
)

const (
	routeUsers            = "/admin/api/users"
	routeUser             = "/admin/api/users/{id}"
	routeBan              = "/admin/api/users/{id}/ban"
	routeUnban            = "/admin/api/users/{id}/unban"
	routeUsage            = "/admin/api/usage"
	routeActivity         = "/admin/api/activity"
	routeEndpointOverview = "/admin/api/overview/endpoints"
	maxRawQueryBytes      = 8192
)

type httpAPI struct{ service *Service }

func RegisterRoutes(registrar AdminRouteRegistrar, service *Service) error {
	if nilDependency(registrar) || service == nil || service.database == nil {
		return errors.New("adminusers: route registrar and service are required")
	}
	api := &httpAPI{service: service}
	routes := []struct {
		method, pattern string
		handler         AuthorizedAdminHandler
	}{
		{http.MethodGet, routeUsers, api.listUsers},
		{http.MethodGet, routeUser, api.getUser},
		{http.MethodPatch, routeUser, api.patchUser},
		{http.MethodPost, routeBan, api.banUser},
		{http.MethodPost, routeUnban, api.unbanUser},
		{http.MethodGet, routeUsage, api.getUsage},
		{http.MethodGet, routeActivity, api.getActivity},
		{http.MethodGet, routeEndpointOverview, api.getEndpointOverview},
	}
	for _, route := range routes {
		if err := registrar.RegisterAdminRoute(route.method, route.pattern, route.handler); err != nil {
			return fmt.Errorf("adminusers: register %s %s: %w", route.method, route.pattern, err)
		}
	}
	return nil
}

func (api *httpAPI) listUsers(writer http.ResponseWriter, request *http.Request, principal AdminPrincipal) {
	if !requireNoBody(writer, request) {
		return
	}
	values, ok := strictQuery(writer, request, "is_banned", "q", "cursor", "limit")
	if !ok {
		return
	}
	query := UserListQuery{}
	if raw, set := singleQuery(values, "is_banned"); set {
		value, err := strconv.ParseBool(raw)
		if err != nil || strconv.FormatBool(value) != raw {
			writeError(writer, ErrInvalidRequest)
			return
		}
		query.IsBanned = &value
	}
	if raw, set := singleQuery(values, "q"); set {
		if !validFilter(raw) {
			writeError(writer, ErrInvalidRequest)
			return
		}
		query.Q = raw
	}
	if !parsePageQuery(writer, values, &query.Cursor, &query.Limit) {
		return
	}
	page, err := api.service.ListUsers(request.Context(), principal.UserID, query)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, page)
}

func (api *httpAPI) getUser(writer http.ResponseWriter, request *http.Request, principal AdminPrincipal) {
	if !requireReadRequest(writer, request) {
		return
	}
	userID, ok := pathUserID(request)
	if !ok {
		writeError(writer, ErrNotFound)
		return
	}
	user, err := api.service.GetUser(request.Context(), principal.UserID, userID)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, user)
}

func (api *httpAPI) patchUser(writer http.ResponseWriter, request *http.Request, principal AdminPrincipal) {
	userID, ok := pathUserID(request)
	if !ok {
		writeError(writer, ErrNotFound)
		return
	}
	object, ok := decodeStrictObject(writer, request)
	if !ok {
		return
	}
	mode, ok := requiredString(object, "mode")
	if !ok {
		writeError(writer, ErrInvalidRequest)
		return
	}
	switch mode {
	case "profile":
		input, ok := decodeProfileMutation(object)
		if !ok {
			writeError(writer, ErrInvalidRequest)
			return
		}
		control, ok := makeControl(writer, request, routeUser, userID, profileCanonical(input))
		if !ok {
			return
		}
		result, err := api.service.Profile(request.Context(), principal.UserID, userID, control, input)
		if err != nil {
			writeError(writer, err)
			return
		}
		writeMutation(writer, result.Status, result.Body)
	case "economy":
		input, ok := decodeEconomyMutation(object)
		if !ok {
			writeError(writer, ErrInvalidRequest)
			return
		}
		control, ok := makeControl(writer, request, routeUser, userID, economyCanonical(input))
		if !ok {
			return
		}
		result, err := api.service.Economy(request.Context(), principal.UserID, userID, control, input)
		if err != nil {
			writeError(writer, err)
			return
		}
		writeMutation(writer, result.Status, result.Body)
	default:
		writeError(writer, ErrInvalidRequest)
	}
}

func (api *httpAPI) banUser(writer http.ResponseWriter, request *http.Request, principal AdminPrincipal) {
	userID, ok := pathUserID(request)
	if !ok {
		writeError(writer, ErrNotFound)
		return
	}
	object, ok := decodeStrictObject(writer, request)
	if !ok {
		return
	}
	if !exactObject(object, "expected_revision", "reason", "duration_seconds") {
		writeError(writer, ErrInvalidRequest)
		return
	}
	revision, ok := requiredRevision(object, "expected_revision")
	if !ok {
		writeError(writer, ErrInvalidRequest)
		return
	}
	reason, ok := requiredString(object, "reason")
	if !ok || !validReason(reason) {
		writeError(writer, ErrInvalidRequest)
		return
	}
	duration, ok := nullableInteger(object["duration_seconds"])
	if !ok || duration != nil && (*duration <= 0 || *duration > db.MaxBanDurationSeconds) {
		writeError(writer, ErrInvalidRequest)
		return
	}
	canonical := map[string]any{"expected_revision": revision.Decimal(), "reason": reason, "duration_seconds": duration}
	control, ok := makeControl(writer, request, routeBan, userID, canonical)
	if !ok {
		return
	}
	result, err := api.service.Ban(request.Context(), principal.UserID, userID, control, BanMutation{ExpectedRevision: revision, Reason: reason, DurationSeconds: duration})
	if err != nil {
		writeError(writer, err)
		return
	}
	writeMutation(writer, result.Status, result.Body)
}

func (api *httpAPI) unbanUser(writer http.ResponseWriter, request *http.Request, principal AdminPrincipal) {
	userID, ok := pathUserID(request)
	if !ok {
		writeError(writer, ErrNotFound)
		return
	}
	object, ok := decodeStrictObject(writer, request)
	if !ok {
		return
	}
	if !exactObject(object, "expected_revision") {
		writeError(writer, ErrInvalidRequest)
		return
	}
	revision, ok := requiredRevision(object, "expected_revision")
	if !ok {
		writeError(writer, ErrInvalidRequest)
		return
	}
	control, ok := makeControl(writer, request, routeUnban, userID, map[string]any{"expected_revision": revision.Decimal()})
	if !ok {
		return
	}
	result, err := api.service.Unban(request.Context(), principal.UserID, userID, control, revision)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeMutation(writer, result.Status, result.Body)
}

func (api *httpAPI) getUsage(writer http.ResponseWriter, request *http.Request, principal AdminPrincipal) {
	if !requireNoBody(writer, request) {
		return
	}
	values, ok := strictQuery(writer, request, "group_by", "cursor", "limit")
	if !ok {
		return
	}
	groupBy, set := singleQuery(values, "group_by")
	if !set || groupBy != "site" && groupBy != "user" {
		writeError(writer, ErrInvalidRequest)
		return
	}
	if groupBy == "site" {
		if _, cursor := values["cursor"]; cursor || values["limit"] != nil {
			writeError(writer, ErrInvalidRequest)
			return
		}
		usage, err := api.service.SiteUsage(request.Context(), principal.UserID)
		if err != nil {
			writeError(writer, err)
			return
		}
		writeJSON(writer, http.StatusOK, usage)
		return
	}
	query := PageQuery{}
	if !parsePageQuery(writer, values, &query.Cursor, &query.Limit) {
		return
	}
	page, err := api.service.UserUsage(request.Context(), principal.UserID, query)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, page)
}

func (api *httpAPI) getActivity(writer http.ResponseWriter, request *http.Request, principal AdminPrincipal) {
	if !requireNoBody(writer, request) {
		return
	}
	values, ok := strictQuery(writer, request, "cursor", "limit")
	if !ok {
		return
	}
	query := PageQuery{}
	if !parsePageQuery(writer, values, &query.Cursor, &query.Limit) {
		return
	}
	page, err := api.service.Activity(request.Context(), principal.UserID, query)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, page)
}

func (api *httpAPI) getEndpointOverview(writer http.ResponseWriter, request *http.Request, principal AdminPrincipal) {
	if !requireNoBody(writer, request) {
		return
	}
	values, ok := strictQuery(writer, request, "q", "cursor", "limit")
	if !ok {
		return
	}
	query := EndpointOverviewQuery{}
	if raw, set := singleQuery(values, "q"); set {
		if !validFilter(raw) {
			writeError(writer, ErrInvalidRequest)
			return
		}
		query.Q = raw
	}
	if !parsePageQuery(writer, values, &query.Cursor, &query.Limit) {
		return
	}
	page, err := api.service.EndpointOverview(request.Context(), principal.UserID, query)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, page)
}

func decodeProfileMutation(object map[string]json.RawMessage) (ProfileMutation, bool) {
	if !allowedObject(object, []string{"mode", "expected_revision"}, []string{"endpoint_limit", "rpm_limit", "concurrency_limit", "lang", "level"}) {
		return ProfileMutation{}, false
	}
	revision, ok := requiredRevision(object, "expected_revision")
	if !ok {
		return ProfileMutation{}, false
	}
	input := ProfileMutation{ExpectedRevision: revision}
	if raw, set := object["endpoint_limit"]; set {
		input.EndpointLimitSet = true
		input.EndpointLimit, ok = nullableDecimalInteger(raw, 0, 10000)
		if !ok {
			return ProfileMutation{}, false
		}
	}
	if raw, set := object["rpm_limit"]; set {
		input.RPMLimitSet = true
		input.RPMLimit, ok = nullableDecimalInteger(raw, 1, db.MaxUserRPMLimit)
		if !ok {
			return ProfileMutation{}, false
		}
	}
	if raw, set := object["concurrency_limit"]; set {
		input.ConcurrencySet = true
		input.Concurrency, ok = nullableDecimalInteger(raw, 1, db.MaxUserConcurrencyLimit)
		if !ok {
			return ProfileMutation{}, false
		}
	}
	if raw, set := object["lang"]; set {
		input.LangSet = true
		if json.Unmarshal(raw, &input.Lang) != nil || input.Lang != "zh" && input.Lang != "en" {
			return ProfileMutation{}, false
		}
	}
	if raw, set := object["level"]; set {
		input.LevelSet = true
		value, ok := nullableInteger(raw)
		if !ok || value != nil && (*value < 1 || *value > 5) {
			return ProfileMutation{}, false
		}
		if value != nil {
			level := int(*value)
			input.Level = &level
		}
	}
	if !input.EndpointLimitSet && !input.RPMLimitSet && !input.ConcurrencySet && !input.LangSet && !input.LevelSet {
		return ProfileMutation{}, false
	}
	return input, true
}

func decodeEconomyMutation(object map[string]json.RawMessage) (EconomyMutation, bool) {
	if !exactObject(object, "mode", "expected_revision", "target", "direction", "amount", "reason") {
		return EconomyMutation{}, false
	}
	revision, ok := requiredRevision(object, "expected_revision")
	if !ok {
		return EconomyMutation{}, false
	}
	target, ok := requiredString(object, "target")
	if !ok || target != "balance" && target != "donation_credit" {
		return EconomyMutation{}, false
	}
	direction, ok := requiredString(object, "direction")
	if !ok || direction != "increase" && direction != "decrease" {
		return EconomyMutation{}, false
	}
	amount, ok := requiredString(object, "amount")
	if !ok {
		return EconomyMutation{}, false
	}
	milli, ok := parsePointsMilli(amount)
	if !ok || milli <= 0 {
		return EconomyMutation{}, false
	}
	reason, ok := requiredString(object, "reason")
	if !ok || !validReason(reason) {
		return EconomyMutation{}, false
	}
	return EconomyMutation{ExpectedRevision: revision, Target: target, Direction: direction, AmountMilli: milli, Reason: reason}, true
}

func profileCanonical(input ProfileMutation) map[string]any {
	canonical := map[string]any{"mode": "profile", "expected_revision": input.ExpectedRevision.Decimal()}
	if input.EndpointLimitSet {
		canonical["endpoint_limit"] = nullableDecimal(input.EndpointLimit)
	}
	if input.RPMLimitSet {
		canonical["rpm_limit"] = nullableDecimal(input.RPMLimit)
	}
	if input.ConcurrencySet {
		canonical["concurrency_limit"] = nullableDecimal(input.Concurrency)
	}
	if input.LangSet {
		canonical["lang"] = input.Lang
	}
	if input.LevelSet {
		if input.Level == nil {
			canonical["level"] = nil
		} else {
			canonical["level"] = *input.Level
		}
	}
	return canonical
}

func economyCanonical(input EconomyMutation) map[string]any {
	amount := formatMilliPoints(big.NewInt(input.AmountMilli))
	return map[string]any{
		"mode": "economy", "expected_revision": input.ExpectedRevision.Decimal(),
		"target": input.Target, "direction": input.Direction, "amount": amount, "reason": input.Reason,
	}
}

func nullableDecimal(value *int64) any {
	if value == nil {
		return nil
	}
	return strconv.FormatInt(*value, 10)
}

func decodeStrictObject(writer http.ResponseWriter, request *http.Request) (map[string]json.RawMessage, bool) {
	if !requireEmptyQuery(writer, request) || request == nil || request.Body == nil {
		return nil, false
	}
	limited := http.MaxBytesReader(writer, request.Body, idempotency.MaxControlBodyBytes)
	body, err := io.ReadAll(limited)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(writer, ErrPayloadTooLarge)
		} else {
			writeError(writer, ErrInvalidRequest)
		}
		return nil, false
	}
	if len(body) == 0 || strictjson.ValidateObject(body) != nil {
		writeError(writer, ErrInvalidRequest)
		return nil, false
	}
	var object map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&object) != nil {
		writeError(writer, ErrInvalidRequest)
		return nil, false
	}
	return object, true
}

func exactObject(object map[string]json.RawMessage, keys ...string) bool {
	if len(object) != len(keys) {
		return false
	}
	for _, key := range keys {
		if _, ok := object[key]; !ok {
			return false
		}
	}
	return true
}

func allowedObject(object map[string]json.RawMessage, required, optional []string) bool {
	allowed := make(map[string]struct{}, len(required)+len(optional))
	for _, key := range append(append([]string{}, required...), optional...) {
		allowed[key] = struct{}{}
	}
	for _, key := range required {
		if _, ok := object[key]; !ok {
			return false
		}
	}
	for key := range object {
		if _, ok := allowed[key]; !ok {
			return false
		}
	}
	return true
}

func requiredString(object map[string]json.RawMessage, key string) (string, bool) {
	raw, ok := object[key]
	if !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "", false
	}
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return "", false
	}
	return value, true
}

func requiredRevision(object map[string]json.RawMessage, key string) (db.U128, bool) {
	value, ok := requiredString(object, key)
	if !ok {
		return db.U128{}, false
	}
	revision, err := db.ParseU128Decimal(value)
	return revision, err == nil && revision.Big().Sign() > 0
}

func nullableInteger(raw json.RawMessage) (*int64, bool) {
	trimmed := bytes.TrimSpace(raw)
	if bytes.Equal(trimmed, []byte("null")) {
		return nil, true
	}
	if len(trimmed) == 0 || len(trimmed) > 19 {
		return nil, false
	}
	for index, value := range trimmed {
		if value < '0' || value > '9' || index == 0 && len(trimmed) > 1 && value == '0' {
			return nil, false
		}
	}
	value, err := strconv.ParseInt(string(trimmed), 10, 64)
	if err != nil || strconv.FormatInt(value, 10) != string(trimmed) {
		return nil, false
	}
	return &value, true
}

func nullableDecimalInteger(raw json.RawMessage, minimum, maximum int) (*int64, bool) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, true
	}
	var text string
	if json.Unmarshal(raw, &text) != nil {
		return nil, false
	}
	value, err := strconv.ParseInt(text, 10, 64)
	if err != nil || strconv.FormatInt(value, 10) != text || value < int64(minimum) || value > int64(maximum) {
		return nil, false
	}
	return &value, true
}

func parsePointsMilli(raw string) (int64, bool) {
	if raw == "" || len(raw) > 32 {
		return 0, false
	}
	wholeText, fractionText := raw, ""
	if dot := strings.IndexByte(raw, '.'); dot >= 0 {
		if strings.IndexByte(raw[dot+1:], '.') >= 0 {
			return 0, false
		}
		wholeText, fractionText = raw[:dot], raw[dot+1:]
		if len(fractionText) < 1 || len(fractionText) > 3 {
			return 0, false
		}
	}
	if wholeText == "" || len(wholeText) > 1 && wholeText[0] == '0' || !asciiDigits(wholeText) || !asciiDigits(fractionText) {
		return 0, false
	}
	whole, err := strconv.ParseInt(wholeText, 10, 64)
	if err != nil || whole < 0 {
		return 0, false
	}
	fraction := int64(0)
	for index := range fractionText {
		fraction = fraction*10 + int64(fractionText[index]-'0')
	}
	for digits := len(fractionText); digits < 3; digits++ {
		fraction *= 10
	}
	if whole > (db.MaxMoneyMilli-fraction)/1000 {
		return 0, false
	}
	return whole*1000 + fraction, true
}

func asciiDigits(value string) bool {
	for index := range value {
		if value[index] < '0' || value[index] > '9' {
			return false
		}
	}
	return true
}

func validReason(value string) bool {
	return validText(value, 1, 1024, 4096)
}

func validFilter(value string) bool {
	return validText(value, 1, utf8.RuneCountInString(value), 512)
}

func validText(value string, minRunes, maxRunes, maxBytes int) bool {
	if !utf8.ValidString(value) || len(value) > maxBytes {
		return false
	}
	runes := utf8.RuneCountInString(value)
	if runes < minRunes || runes > maxRunes {
		return false
	}
	for _, char := range value {
		if char == 0 || char < 0x20 || char >= 0x7f && char <= 0x9f {
			return false
		}
	}
	return true
}

func makeControl(writer http.ResponseWriter, request *http.Request, route string, userID int64, canonicalValue any) (ControlMutation, bool) {
	key, ok := requireIdempotencyKey(writer, request)
	if !ok {
		return ControlMutation{}, false
	}
	canonical, err := idempotency.CanonicalJSON(canonicalValue)
	if err != nil {
		writeError(writer, ErrInvalidRequest)
		return ControlMutation{}, false
	}
	return ControlMutation{IdempotencyKey: key, Method: request.Method, Route: route, PathIDs: []string{strconv.FormatInt(userID, 10)}, CanonicalBody: canonical}, true
}

func requireIdempotencyKey(writer http.ResponseWriter, request *http.Request) (string, bool) {
	values := request.Header.Values("Idempotency-Key")
	if len(values) != 1 {
		writeError(writer, ErrInvalidRequest)
		return "", false
	}
	if _, err := idempotency.KeyHash(values[0]); err != nil {
		writeError(writer, ErrInvalidRequest)
		return "", false
	}
	return values[0], true
}

func pathUserID(request *http.Request) (int64, bool) {
	if request == nil {
		return 0, false
	}
	raw := request.PathValue("id")
	value, err := strconv.ParseInt(raw, 10, 64)
	return value, err == nil && value > 0 && strconv.FormatInt(value, 10) == raw
}

func strictQuery(writer http.ResponseWriter, request *http.Request, allowed ...string) (url.Values, bool) {
	if request == nil || request.URL == nil || len(request.URL.RawQuery) > maxRawQueryBytes {
		writeError(writer, ErrInvalidRequest)
		return nil, false
	}
	values, err := url.ParseQuery(request.URL.RawQuery)
	if err != nil {
		writeError(writer, ErrInvalidRequest)
		return nil, false
	}
	set := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		set[key] = struct{}{}
	}
	for key, entries := range values {
		if _, ok := set[key]; !ok || len(entries) != 1 {
			writeError(writer, ErrInvalidRequest)
			return nil, false
		}
	}
	return values, true
}

func singleQuery(values url.Values, key string) (string, bool) {
	entries, ok := values[key]
	if !ok {
		return "", false
	}
	return entries[0], true
}

func parsePageQuery(writer http.ResponseWriter, values url.Values, cursor *string, limit *int) bool {
	if raw, set := singleQuery(values, "cursor"); set {
		if raw == "" {
			writeError(writer, ErrInvalidRequest)
			return false
		}
		*cursor = raw
	}
	if raw, set := singleQuery(values, "limit"); set {
		value, err := strconv.Atoi(raw)
		if err != nil || strconv.Itoa(value) != raw || value < 1 || value > maxPageLimit {
			writeError(writer, ErrInvalidRequest)
			return false
		}
		*limit = value
	}
	return true
}

func requireReadRequest(writer http.ResponseWriter, request *http.Request) bool {
	return requireEmptyQuery(writer, request) && requireNoBody(writer, request)
}

func requireEmptyQuery(writer http.ResponseWriter, request *http.Request) bool {
	values, ok := strictQuery(writer, request)
	if !ok {
		return false
	}
	if len(values) != 0 {
		writeError(writer, ErrInvalidRequest)
		return false
	}
	return true
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

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Cache-Control", "no-store")
	httperr.WriteJSON(writer, status, value)
}

func writeMutation(writer http.ResponseWriter, status int, body []byte) {
	writer.Header().Set("Cache-Control", "no-store")
	if status == http.StatusNoContent {
		writer.WriteHeader(status)
		return
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_, _ = writer.Write(body)
}

func writeError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrInvalidRequest):
		httperr.WriteError(writer, httperr.New(httperr.CodeInvalidRequest, "invalid request"))
	case errors.Is(err, ErrUnauthorized):
		httperr.WriteError(writer, httperr.New(httperr.CodeUnauthorized, "authentication required"))
	case errors.Is(err, ErrForbidden):
		httperr.WriteError(writer, httperr.New(httperr.CodeForbidden, "access denied"))
	case errors.Is(err, ErrNotFound):
		httperr.WriteError(writer, httperr.New(httperr.CodeNotFound, "not found"))
	case errors.Is(err, ErrConflict):
		httperr.WriteError(writer, httperr.New(httperr.CodeConflict, "request conflicts with current state"))
	case errors.Is(err, ErrPayloadTooLarge):
		httperr.WriteError(writer, httperr.New(httperr.CodePayloadTooLarge, "payload is too large"))
	case errors.Is(err, ErrResourceLimit):
		httperr.WriteError(writer, httperr.New(httperr.CodeResourceLimitExceeded, "resource limit exceeded"))
	case errors.Is(err, ErrUnavailable), errors.Is(err, ErrRetryable):
		httperr.WriteError(writer, httperr.New(httperr.CodeServiceUnavailable, "service unavailable"))
	default:
		httperr.WriteError(writer, httperr.New(httperr.CodeInternal, "internal error"))
	}
}
