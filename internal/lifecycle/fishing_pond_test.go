package lifecycle

import (
	"errors"
	"testing"
)

func TestFishingPondExportHasClosedSafeShapeAndBounds(t *testing.T) {
	assertClosedJSONKeys(t, FishingExport{}, "pending", "terminal", "single_best", "rolling_total", "rolling_best", "pond_queue", "pond_stats")
	assertClosedJSONKeys(t, FishingPondQueuedExport{}, "batch_id", "ordinal", "species_key", "returned_at", "identity")
	assertClosedJSONKeys(t, FishingPondIdentityExport{}, "kind", "display_name", "avatar_url")
	assertClosedJSONKeys(t, FishingPondGiftExport{}, "kind")
	assertClosedJSONKeys(t, FishingPondOutcomeExport{}, "return_state", "gift")
	assertClosedJSONKeys(t, FishingPondStatExport{}, "board", "period", "item_count")
	for _, fishing := range []FishingExport{
		{PondQueue: make([]FishingPondQueuedExport, CollectionLimit+1)},
		{PondStats: make([]FishingPondStatExport, CollectionLimit+1)},
	} {
		if err := validateExportCollectionBounds(ExportDocument{Fishing: fishing}); !errors.Is(err, ErrTooLarge) {
			t.Fatalf("oversized pond export: %v", err)
		}
	}
}
