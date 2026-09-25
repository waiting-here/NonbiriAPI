package riskaudit

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

func serveScans(repository *Repository, action string, actor Actor, w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if len(r.URL.RawQuery) > 512 || (r.Method == http.MethodGet && r.ContentLength != 0) {
		auditError(w, ErrInvalid)
		return
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		auditError(w, ErrInvalid)
		return
	}
	for key, values := range q {
		if action != "scan_results" || (key != "page" && key != "page_size") || len(values) != 1 {
			auditError(w, ErrInvalid)
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	var output any
	status := http.StatusOK
	switch action {
	case "scan_create":
		var input ScanInput
		err = decodeBody(w, r, &input)
		if err == nil {
			output, err = repository.CreateScan(ctx, actor, input)
		}
		status = http.StatusAccepted
	case "scan_recent":
		var items []ClientScan
		items, err = repository.RecentScans(ctx, actor)
		output = struct {
			Items []ClientScan `json:"items"`
		}{items}
	case "scan_get":
		output, err = repository.GetScan(ctx, actor, r.PathValue("id"))
	case "scan_cancel":
		var input struct{}
		err = decodeBody(w, r, &input)
		if err == nil {
			output, err = repository.CancelScan(ctx, actor, r.PathValue("id"))
		}
	case "scan_results":
		page, e := intQuery(q, "page", 1)
		if e != nil {
			err = e
			break
		}
		size, e := intQuery(q, "page_size", 20)
		if e != nil || size > 100 {
			err = ErrInvalid
			break
		}
		output, err = repository.ScanResults(ctx, actor, r.PathValue("id"), page, int(size))
	default:
		err = ErrInvalid
	}
	if err != nil {
		auditError(w, err)
		return
	}
	auditJSON(w, status, output)
}
