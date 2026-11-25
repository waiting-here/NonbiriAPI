package app

import (
	"context"
	"database/sql"
	"net/http"
	"testing"
	"time"
)

// Block a real numbered list after entry authorization, without delaying
// unrelated calls before the page reaches the log handler.
func delayBrowserLogRead(t *testing.T, f *imageBrowserFixture, r *http.Request) {
	body := &queuedLogReadBody{ctx: r.Context(), entered: make(chan struct{}), release: make(chan struct{})}
	r.Body = body
	go func() {
		select {
		case <-body.entered:
		case <-r.Context().Done():
			return
		}
		defer body.Close()
		reader := f.store.ReadDB()
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		held := make([]*sql.Conn, 0, reader.Stats().MaxOpenConnections)
		defer func() {
			for _, conn := range held {
				_ = conn.Close()
			}
		}()
		for range cap(held) {
			conn, err := reader.Conn(ctx)
			if err != nil {
				t.Errorf("acquire browser fixture reader: %v", err)
				return
			}
			held = append(held, conn)
		}
		_ = body.Close()
		select {
		case <-time.After(7500 * time.Millisecond):
		case <-t.Context().Done():
		}
	}()
}
