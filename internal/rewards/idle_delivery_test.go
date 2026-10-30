package rewards

import (
	"context"
	"testing"
	"time"
)

func TestIdleDeliveryDoesNotAcquireWriter(t *testing.T) {
	f := setup(t)
	held, err := f.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	before := f.db.Stats().WaitCount
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if n, err := f.s.ProcessBatch(ctx, 100); err != nil || n != 0 {
		t.Fatalf("idle delivery: %d, %v", n, err)
	}
	if f.db.Stats().WaitCount != before || f.s.config.ReadDatabase.Stats().InUse != 0 {
		t.Fatal("idle delivery waited for a writer or retained a reader")
	}
}
