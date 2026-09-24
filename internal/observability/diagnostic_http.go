package observability

import (
	"encoding/json"
	"errors"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"io"
	"net/http"
	"net/url"
	"strconv"
)

type DiagnosticPrincipal struct{ UserID int64 }
type AuthorizedDiagnosticHandler func(http.ResponseWriter, *http.Request, DiagnosticPrincipal)
type DiagnosticAdminRegistrar interface {
	RegisterAdminRoute(string, string, AuthorizedDiagnosticHandler) error
}
type DiagnosticStewardRegistrar interface {
	RegisterUserRoute(string, string, AuthorizedDiagnosticHandler) error
}

func RegisterAdminIndependentDiagnosticRoutes(registrar DiagnosticAdminRegistrar, reader *DiagnosticReader) error {
	if registrar == nil || reader == nil {
		return ErrInvalid
	}
	for _, path := range []string{"/admin/api/diagnostics", "/admin/api/diagnostics/{id}"} {
		if err := registrar.RegisterAdminRoute(http.MethodGet, path, func(w http.ResponseWriter, r *http.Request, p DiagnosticPrincipal) {
			reader.serve(w, r, DiagnosticActor{UserID: p.UserID, Admin: true})
		}); err != nil {
			return err
		}
	}
	return nil
}
func RegisterStewardIndependentDiagnosticRoutes(registrar DiagnosticStewardRegistrar, reader *DiagnosticReader) error {
	if registrar == nil || reader == nil {
		return ErrInvalid
	}
	for _, path := range []string{"/api/steward/diagnostics", "/api/steward/diagnostics/{id}"} {
		if err := registrar.RegisterUserRoute(http.MethodGet, path, func(w http.ResponseWriter, r *http.Request, p DiagnosticPrincipal) {
			reader.serve(w, r, DiagnosticActor{UserID: p.UserID})
		}); err != nil {
			return err
		}
	}
	return nil
}
func parseIndependentDiagnosticQuery(r *http.Request) (DiagnosticFilter, error) {
	var out DiagnosticFilter
	if r.URL == nil || len(r.URL.RawQuery) > 1024 {
		return out, ErrInvalid
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return out, ErrInvalid
	}
	for key, values := range q {
		if len(values) != 1 || values[0] == "" {
			return out, ErrInvalid
		}
		value := values[0]
		switch key {
		case "kind":
			out.Kind = value
		case "subject_id":
			out.SubjectID = value
		case "before":
			out.Before = value
		case "user_id", "from", "to", "page_size":
			n, e := diagnosticNumber(value, key == "from")
			if e != nil {
				return out, e
			}
			switch key {
			case "user_id":
				out.UserID = n
			case "from":
				out.From = n
			case "to":
				out.To = n
			case "page_size":
				if n > 20 {
					return out, ErrInvalid
				}
				out.Limit = int(n)
			}
		default:
			return out, ErrInvalid
		}
	}
	return out, nil
}
func writeIndependentDiagnosticError(w http.ResponseWriter, err error) {
	code, message := httperr.CodeServiceUnavailable, "diagnostics unavailable"
	switch {
	case errors.Is(err, ErrInvalid):
		code, message = httperr.CodeInvalidRequest, "invalid diagnostic query"
	case errors.Is(err, ErrDiagnosticForbidden):
		code, message = httperr.CodeForbidden, "diagnostic access forbidden"
	case errors.Is(err, ErrDiagnosticNotFound):
		code, message = httperr.CodeNotFound, "diagnostic not found"
	}
	httperr.WriteError(w, httperr.New(code, message))
}
func (r *DiagnosticReader) serve(w http.ResponseWriter, request *http.Request, actor DiagnosticActor) {
	if request.Body != nil {
		b, err := io.ReadAll(io.LimitReader(request.Body, 1))
		if err != nil || len(b) != 0 {
			writeIndependentDiagnosticError(w, ErrInvalid)
			return
		}
	}
	var value any
	var err error
	if id := request.PathValue("id"); id != "" {
		if request.URL == nil || request.URL.RawQuery != "" {
			writeIndependentDiagnosticError(w, ErrInvalid)
			return
		}
		value, err = r.Detail(request.Context(), actor, id)
	} else {
		var filter DiagnosticFilter
		filter, err = parseIndependentDiagnosticQuery(request)
		if err == nil {
			value, err = r.List(request.Context(), actor, filter)
		}
	}
	if err != nil {
		writeIndependentDiagnosticError(w, err)
		return
	}
	body, err := json.Marshal(value)
	if err != nil || len(body) > 8<<20 {
		writeIndependentDiagnosticError(w, ErrUnavailable)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
