package imageactivity

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDiscoveryScalarBoundAndDefaultPageSize(t *testing.T) {
	adapter := DiscoveryAdapter{ItemsPointer: "/data", IDPointer: "/id"}
	for _, tc := range []struct {
		id    string
		valid bool
	}{
		{strings.Repeat("a", 512), true},
		{strings.Repeat("😀", 512), true},
		{strings.Repeat("a", 513), false},
		{strings.Repeat("😀", 513), false},
	} {
		raw, err := json.Marshal(map[string]any{"data": []any{map[string]any{"id": tc.id}}})
		if err != nil {
			t.Fatal(err)
		}
		_, err = parseDiscovery(raw, adapter)
		if (err == nil) != tc.valid {
			t.Fatalf("discovery bound valid=%v err=%v", tc.valid, err)
		}
	}
	limit, cursor, err := listQuery(httptest.NewRequest("GET", "/tasks", nil))
	if err != nil || limit != 20 || cursor != "" {
		t.Fatalf("default page %d %q %v", limit, cursor, err)
	}
}

func TestRetentionPrunesIdleHTTPWindowWithoutChangingProtection(t *testing.T) {
	f := newFixture(t)
	f.configure(t)
	setting, err := f.service.GetUpstream(f.ctx(f.admin), f.admin)
	if err != nil {
		t.Fatal(err)
	}
	raw := make([]byte, 16)
	binary.BigEndian.PutUint64(raw[:8], uint64((f.now.Load()-60)*1000))
	binary.BigEndian.PutUint64(raw[8:], uint64(f.now.Load()*1000))
	f.tx(t, func(tx *sql.Tx) {
		_, err := tx.Exec("UPDATE image_upstream_control SET http_times=?,updated_at=?,protection_paused=1,protection_reason='receipt_unknown' WHERE id=?", raw, f.now.Load()-60, setting.Control.ID)
		if err != nil {
			t.Fatal(err)
		}
	})
	if _, err = f.service.Retain(context.Background(), f.now.Load(), 100, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	var kept []byte
	var paused bool
	var reason string
	if err = f.database.QueryRow("SELECT http_times,protection_paused,protection_reason FROM image_upstream_control WHERE id=?", setting.Control.ID).Scan(&kept, &paused, &reason); err != nil {
		t.Fatal(err)
	}
	if len(kept) != 8 || binary.BigEndian.Uint64(kept) != uint64(f.now.Load()*1000) || !paused || reason != "receipt_unknown" {
		t.Fatalf("prune changed live state len=%d paused=%v reason=%s", len(kept), paused, reason)
	}
	f.now.Add(60)
	if _, err = f.service.Retain(context.Background(), f.now.Load(), 100, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	if err = f.database.QueryRow("SELECT http_times FROM image_upstream_control WHERE id=?", setting.Control.ID).Scan(&kept); err != nil || len(kept) != 0 {
		t.Fatalf("idle window retained len=%d %v", len(kept), err)
	}
}

func TestWorkerReportsNonContextStepFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	background, stop := context.WithCancel(context.Background())
	defer stop()
	calls := 0
	s := &Service{config: Config{Now: func() time.Time { return time.Unix(-1, 0) }, ReportError: func(err error) {
		calls++
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("wrong worker error: %v", err)
		}
		cancel()
	}}, background: background, cancel: stop, wake: make(chan struct{}, 1)}
	if err := s.Run(ctx); err != nil || calls != 1 {
		t.Fatalf("worker report calls=%d err=%v", calls, err)
	}
	cancelled, cancelNow := context.WithCancel(context.Background())
	cancelNow()
	background2, stop2 := context.WithCancel(context.Background())
	defer stop2()
	s = &Service{config: Config{Now: func() time.Time { return time.Unix(-1, 0) }, ReportError: func(error) { t.Error("shutdown reported as operational error") }}, background: background2, cancel: stop2, wake: make(chan struct{}, 1)}
	if err := s.Run(cancelled); err != nil {
		t.Fatal(err)
	}
}

func TestShorterQueueDeadlineExpiresBehindProtectedHead(t *testing.T) {
	f := newFixture(t)
	f.configure(t)
	configureTimeout := func(seconds int) {
		t.Helper()
		current, err := f.service.GetUpstream(f.ctx(f.admin), f.admin)
		if err != nil {
			t.Fatal(err)
		}
		f.settings.ExpectedRevision = current.Revision
		f.settings.Secret = SecretInput{Mode: "keep"}
		f.settings.QueueTimeoutSeconds = seconds
		if _, err = f.service.PutUpstream(f.ctx(f.admin), f.admin, f.key(), f.settings); err != nil {
			t.Fatal(err)
		}
	}
	configureTimeout(180)
	head := f.submit(t, f.user, 1)
	configureTimeout(60)
	later := f.submit(t, f.other, 2)
	if _, err := f.database.Exec("UPDATE image_upstream_control SET protection_paused=1,protection_reason='receipt_unknown'"); err != nil {
		t.Fatal(err)
	}
	f.now.Add(60)
	if err := f.service.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	first, err := f.service.GetTask(f.ctx(f.user), f.user, head.ID)
	if err != nil || first.Status != "queued" {
		t.Fatalf("protected head: %+v %v", first, err)
	}
	second, err := f.service.GetTask(f.ctx(f.other), f.other, later.ID)
	if err != nil || second.Status != "cancelled" || second.ErrorCode == nil || *second.ErrorCode != "queue_timeout" || second.Refund != (Price{"4", "2"}) {
		t.Fatalf("later timeout refund: %+v %v", second, err)
	}
	if f.upstream.posts.Load() != 0 {
		t.Fatal("protected queue dispatched")
	}
	f.checkLedger(t)
}

func TestInvalidCatalogRetainsRejectedResponse(t *testing.T) {
	f := newFixture(t)
	f.configure(t)
	f.upstream.mode.Store(7)
	refresh, err := f.service.RefreshModels(f.ctx(f.admin), f.admin, f.key())
	if err != nil {
		t.Fatal(err)
	}
	f.wait(t, func() bool {
		state, err := f.service.GetRefresh(f.ctx(f.admin), f.admin, refresh.Value.Operation.ID)
		return err == nil && state.State == "failed"
	})
	var body, mime string
	if err := f.database.QueryRow("SELECT CAST(body AS TEXT),content_type FROM request_error_bodies WHERE operation_id=?", refresh.Value.Operation.ID).Scan(&body, &mime); err != nil {
		t.Fatal(err)
	}
	if body != `{"error":"synthetic malformed catalog"}` || mime != "application/json" {
		t.Fatalf("catalog diagnostic: %s %s", mime, body)
	}
}
