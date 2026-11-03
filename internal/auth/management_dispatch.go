package auth

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/managementlog"
)

type ManagementEffect string

const (
	ManagementRead      ManagementEffect = "read"
	ManagementDiscovery ManagementEffect = "model_discovery"
	ManagementWrite     ManagementEffect = "write"
)

type ManagementRoute struct {
	Route      string           `json:"route"`
	Effect     ManagementEffect `json:"effect"`
	Permission string           `json:"permission,omitempty"`
}
type managementRoute struct {
	ManagementRoute
	handler http.Handler
}

// ManagementRequest addresses a registered template, not an arbitrary URL.
// Authentication, headers and idempotency keys are always supplied by the host.
type ManagementRequest struct {
	Route       string            `json:"route"`
	PathParams  map[string]string `json:"path_params,omitempty"`
	Query       map[string]string `json:"query,omitempty"`
	Body        json.RawMessage   `json:"body,omitempty"`
	BodyBase64  string            `json:"body_base64,omitempty"`
	ContentType string            `json:"content_type,omitempty"`
}

// Explicit exceptions to the default write classification. A route's spelling
// (e.g. a /preview suffix) alone must never grant immediate execution.
func managementEffect(method, pattern string) ManagementEffect {
	p := strings.TrimPrefix(strings.TrimPrefix(pattern, "/admin/api/"), "/api/steward/")
	if p == pattern || strings.HasPrefix(p, "automation/") || strings.HasPrefix(p, "auth/") || p == "login" || p == "logout" {
		return ""
	}
	if method == http.MethodGet || method == http.MethodHead {
		return ManagementRead
	}
	// A model can never approve its own plan, alter the conversation that is
	// authorizing it, or submit a recursive assistant request.
	if p == "ai/chat" || strings.HasPrefix(p, "ai/chat/") {
		return ""
	}
	if method == http.MethodPost {
		switch p {
		case "donations/{id}/keys/{keyId}/models/refresh", "ai/driver/models", "ai/drivers/{id}/models", "limited-activities/picture-book/models/refresh":
			return ManagementDiscovery
		case "donation-keys/models/refresh/selection", "donation-keys/failure-streak-reset/selection", "announcements/{id}/preview", "inactivity-policy/preview", "rewards/preview", "limited-activities/fat-fish/levels/validate", "limited-activities/picture-book/models/check", "management-operations/cleanup/prepare":
			return ManagementRead
		case "games/bidding/ai/preview", "games/bidding/history/export", "games/gwent/history/export", "games/blackjack/history/export", "games/likes/history/export":
			return ManagementRead
		}
	}
	return ManagementWrite
}

func (r *Runtime) captureManagementRoute(method, pattern string, handler http.Handler) {
	effect := managementEffect(method, pattern)
	if effect == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.managementRoutes == nil {
		r.managementRoutes = make(map[string]managementRoute)
	}
	key := method + " " + pattern
	r.managementRoutes[key] = managementRoute{ManagementRoute{key, effect, authz.StewardRoutePermission(method, pattern)}, handler}
}

func (r *Runtime) managementContext(ctx context.Context, actor authz.Actor, automatic bool) context.Context {
	if automatic {
		ctx = authz.WithAutomaticReviewAuthority(ctx, actor.UserID)
	}
	return WithSessionActor(ctx, actor)
}

// ManagementCatalog is filtered against live grants. Domain handlers perform
// their own final checks of fields, target hierarchy, donation scope and limits.
func (r *Runtime) ManagementCatalog(ctx context.Context, actor authz.Actor, automatic bool) ([]ManagementRoute, error) {
	ctx = r.managementContext(ctx, actor, automatic)
	tx, err := r.readDatabase().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	role := authz.RoleAdministrator
	if actor.Kind == authz.ActorUserSession {
		role = authz.RoleSteward
		ctx = authz.WithStewardPermission(ctx, "ai.chat")
	}
	if _, err = r.authorizer.Authorize(ctx, tx, actor, authz.Requirement{Role: role}); err != nil {
		return nil, err
	}
	var grants authz.StewardPermissions
	if actor.Kind == authz.ActorUserSession {
		grants, err = authz.ReadStewardPermissions(ctx, tx, actor.UserID)
		if err != nil {
			return nil, err
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, ErrClosed
	}
	out := []ManagementRoute{}
	for _, route := range r.managementRoutes {
		admin := strings.Contains(route.Route, " /admin/api/")
		if admin != (actor.Kind == authz.ActorAdminSession) || automatic && route.Effect == ManagementWrite {
			continue
		}
		if !admin && !grants.AllowsOperation(route.Permission) {
			continue
		}
		out = append(out, route.ManagementRoute)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Route < out[j].Route })
	return out, nil
}

