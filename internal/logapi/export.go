package logapi

// Administrator log export: unpaginated CSV/JSON selections over the same
// frozen filter set as /admin/api/logs, with hard fail-closed bounds.
//
// Bounds and failure semantics:
//   - the row selection is capped at db.MaxLogExportRows; a larger selection is
//     refused (payload_too_large) instead of silently truncated;
//   - the rendered payload is capped at MaxLogExportBytes; crossing the bound
//     mid-render aborts the response before any body byte is written.
//
// CSV formula-injection defense: every cell — regardless of source — is
// sanitized before rendering. A cell whose first rune is one of = + - @, TAB,
// CR, LF, a BOM, or any Unicode whitespace is prefixed with a single quote so
// a spreadsheet application never interprets it as a formula or control
// sequence; encoding/csv then quotes and escapes the remainder. Upstream- or
// user-derived text (model names, diagnostics, base URLs) can therefore never
// execute as a formula in a downstream spreadsheet.

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

// MaxLogExportBytes bounds one rendered export payload (final byte-level
// backstop behind db.MaxLogExportRows).
const MaxLogExportBytes = 16 << 20

// adminLogExportColumns is the frozen CSV column order.
var adminLogExportColumns = []string{
	"id", "started_at", "completed_at", "user_id", "route_kind",
	"endpoint_base_url", "endpoint_key_id", "upstream_model_id",
	"status_code", "duration_ms",
	"uncached_input_tokens", "cache_write_input_tokens", "cache_read_input_tokens", "output_tokens",
	"prompt_tokens", "completion_tokens", "total_tokens",
	"usage_unknown", "error_code", "error_source", "error_diag", "attempt_id",
}

func adminLogExportRow(l db.AdminRequestLog) []string {
	return []string{
		strconv.FormatInt(l.ID, 10),
		strconv.FormatInt(l.StartedAt.Unix(), 10),
		strconv.FormatInt(l.CompletedAt.Unix(), 10),
		strconv.FormatInt(l.UserID, 10),
		l.RouteKind,
		l.EndpointBaseURL,
		strconv.FormatInt(l.EndpointKeyID, 10),
		l.UpstreamModelID,
		strconv.Itoa(l.StatusCode),
		strconv.FormatInt(l.DurationMs, 10),
		strconv.FormatInt(l.UncachedInputTokens, 10),
		strconv.FormatInt(l.CacheWriteInputTokens, 10),
		strconv.FormatInt(l.CacheReadInputTokens, 10),
		strconv.FormatInt(l.OutputTokens, 10),
		strconv.FormatInt(l.PromptTokens, 10),
		strconv.FormatInt(l.CompletionTokens, 10),
		strconv.FormatInt(l.TotalTokens, 10),
		strconv.FormatBool(l.UsageUnknown),
		l.ErrorCode,
		l.ErrorSource,
		l.ErrorDiag,
		l.AttemptID,
	}
}

// sanitizeCSVCell neutralizes spreadsheet formula injection for one cell. A
// leading rune from the dangerous set (= + - @ TAB CR LF BOM) or any Unicode
// whitespace gets a single-quote prefix, which spreadsheet applications treat
// as "literal text". The check decodes runes, not bytes, so multi-byte
// whitespace (U+00A0, U+2000.., U+FEFF) cannot smuggle a formula past it.
func sanitizeCSVCell(s string) string {
	if s == "" {
		return s
	}
	first, _ := utf8.DecodeRuneInString(s)
	switch first {
	case '=', '+', '-', '@', '\t', '\r', '\n', 0xFEFF:
		return "'" + s
	}
	if unicode.IsSpace(first) {
		return "'" + s
	}
	return s
}

// renderAdminLogCSV renders the full export payload. It returns an error when
// the payload would exceed MaxLogExportBytes; no partial output escapes.
func renderAdminLogCSV(logs []db.AdminRequestLog) ([]byte, bool, error) {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	if err := w.Write(applyCSVSanitize(adminLogExportColumns)); err != nil {
		return nil, false, fmt.Errorf("render log csv header: %w", err)
	}
	for i := range logs {
		if err := w.Write(applyCSVSanitize(adminLogExportRow(logs[i]))); err != nil {
			return nil, false, fmt.Errorf("render log csv row: %w", err)
		}
		if buf.Len() > MaxLogExportBytes {
			return nil, true, nil
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, false, fmt.Errorf("render log csv: %w", err)
	}
	if buf.Len() > MaxLogExportBytes {
		return nil, true, nil
	}
	payload := buf.Bytes()
	// Copy out of the buffer so the returned slice owns its storage.
	out := make([]byte, len(payload))
	copy(out, payload)
	return out, false, nil
}

func applyCSVSanitize(row []string) []string {
	out := make([]string, len(row))
	for i, cell := range row {
		out[i] = sanitizeCSVCell(cell)
	}
	return out
}

// renderAdminLogJSON renders the JSON export payload (same frozen row shape as
// the list endpoint). The second return value reports an over-bound payload.
func renderAdminLogJSON(logs []db.AdminRequestLog) ([]byte, bool, error) {
	body := struct {
		Data []logRowResp `json:"data"`
	}{Data: make([]logRowResp, 0, len(logs))}
	for _, l := range logs {
		body.Data = append(body.Data, adminLogRowResponse(l))
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, false, fmt.Errorf("render log json export: %w", err)
	}
	if len(payload) > MaxLogExportBytes {
		return nil, true, nil
	}
	return payload, false, nil
}

// adminExport handles GET /admin/api/logs/export.csv and
// /admin/api/logs/export.json: an unpaginated, bounded, metadata-only export
// of the administrator-filtered log rows. The whole selection is materialized
// and bounds-checked before any response byte is written, so an over-bound
// export fails closed as payload_too_large with no truncated file.
func (h *Handler) adminExport(w http.ResponseWriter, r *http.Request, format string) {
	if h.store == nil {
		writeErr(w, httperr.New(httperr.CodeServiceUnavailable, "log service unavailable"))
		return
	}
	if _, ok := h.requireAdminSession(w, r); !ok {
		return
	}
	query, derr := parseLogExportQuery(r)
	if derr.Code != "" {
		writeErr(w, derr)
		return
	}
	logs, err := h.store.ExportAdminRequestLogs(r.Context(), query)
	if err != nil {
		if errors.Is(err, db.ErrLogExportTooLarge) {
			writeErr(w, httperr.New(httperr.CodePayloadTooLarge, "log export too large"))
			return
		}
		writeRepoErr(w, err)
		return
	}
	var payload []byte
	var tooLarge bool
	contentType, ext := "text/csv; charset=utf-8", "csv"
	switch format {
	case "csv":
		payload, tooLarge, err = renderAdminLogCSV(logs)
	case "json":
		payload, tooLarge, err = renderAdminLogJSON(logs)
		contentType, ext = "application/json; charset=utf-8", "json"
	default:
		writeErr(w, httperr.New(httperr.CodeInternal, "internal error"))
		return
	}
	if err != nil {
		writeRepoErr(w, err)
		return
	}
	if tooLarge {
		writeErr(w, httperr.New(httperr.CodePayloadTooLarge, "log export too large"))
		return
	}
	filename := fmt.Sprintf("nonbiri-admin-logs-%d.%s", time.Now().Unix(), ext)
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(payload)
}

func (h *Handler) adminExportCSV(w http.ResponseWriter, r *http.Request) {
	h.adminExport(w, r, "csv")
}

func (h *Handler) adminExportJSON(w http.ResponseWriter, r *http.Request) {
	h.adminExport(w, r, "json")
}
