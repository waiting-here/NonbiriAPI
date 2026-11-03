package claim

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/headerfacts"
	"github.com/waiting-here/NonbiriAPI/internal/observability"
)

func sourceLogContext(agent string) context.Context {
	r := httptest.NewRequest("POST", "https://example.invalid/v1/chat/completions", nil)
	r.Header.Set("User-Agent", agent)
	return observability.WithSource(headerfacts.WithRequest(context.Background(), r),
		observability.Source{EffectiveIP: "192.0.2.1", IPQuality: "direct_peer", UserAgent: agent})
}

func TestRequestLogSourceBackfillAndReplay(t *testing.T) {
	for _, initial := range []string{"missing_source", "unclassified", "classified", "missing_log", "retired_unclassified", "retired_missing_source"} {
		t.Run(initial, func(t *testing.T) {
			f := newClaimFixture(t)
			user := f.seedUser("source-fast-path", false)
			request := f.acceptSelf(user, 1)
			repository, err := observability.NewRepository(f.db)
			if err != nil {
				t.Fatal(err)
			}
			f.service.observations = repository
			oldCtx, newCtx := sourceLogContext("original"), sourceLogContext("retry")
			if initial == "missing_log" {
				if _, err = f.db.Exec("DELETE FROM request_logs WHERE logical_request_id=?", request.ID); err != nil {
					t.Fatal(err)
				}
			}
			if initial == "unclassified" || initial == "classified" || initial == "retired_unclassified" {
				kind := "unclassified"
				if initial == "classified" {
					kind = "self"
				}
				tx, err := f.db.BeginTx(oldCtx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				if err = repository.RecordSourceTx(oldCtx, tx, request.ID, user, kind, f.clock.Load()); err != nil {
					t.Fatal(err)
				}
				if err = tx.Commit(); err != nil {
					t.Fatal(err)
				}
			}
			ensure := func(ctx context.Context) error {
				tx, err := f.db.BeginTx(ctx, nil)
				if err != nil {
					return err
				}
				defer tx.Rollback()
				if err = f.service.ensureRequestLogTx(ctx, tx, request.ID); err != nil {
					return err
				}
				return tx.Commit()
			}
			if initial == "retired_unclassified" || initial == "retired_missing_source" {
				if _, err = f.db.Exec("UPDATE logical_requests SET user_id=NULL WHERE id=?", request.ID); err != nil {
					t.Fatal(err)
				}
				if _, err = f.db.Exec("DELETE FROM users WHERE id=?", user); err != nil {
					t.Fatal(err)
				}
			}
			if err = ensure(newCtx); err != nil {
				t.Fatal(err)
			}
			var beforeSource, kind string
			var beforeHeaders []byte
			var origin int64
			if err = f.db.QueryRow(`SELECT l.origin_user_id,s.kind,s.source_json,h.encoded
FROM request_logs l JOIN request_source_facts s ON s.request_log_id=l.id
JOIN request_header_facts h ON h.request_log_id=l.id AND h.attempt_seq=0
WHERE l.logical_request_id=?`, request.ID).Scan(&origin, &kind, &beforeSource, &beforeHeaders); err != nil {
				t.Fatal(err)
			}
			source, err := observability.ParseSource([]byte(beforeSource))
			wantAgent := "retry"
			if initial == "unclassified" || initial == "classified" || initial == "retired_unclassified" {
				wantAgent = "original"
			}
			if err != nil || source.UserAgent != wantAgent || origin != user || kind != "self" {
				t.Fatalf("source projection kind=%s origin=%d agent=%s err=%v", kind, origin, source.UserAgent, err)
			}
			// A late retry may still use the retained origin, but cannot restore
			// the live identity or overwrite the original source and headers.
			if _, err = f.db.Exec("UPDATE logical_requests SET user_id=NULL WHERE id=?", request.ID); err != nil {
				t.Fatal(err)
			}
			if _, err = f.db.Exec("DELETE FROM users WHERE id=?", user); err != nil {
				t.Fatal(err)
			}
			if err = ensure(sourceLogContext("late")); err != nil {
				t.Fatal(err)
			}
			var afterSource string
			var afterHeaders []byte
			var live sql.NullInt64
			if err = f.db.QueryRow(`SELECT l.user_id,s.source_json,h.encoded
FROM request_logs l JOIN request_source_facts s ON s.request_log_id=l.id
JOIN request_header_facts h ON h.request_log_id=l.id AND h.attempt_seq=0
WHERE l.logical_request_id=?`, request.ID).Scan(&live, &afterSource, &afterHeaders); err != nil {
				t.Fatal(err)
			}
			if live.Valid || afterSource != beforeSource || !bytes.Equal(afterHeaders, beforeHeaders) {
				t.Fatal("retry changed retained identity or source facts")
			}
			if _, err = f.db.Exec("DELETE FROM request_logs WHERE logical_request_id=?", request.ID); err != nil {
				t.Fatal(err)
			}
			if err = ensure(newCtx); !errors.Is(err, ErrNotFound) {
				t.Fatalf("retired root recreated: %v", err)
			}
		})
	}
}

func TestRequestLogSourceCancellationReturnsConnection(t *testing.T) {
	f := newClaimFixture(t)
	user := f.seedUser("source-cancel", false)
	request := f.acceptSelf(user, 1)
	repository, err := observability.NewRepository(f.db)
	if err != nil {
		t.Fatal(err)
	}
	f.service.observations = repository
	ctx, cancel := context.WithCancel(sourceLogContext("cancel"))
	tx, err := f.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if err = f.service.ensureRequestLogTx(ctx, tx, request.ID); !errors.Is(err, context.Canceled) && !errors.Is(err, sql.ErrTxDone) {
		t.Fatalf("canceled source write: %v", err)
	}
	_ = tx.Rollback()
	reuse, stop := context.WithTimeout(sourceLogContext("after-cancel"), time.Second)
	defer stop()
	tx, err = f.db.BeginTx(reuse, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = f.service.ensureRequestLogTx(reuse, tx, request.ID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func BenchmarkRequestLogReuse(b *testing.B) {
	for _, size := range []int{16, 2048} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			f := newClaimFixture(b)
			user := f.seedUser("source-benchmark", false)
			request := f.acceptSelf(user, 1)
			repository, err := observability.NewRepository(f.db)
			if err != nil {
				b.Fatal(err)
			}
			f.service.observations = repository
			ctx := sourceLogContext(strings.Repeat("a", size))
			run := func() {
				tx, err := f.db.BeginTx(ctx, nil)
				if err != nil {
					b.Fatal(err)
				}
				if err = f.service.ensureRequestLogTx(ctx, tx, request.ID); err != nil {
					_ = tx.Rollback()
					b.Fatal(err)
				}
				if err = tx.Commit(); err != nil {
					b.Fatal(err)
				}
			}
			run()
			b.ReportAllocs()
			for b.Loop() {
				run()
			}
		})
	}
}

func TestRequestLogClassifiedRootAvoidsNoopWrites(t *testing.T) {
	f := newClaimFixture(t)
	user := f.seedUser("source-noop", false)
	request := f.acceptSelf(user, 1)
	repository, err := observability.NewRepository(f.db)
	if err != nil {
		t.Fatal(err)
	}
	f.service.observations = repository
	ctx := sourceLogContext("original")
	tx, err := f.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = f.service.ensureRequestLogTx(ctx, tx, request.ID); err != nil {
		t.Fatal(err)
	}
	// Even a conflicting no-op INSERT runs a BEFORE INSERT trigger.
	if _, err = tx.Exec(`CREATE TEMP TRIGGER reject_log_reinsert BEFORE INSERT ON request_logs
BEGIN SELECT RAISE(ABORT,'unexpected request log write'); END;
CREATE TEMP TRIGGER reject_source_reinsert BEFORE INSERT ON request_source_facts
BEGIN SELECT RAISE(ABORT,'unexpected source write'); END;`); err != nil {
		t.Fatal(err)
	}
	if err = f.service.ensureRequestLogTx(sourceLogContext("retry"), tx, request.ID); err != nil {
		t.Fatal(err)
	}
}
