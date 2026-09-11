package charityrouting

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
)

func TestEmbeddingPreflightSkipsOnlyShortContentPolicy(t *testing.T) {
	e := newRoutingTestEnv(t)
	admin := e.seedUser(t, true, nil)
	caller := e.seedUser(t, false, nil)
	e.seedUserBalance(t, caller, "100000")
	e.createModel(t, 'M')
	if _, err := e.store.DB().Exec(`UPDATE site_config SET value='1000' WHERE key='charity_min_chars'`); err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{`" "`, `[1]`, `["x","y"]`, `[[1],[2]]`} {
		r, err := openai.DecodeEmbeddingRequest(strings.NewReader(`{"model":"[公益]provider/model","input":`+input+`}`), 0)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Clear()
		if _, err := e.service.PreflightEmbedding(context.Background(), caller, r.Model, r, routingTestNow); err != nil {
			t.Fatal(err)
		}
		if _, err := e.service.PreflightEmbedding(context.Background(), admin, r.Model, r, routingTestNow); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("admin bypass: %v", err)
		}
		if _, err := e.store.DB().Exec(`UPDATE users SET charity_suspended_until=? WHERE id=?`, routingTestNow+1, caller); err != nil {
			t.Fatal(err)
		}
		if _, err := e.service.PreflightEmbedding(context.Background(), caller, r.Model, r, routingTestNow); !errors.Is(err, ErrCharitySuspended) {
			t.Fatalf("suspension bypass: %v", err)
		}
		if _, err := e.store.DB().Exec(`UPDATE users SET charity_suspended_until=NULL WHERE id=?`, caller); err != nil {
			t.Fatal(err)
		}
	}
	if e.state.dueCalls.Load() != 0 {
		t.Fatal("preflight inspected physical candidates")
	}
}
