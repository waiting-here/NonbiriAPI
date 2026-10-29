package host

import (
	"context"
	"testing"
	"time"
)

func TestHostReadersWhileWriterHeld(t *testing.T) {
	f := newGameFixture(t, nil)
	writer, err := f.database.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Rollback()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if _, err = f.service.ReadGamesConfig(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.HomeSummary(ctx, f.adminID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.ActiveCounts(ctx); err != nil {
		t.Fatal(err)
	}
	if n := f.service.services.ReadDatabase.Stats().InUse; n != 0 {
		t.Fatalf("reader retained: %d", n)
	}
}
