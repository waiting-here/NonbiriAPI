package riskaudit

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type taskScanInput struct {
	RequestToken  string `json:"request_token"`
	From          int64  `json:"from,omitempty"`
	To            int64  `json:"to,omitempty"`
	LookbackHours int64  `json:"lookback_hours,omitempty"`
	Kind          string `json:"kind"`
	CallKind      string `json:"call_kind,omitempty"`
	Model         string `json:"model,omitempty"`
	Signal        string `json:"signal,omitempty"`
}

func taskScanView(scan ClientScan) any {
	return struct {
		ClientScan
		Kind              string `json:"kind"`
		CallKind          string `json:"call_kind"`
		Status            string `json:"status"`
		ScannedCandidates string `json:"scanned_candidates"`
	}{scan, scan.ScanKind, scan.Kind, scan.State, strconv.FormatInt(scan.Scanned, 10)}
}

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
		if action != "scan_results" && action != "scan_results_v2" || (key != "page" && key != "page_size") || len(values) != 1 {
			auditError(w, ErrInvalid)
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	var output any
	status := http.StatusOK
	switch action {
	case "scan_create_v2":
		var input taskScanInput
		err = decodeBody(w, r, &input)
		if err == nil {
			if input.Kind == "" {
				err = ErrInvalid
			} else {
				var scan ClientScan
				scan, err = repository.CreateScan(ctx, actor, ScanInput{RequestToken: input.RequestToken, From: input.From, To: input.To, LookbackHours: input.LookbackHours, Kind: input.CallKind, Model: input.Model, ScanKind: input.Kind, Signal: input.Signal})
				output = taskScanView(scan)
			}
		}
		status = http.StatusAccepted
	case "scan_recent_v2":
		var items []ClientScan
		items, err = repository.RecentTasks(ctx, actor)
		views := make([]any, 0, len(items))
		for _, item := range items {
			views = append(views, taskScanView(item))
		}
		output = struct {
			Items []any `json:"items"`
		}{views}
	case "scan_get_v2":
		var scan ClientScan
		scan, err = repository.GetScan(ctx, actor, r.PathValue("id"))
		output = taskScanView(scan)
	case "scan_cancel_v2":
		var input struct{}
		err = decodeBody(w, r, &input)
		if err == nil {
			var scan ClientScan
			scan, err = repository.CancelScan(ctx, actor, r.PathValue("id"))
			output = taskScanView(scan)
		}
	case "scan_results_v2":
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
		var results TaskResults
		results, err = repository.TaskResults(ctx, actor, r.PathValue("id"), page, int(size))
		output = struct {
			TaskResults
			Scan any `json:"scan"`
		}{results, taskScanView(results.Scan)}
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
