package inactivity

import (
	"context"
	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	"testing"
	"time"
)

func TestReaderPoolEmptyInactivityCandidates(t *testing.T) {
	e := fixture(t)
	dbtest.WithoutWriter(t, e.store.DB(), e.store.ReadDB(), func(ctx context.Context) {
		out, err := e.s.Process(ctx, testNow, 1, time.Now().Add(5*time.Second))
		if err != nil {
			t.Fatalf("%+v: %v", out, err)
		}
	})
}