func (r *Runtime) ResolveManagement(ctx context.Context, actor authz.Actor, automatic bool, input ManagementRequest, effect ManagementEffect) (ManagementRoute, error) {
	items, err := r.ManagementCatalog(ctx, actor, automatic)
	if err != nil {
		return ManagementRoute{}, err
	}
	for _, item := range items {
		if item.Route == input.Route && item.Effect == effect {
			if _, err = managementHTTPRequest(ctx, input); err != nil {
				return ManagementRoute{}, err
			}
			return item, nil
		}
	}
	return ManagementRoute{}, authz.ErrForbidden
}

func managementHTTPRequest(ctx context.Context, in ManagementRequest) (*http.Request, error) {
	method, pattern, ok := strings.Cut(in.Route, " ")
	if !ok || len(in.Query) > 100 || len(in.PathParams) > 20 || len(in.Body) > 8<<20 || len(in.BodyBase64) > 12<<20 {
		return nil, ErrInvalidRoute
	}
	parts := strings.Split(pattern, "/")
	used := 0
	for i, p := range parts {
		if strings.HasPrefix(p, "{") && strings.HasSuffix(p, "}") {
			key := strings.TrimSuffix(strings.TrimPrefix(p, "{"), "}")
			value, exists := in.PathParams[key]
			if !exists || value == "" || len(value) > 1024 || strings.ContainsAny(value, "/\\\x00\r\n") || value == "." || value == ".." {
				return nil, ErrInvalidRoute
			}
			parts[i] = url.PathEscape(value)
			used++
		}
	}
	if used != len(in.PathParams) {
		return nil, ErrInvalidRoute
	}
	query := url.Values{}
	for k, v := range in.Query {
		if len(k) > 128 || len(v) > 16384 {
			return nil, ErrInvalidRoute
		}
		query.Set(k, v)
	}
	body := []byte(in.Body)
	media := "application/json"
	if in.BodyBase64 != "" {
		if len(body) != 0 && string(body) != "null" {
			return nil, ErrInvalidRoute
		}
		var err error
		body, err = base64.StdEncoding.DecodeString(in.BodyBase64)
		if err != nil || len(body) > 8<<20 {
			return nil, ErrInvalidRoute
		}
		media = in.ContentType
		if media == "" || len(media) > 128 || strings.ContainsAny(media, "\r\n") {
			return nil, ErrInvalidRoute
		}
	} else if in.ContentType != "" {
		return nil, ErrInvalidRoute
	}
	if string(body) == "null" {
		body = nil
	}
	if (method == http.MethodGet || method == http.MethodHead) && len(body) != 0 {
		return nil, ErrInvalidRoute
	}
	path := strings.Join(parts, "/")
	if q := query.Encode(); q != "" {
		path += "?" + q
	}
	req, err := http.NewRequestWithContext(ctx, method, path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Pattern = in.Route
	for k, v := range in.PathParams {
		req.SetPathValue(k, v)
	}
	if len(body) > 0 {
		req.Header.Set("Content-Type", media)
	}
	return req, nil
}

type managementResponse struct {
	header       http.Header
	status       int
	body         bytes.Buffer
	size         int64
	cancel       context.CancelFunc
	streamSample bool
}

func (w *managementResponse) Header() http.Header { return w.header }
func (w *managementResponse) WriteHeader(n int) {
	if w.status == 0 {
		w.status = n
	}
}
func (w *managementResponse) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	w.size += int64(len(b))
	if w.size <= 1<<20 {
		_, _ = w.body.Write(b)
	}
	return len(b), nil
}

