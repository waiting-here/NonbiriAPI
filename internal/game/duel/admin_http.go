package duel

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/game/host"
)

func (s *Service) RegisterAdminRoutes(admin host.AdminRegistrar) error {
	if admin == nil || s.adminAuthorizer == nil {
		return ErrInvariant
	}
	base := "/admin/api/games/" + s.rules.ID() + "/history"
	routes := []struct{ method, path string }{{"GET", base}, {"GET", base + "/{id}"}, {"GET", base + "/{id}/rounds"}, {"POST", base + "/export"}}
	for _, route := range routes {
		if err := admin.RegisterAdminRoute(route.method, route.path, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if route.method == "POST" {
				if r.URL.RawQuery != "" {
					writeError(w, ErrInvalidRequest)
					return
				}
				var raw struct {
					Dataset   string          `json:"dataset"`
					Selection json.RawMessage `json:"selection"`
					Cursor    json.RawMessage `json:"cursor"`
				}
				if !readJSON(w, r, &raw) {
					return
				}
				var cursor *string
				if len(raw.Cursor) == 0 || json.Unmarshal(raw.Cursor, &cursor) != nil {
					writeError(w, ErrInvalidRequest)
					return
				}
				var selection AdminSelection
				if Decode(raw.Selection, &selection) != nil {
					writeError(w, ErrInvalidRequest)
					return
				}
				var members map[string]json.RawMessage
				if json.Unmarshal(raw.Selection, &members) != nil {
					writeError(w, ErrInvalidRequest)
					return
				}
				for _, value := range members {
					if string(value) == "null" {
						writeError(w, ErrInvalidRequest)
						return
					}
				}
				value, err := s.AdminExport(r.Context(), AdminExportInput{Dataset: raw.Dataset, Selection: selection, Cursor: cursor})
				writeAdminValue(w, value, err)
				return
			}
			if !noBody(w, r) {
				return
			}
			kind := "list"
			if route.path == base+"/{id}" {
				kind = "detail"
			}
			if route.path == base+"/{id}/rounds" {
				kind = "rounds"
			}
			in, err := parseAdminPage(r.URL.RawQuery, kind)
			if err != nil {
				writeError(w, err)
				return
			}
			switch kind {
			case "list":
				value, err := s.AdminHistory(r.Context(), in)
				writeAdminValue(w, value, err)
			case "detail":
				value, err := s.AdminDetail(r.Context(), in.Dataset, r.PathValue("id"))
				writeAdminValue(w, value, err)
			case "rounds":
				value, err := s.AdminRounds(r.Context(), r.PathValue("id"), in)
				writeAdminValue(w, value, err)
			}
		})); err != nil {
			return err
		}
	}
	return nil
}
func writeAdminValue(w http.ResponseWriter, value any, err error) {
	if errors.Is(err, context.DeadlineExceeded) {
		err = ErrUnavailable
	}
	writeValue(w, value, err)
}
func parseAdminPage(raw, kind string) (AdminPageInput, error) {
	in := AdminPageInput{Dataset: "recent"}
	values, err := url.ParseQuery(raw)
	if err != nil {
		return in, ErrInvalidRequest
	}
	for key, values := range values {
		if len(values) != 1 || values[0] == "" {
			return in, ErrInvalidRequest
		}
		v := values[0]
		if key == "dataset" {
			in.Dataset = v
			continue
		}
		if kind == "detail" {
			return in, ErrInvalidRequest
		}
		if key == "cursor" {
			in.Cursor = v
			continue
		}
		if key == "limit" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 || strconv.Itoa(n) != v {
				return in, ErrInvalidRequest
			}
			in.Limit = n
			continue
		}
		if kind != "list" {
			return in, ErrInvalidRequest
		}
		switch key {
		case "mode":
			in.Selection.Mode = v
		case "outcome":
			in.Selection.Outcome = v
		case "rules_version":
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 || strconv.Itoa(n) != v {
				return in, ErrInvalidRequest
			}
			in.Selection.RulesVersion = &n
		case "from", "to":
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil || n < 0 || strconv.FormatInt(n, 10) != v {
				return in, ErrInvalidRequest
			}
			if key == "from" {
				in.Selection.From = &n
			} else {
				in.Selection.To = &n
			}
		default:
			return in, ErrInvalidRequest
		}
	}
	return in, nil
}
