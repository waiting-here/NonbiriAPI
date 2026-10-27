package managementlog

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type AdminRegistrar interface {
	RegisterAdminRoute(string, string, http.Handler) error
}

func (s *Service) RegisterRoutes(registrar AdminRegistrar, actorID func(*http.Request) int64) error {
	const base = "/admin/api/management-operations"
	routes := []struct {
		method, path string
		handle       func(http.ResponseWriter, *http.Request, int64)
	}{
		{"GET", base, s.listHTTP}, {"GET", base + "/{id}", s.detailHTTP}, {"GET", base + "/{id}/items", s.itemsHTTP},
		{"POST", base + "/cleanup/prepare", s.prepareHTTP}, {"POST", base + "/cleanup", s.cleanupHTTP},
		{"GET", base + "/cleanup/{id}/status", s.cleanupStatusHTTP}, {"POST", base + "/cleanup/{id}/resume", s.resumeHTTP},
	}
	for _, route := range routes {
		if err := registrar.RegisterAdminRoute(route.method, route.path, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			route.handle(w, r, actorID(r))
		})); err != nil {
			return err
		}
	}
	return nil
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, err error) {
	status := 500
	code := "internal_error"
	switch {
	case errors.Is(err, ErrForbidden):
		status = 403
		code = "forbidden"
	case errors.Is(err, ErrInvalid):
		status = 400
		code = "invalid_request"
	case errors.Is(err, ErrNotFound):
		status = 404
		code = "not_found"
	case errors.Is(err, ErrCleaning):
		status = 409
		code = "cleanup_in_progress"
	case errors.Is(err, ErrBudget):
		status = 422
		code = "query_budget_exceeded"
	}
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": code}})
}
func number(value string) (int64, error) {
	if value == "" {
		return 0, nil
	}
	v, e := strconv.ParseInt(value, 10, 64)
	if e != nil || v < 0 || strconv.FormatInt(v, 10) != value {
		return 0, ErrInvalid
	}
	return v, nil
}
func parseHTTPQuery(r *http.Request) (Query, error) {
	var q Query
	if len(r.URL.RawQuery) > 8192 {
		return q, ErrInvalid
	}
	values, e := url.ParseQuery(r.URL.RawQuery)
	if e != nil {
		return q, ErrInvalid
	}
	known := " time_from time_to source actor_id authorizer_id domain action outcome event_id target_kind target_id group_key search view page page_size snapshot_max_id "
	for key, items := range values {
		if len(items) != 1 || !strings.Contains(known, " "+key+" ") {
			return q, ErrInvalid
		}
	}
	q.Source = Source(values.Get("source"))
	q.ActorID = values.Get("actor_id")
	q.AuthorizerID = values.Get("authorizer_id")
	q.Domain = values.Get("domain")
	q.Action = values.Get("action")
	q.Outcome = Outcome(values.Get("outcome"))
	q.EventID = values.Get("event_id")
	q.TargetKind = values.Get("target_kind")
	q.TargetID = values.Get("target_id")
	q.GroupKey = values.Get("group_key")
	q.Search = values.Get("search")
	q.View = values.Get("view")
	if q.TimeFrom, e = number(values.Get("time_from")); e != nil {
		return q, e
	}
	if q.TimeTo, e = number(values.Get("time_to")); e != nil {
		return q, e
	}
	if q.SnapshotMaxID, e = number(values.Get("snapshot_max_id")); e != nil {
		return q, e
	}
	page, e := number(values.Get("page"))
	if e != nil || page > 10000000 {
		return q, ErrInvalid
	}
	size, e := number(values.Get("page_size"))
	if e != nil || size > 100 {
		return q, ErrInvalid
	}
	q.Page = int(page)
	q.PageSize = int(size)
	return q, nil
}
func decodeHTTP(w http.ResponseWriter, r *http.Request, value any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384))
	decoder.DisallowUnknownFields()
	if decoder.Decode(value) != nil {
		return ErrInvalid
	}
	if decoder.Decode(new(any)) != io.EOF {
		return ErrInvalid
	}
	return nil
}
func (s *Service) listHTTP(w http.ResponseWriter, r *http.Request, actor int64) {
	q, e := parseHTTPQuery(r)
	if e != nil {
		writeError(w, e)
		return
	}
	value, e := s.Query(r.Context(), actor, q)
	if e != nil {
		writeError(w, e)
		return
	}
	writeJSON(w, 200, value)
}
func (s *Service) detailHTTP(w http.ResponseWriter, r *http.Request, actor int64) {
	id, e := number(r.PathValue("id"))
	if e != nil {
		writeError(w, e)
		return
	}
	value, e := s.Detail(r.Context(), actor, id)
	if e != nil {
		writeError(w, e)
		return
	}
	writeJSON(w, 200, value)
}
func (s *Service) itemsHTTP(w http.ResponseWriter, r *http.Request, actor int64) {
	id, e := number(r.PathValue("id"))
	if e != nil {
		writeError(w, e)
		return
	}
	q, e := parseHTTPQuery(r)
	if e != nil {
		writeError(w, e)
		return
	}
	value, e := s.Items(r.Context(), actor, id, q.Page, q.PageSize)
	if e != nil {
		writeError(w, e)
		return
	}
	writeJSON(w, 200, value)
}
func (s *Service) prepareHTTP(w http.ResponseWriter, r *http.Request, actor int64) {
	var in CleanupRequest
	if e := decodeHTTP(w, r, &in); e != nil {
		writeError(w, e)
		return
	}
	value, e := s.PrepareCleanup(r.Context(), actor, in)
	if e != nil {
		writeError(w, e)
		return
	}
	writeJSON(w, 200, value)
}
func (s *Service) cleanupHTTP(w http.ResponseWriter, r *http.Request, actor int64) {
	var in struct {
		RequestID string          `json:"request_id"`
		Prepared  PreparedCleanup `json:"prepared"`
	}
	if e := decodeHTTP(w, r, &in); e != nil {
		writeError(w, e)
		return
	}
	tx, e := s.DB.BeginTx(r.Context(), nil)
	if e != nil {
		writeError(w, e)
		return
	}
	defer tx.Rollback()
	job, e := s.ApplyCleanupTx(r.Context(), tx, actor, in.RequestID, in.Prepared)
	if e == nil {
		e = tx.Commit()
	}
	if e != nil {
		writeError(w, e)
		return
	}
	writeJSON(w, 202, job)
}
func (s *Service) cleanupStatusHTTP(w http.ResponseWriter, r *http.Request, actor int64) {
	id, e := number(r.PathValue("id"))
	if e != nil {
		writeError(w, e)
		return
	}
	job, e := s.CleanupStatus(r.Context(), actor, id)
	if e != nil {
		writeError(w, e)
		return
	}
	writeJSON(w, 200, job)
}
func (s *Service) resumeHTTP(w http.ResponseWriter, r *http.Request, actor int64) {
	id, e := number(r.PathValue("id"))
	if e == nil {
		e = s.ResumeCleanup(r.Context(), actor, id)
	}
	if e != nil {
		writeError(w, e)
		return
	}
	writeJSON(w, 200, map[string]string{"status": "pending"})
}
