package charityreserve

import (
	"github.com/waiting-here/NonbiriAPI/internal/credits"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"testing"
)

func TestOutputReserveCoversClassifiedAndFallbackRates(t *testing.T) {
	policy := credits.ContextPricing{
		UserModalities:  &credits.ModalityPricing{OutputEnabled: true, Audio: &credits.TokenPrices{Output: 20}},
		DonorModalities: &credits.ModalityPricing{OutputEnabled: true, Audio: &credits.TokenPrices{Output: 1000}},
		LongContext:     &credits.LongContextPrice{InputThreshold: 100, UserPrices: [4]int64{1, 2, 3, 30}, UserModalities: &credits.ModalityPricing{OutputEnabled: true, Video: &credits.TokenPrices{Output: 40}}},
	}
	got, err := OutputUnitPrice(10, policy.Encode())
	if err != nil || got != 40 {
		t.Fatalf("highest output rate=%d, %v", got, err)
	}
	policy.LongContext.UserModalities.OutputEnabled = false
	got, err = OutputUnitPrice(10, policy.Encode())
	if err != nil || got != 30 {
		t.Fatalf("tier fallback=%d, %v", got, err)
	}
	policy.LongContext = nil
	policy.UserModalities.OutputEnabled = false
	got, err = OutputUnitPrice(10, policy.Encode())
	if err != nil || got != 10 {
		t.Fatalf("disabled modality=%d, %v", got, err)
	}
	got, err = OutputUnitPrice(10, "{}")
	if err != nil || got != 10 {
		t.Fatalf("legacy=%d, %v", got, err)
	}
	if _, err = OutputUnitPrice(db.MaxMoneyMilli+1, "{}"); err == nil {
		t.Fatal("accepted overflow price")
	}
}
