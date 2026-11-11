package economyaudit

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strconv"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func TestOperationsHistoricalFallbackPreservesSequenceAndCursor(t *testing.T) {
	f := newAuditFixture(t)
	old := auditNow - 3*86400
	f.fund(t, 120, old)
	f.fund(t, 600, auditNow-1000)
	// A later sequence can still belong to an earlier time interval.
	f.fund(t, 1, old+10)
	filter := auditFilter(ledger.General)
	filter.From, filter.To = old, old+200
	first, err := f.service.Operations(f.ctx, f.admin, filter)
	if err != nil || len(first.Data) != 100 || first.NextCursor == nil || first.AnchorSeq != "721" {
		t.Fatalf("historical first page: %+v, %v", first, err)
	}
	for i, o := range first.Data {
		want := int64(121 - i)
		if i == 0 {
			want = 721
		}
		if o.LedgerSeq != strconv.FormatInt(want, 10) || len(o.Entries) != 2 {
			t.Fatalf("operation %d: %+v, want sequence %d and both entries", i, o, want)
		}
	}
	f.fund(t, 1, old+20)
	filter.Cursor = *first.NextCursor
	second, err := f.service.Operations(f.ctx, f.admin, filter)
	if err != nil || len(second.Data) != 21 || second.NextCursor != nil || second.AnchorSeq != "721" || second.Metadata.LedgerSeq != "722" {
		t.Fatalf("historical anchored second page: %+v, %v", second, err)
	}
	for i, o := range second.Data {
		if o.LedgerSeq != strconv.Itoa(21-i) {
			t.Fatalf("second page %d: %+v", i, o)
		}
	}
}

func TestSeriesBatchedBucketsMatchExactRangeMeasures(t *testing.T) {
	f := newAuditFixture(t)
	start := bucketStart(auditNow-10*86400, 330, 86400)
	for _, at := range []int64{start + 5, start + 100, start + 3599, start + 3600, start + 7201, start + 86400, start + 3*86400 + 123, start + 5*86400} {
		f.fund(t, 1, at)
	}
	f.project(t)
	for _, bucket := range []string{"hour", "day"} {
		for _, asset := range []ledger.Asset{ledger.General, ledger.Game} {
			for _, kind := range []string{"", "admin_user_adjustment", "welfare_claim"} {
				for _, bounds := range [][2]int64{{start + 10, start + 3700}, {start, start + 5*86400}, {start + 10, start + 5*86400 + 100}, {start + 1, start + 9}} {
					filter := Filter{Asset: asset, From: bounds[0], To: bounds[1], Bucket: bucket, Kind: kind}
					if kind == "admin_user_adjustment" {
						filter.Channel = "admin"
					}
					got, err := f.service.Series(f.ctx, f.admin, filter)
					if err != nil {
						t.Fatal(err)
					}
					var want []Point
					err = f.service.read(f.ctx, f.admin, filter, func(ctx context.Context, tx *sql.Tx, c checkpoint, _ int64) error {
						step := int64(3600)
						if bucket == "day" {
							step = 86400
						}
						for at := bucketStart(filter.From, c.offset, step); at < filter.To; at += step {
							part := filter
							part.From, part.To = max(filter.From, at), min(filter.To, at+step)
							values, err := rangeMeasures(ctx, tx, c, part)
							if err != nil {
								return err
							}
							m, err := sumDimensions(values)
							if err != nil {
								return err
							}
							want = append(want, Point{part.From, part.To, m.wire()})
						}
						return nil
					})
					if err != nil || !reflect.DeepEqual(got.Data, want) {
						t.Fatalf("%s/%s/%s/%v: got %+v, want %+v, err %v", bucket, asset, kind, bounds, got.Data, want, err)
					}
				}
			}
		}
	}
}

func TestOperationsCancellationAfterAuthorizationReleasesReader(t *testing.T) {
	f := newAuditFixture(t)
	f.fund(t, 1, auditNow-1)
	ctx, cancel := context.WithCancel(f.ctx)
	f.service.auth = auditAuthHook{FinalAuthorizer: f.service.auth, after: cancel}
	_, err := f.service.Operations(ctx, f.admin, auditFilter(ledger.General))
	if !errors.Is(err, context.Canceled) {
		t.Fatal("read ignored cancellation after authorization", err)
	}
	f.service.auth = f.service.auth.(auditAuthHook).FinalAuthorizer
	if got, err := f.service.Operations(f.ctx, f.admin, auditFilter(ledger.General)); err != nil || len(got.Data) != 1 {
		t.Fatal("reader not reusable after cancellation", got, err)
	}
}

func TestOperationsProjectionKeepsAuthorizedSnapshotDuringWrites(t *testing.T) {
	f := newAuditFixture(t)
	f.fund(t, 300, auditNow-1000)
	f.service.auth = auditAuthHook{FinalAuthorizer: f.service.auth, after: func() {
		f.fund(t, 1, auditNow-1)
	}}
	got, err := f.service.Operations(f.ctx, f.admin, auditFilter(ledger.General))
	if err != nil || got.AnchorSeq != "300" || got.Metadata.LedgerSeq != "300" || len(got.Data) != 100 || got.Data[0].LedgerSeq != "300" {
		t.Fatal("projection changed after authorization snapshot", got, err)
	}
}
