package logapi

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestEmbeddingLogProjectionsAndOwnership(t *testing.T) {
	f := newLogFixture(t)
	ctx := context.Background()
	f.mustExec(`UPDATE request_logs SET route_kind=? WHERE logical_request_id=?`, RouteOpenAIEmbeddings, f.selfID)
	f.mustExec(`UPDATE request_logs SET route_kind=? WHERE logical_request_id=?`, RouteCharityEmbeddings, f.charityID)
	self, err := f.repo.GetUser(ctx, logUserOne, f.selfID, AttemptFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if typed, ok := self.(UserSelfLogDetail); !ok || typed.Request.RouteKind != RouteOpenAIEmbeddings || len(typed.Attempts.Data) != 2 {
		t.Fatalf("self projection: %+v", self)
	}
	charity, err := f.repo.GetUser(ctx, logUserOne, f.charityID, AttemptFilter{})
	if err != nil {
		t.Fatal(err)
	}
	requireNoJSONKeys(t, charity, "attempts", "attempt_count", "endpoint_base_url", "upstream_model_id", "diag", "caller_identity")
	if !strings.Contains(string(marshalLogGolden(t, charity)), string(RouteCharityEmbeddings)) {
		t.Fatal("operation was lost")
	}
	page, err := f.repo.ListUser(ctx, logUserOne, ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range page.Data {
		if typed, ok := row.(UserCharityLogRow); ok {
			if typed.RouteKind != RouteCharityEmbeddings {
				t.Fatal("wrong route")
			}
			requireNoJSONKeys(t, typed, "attempt_count", "caller_identity")
		}
	}
	if _, err := f.repo.GetUser(ctx, logUserTwo, f.charityID, AttemptFilter{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-owner log: %v", err)
	}
	f.mustExec(`UPDATE request_logs SET route_kind='unknown' WHERE logical_request_id=?`, f.charityID)
	if _, err := f.repo.GetUser(ctx, logUserOne, f.charityID, AttemptFilter{}); !errors.Is(err, ErrInvariant) {
		t.Fatalf("unknown route defaulted: %v", err)
	}
}
