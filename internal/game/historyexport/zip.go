// Package historyexport writes bounded pages into a single streaming download.
package historyexport

import (
	"archive/zip"
	"compress/flate"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"
)

type Summary struct {
	Format          string `json:"format"`
	RecordsFormat   string `json:"records_format"`
	Dataset         string `json:"dataset"`
	Records         int    `json:"records"`
	ExpiredSkipped  int    `json:"expired_skipped"`
	IncludesUndated bool   `json:"includes_undated"`
	Complete        bool   `json:"complete"`
}

type responseWriter struct {
	context.Context
	http.ResponseWriter
}

func (w responseWriter) Write(p []byte) (int, error) {
	if err := w.Err(); err != nil {
		return 0, err
	}
	if err := http.NewResponseController(w.ResponseWriter).SetWriteDeadline(time.Now().Add(30 * time.Second)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return 0, err
	}
	return w.ResponseWriter.Write(p)
}

// Write is called after the first pages have passed authorization and validation.
// Failed streams have no ZIP central directory and must be aborted by the handler.
func Write(ctx context.Context, w http.ResponseWriter, filename string, summary Summary, emit func(io.Writer, *Summary) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	defer http.NewResponseController(w).SetWriteDeadline(time.Time{})
	archive := zip.NewWriter(responseWriter{ctx, w})
	archive.RegisterCompressor(zip.Deflate, func(w io.Writer) (io.WriteCloser, error) { return flate.NewWriter(w, flate.BestSpeed) })
	records, err := archive.Create("records.ndjson")
	if err != nil {
		return err
	}
	if err := emit(records, &summary); err != nil {
		return err
	}
	manifest, err := archive.Create("manifest.json")
	if err != nil {
		return err
	}
	summary.Format, summary.Complete = "game-history-zip/v1", true
	if err := json.NewEncoder(manifest).Encode(summary); err != nil {
		return err
	}
	return archive.Close()
}