func (w *managementResponse) Flush() {
	if w.size > 0 && strings.HasPrefix(w.header.Get("Content-Type"), "text/event-stream") && w.cancel != nil {
		w.streamSample = true
		w.cancel()
	}
}

// ExecuteManagement never performs an HTTP network request or follows redirects.
// It invokes exactly the registered domain handler with the caller's authority.
func (r *Runtime) ExecuteManagement(ctx context.Context, actor authz.Actor, automatic bool, input ManagementRequest, effect ManagementEffect, requestKey string) (json.RawMessage, error) {
	// Bounded reads also cover streaming endpoints; a tool receives a finite
	// sample instead of keeping a model turn open indefinitely.
	var cancel context.CancelFunc
	if effect == ManagementRead {
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}
	if automatic && effect == ManagementWrite {
		return nil, authz.ErrForbidden
	}
	if _, err := r.ResolveManagement(ctx, actor, automatic, input, effect); err != nil {
		return nil, err
	}
	ctx = r.managementContext(ctx, actor, automatic)
	if actor.Kind == authz.ActorUserSession {
		var release func()
		var err error
		ctx, release, err = r.admitUserLifecycle(ctx, actor, false)
		if err != nil {
			return nil, err
		}
		defer release()
	}
	req, err := managementHTTPRequest(ctx, input)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Idempotency-Key", requestKey)
	response := &managementResponse{header: make(http.Header), cancel: cancel}
	if actor.Kind == authz.ActorUserSession && !r.maintenanceAllowed(response, false) {
		return managementReceipt(response)
	}
	r.mu.Lock()
	route, ok := r.managementRoutes[input.Route]
	r.mu.Unlock()
	if !ok {
		return nil, ErrInvalidRoute
	}
	method, pattern, _ := strings.Cut(input.Route, " ")
	// Preserve approval provenance supplied by the plan, or establish the
	// automatic discovery provenance before a domain opens its transaction.
	if _, exists := managementlog.MetadataFromContext(ctx); !exists && effect == ManagementDiscovery {
		source := managementlog.SourceAIManual
		if automatic {
			source = managementlog.SourceAIAuto
		}
		meta, _ := managementlog.DescribeRoute(method, pattern)
		meta.Source, meta.ExecutorKind = source, "ai"
		meta.Actor = managementlog.Subject{ID: "ai", Name: "AI", Role: "ai"}
		subject, e := managementlog.SubjectTx(ctx, r.readDatabase(), actor.UserID)
		if e != nil {
			return nil, e
		}
		meta.Authorizer = subject
		meta.AuthorizationRef = "model_discovery"
		ctx = managementlog.WithOperation(ctx, meta)
		req = req.WithContext(ctx)
	}
	r.managementBoundary(method, pattern, route.handler).ServeHTTP(response, req)
	if ctx.Err() != nil {
		if response.status == 0 {
			return nil, ctx.Err()
		}
		if strings.HasPrefix(response.header.Get("Content-Type"), "text/event-stream") {
			response.streamSample = true
		}
	}
	return managementReceipt(response)
}

func managementReceipt(w *managementResponse) (json.RawMessage, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	result := map[string]any{"status_code": w.status}
	if w.streamSample {
		result["stream_sample"] = true
	}
	if w.size > 1<<20 {
		result["body_omitted"] = "Response exceeds 1 MiB. Narrow the query; do not repeat an already submitted mutation."
		result["bytes"] = w.size
	} else if json.Valid(w.body.Bytes()) {
		result["body"] = json.RawMessage(w.body.Bytes())
	} else if w.body.Len() > 0 {
		if strings.HasPrefix(w.header.Get("Content-Type"), "text/") {
			result["body_text"] = w.body.String()
		} else {
			result["body_base64"] = base64.StdEncoding.EncodeToString(w.body.Bytes())
			result["content_type"] = w.header.Get("Content-Type")
		}
	}
	return json.Marshal(result)
}
