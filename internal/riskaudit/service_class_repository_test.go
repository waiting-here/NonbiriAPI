package riskaudit

import (
	"context"
	"errors"
	"testing"
)

func TestServiceClassSaturationPersistenceDistinguishesMissingAndZero(t *testing.T) {
	f := newAuditFixture(t)
	user := f.user(1)
	start := f.now/60*60 - 180
	zero, full := int64(0), int64(60000000000)
	values := []Minute{}
	for i, p := range []*int64{nil, &zero, &full} {
		values = append(values, Minute{Epoch: "classes", UserID: user, Minute: start + int64(i)*60, Kind: "total", RPMLimit: 60, ConcurrencyLimit: 20, OccupancyMillis: 300000, ConfigRevision: 1, UpdatedAt: f.now, SaturationMicrosMillis: p})
	}
	if err := f.repository.SaveMinutes(context.Background(), values, nil); err != nil {
		t.Fatal(err)
	}
	tx, err := f.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	got, more, err := readMinutes(context.Background(), tx, []int64{user}, Window{From: start, To: f.now, Kind: "total"})
	tx.Rollback()
	if err != nil || more || len(got) != 3 || got[0].SaturationMicrosMillis != nil || got[1].SaturationMicrosMillis == nil || *got[1].SaturationMicrosMillis != 0 || got[2].SaturationMicrosMillis == nil || *got[2].SaturationMicrosMillis != full {
		t.Fatalf("stored evidence: %+v %v", got, err)
	}
	invalid := full + 1
	values[0].SaturationMicrosMillis = &invalid
	if err := f.repository.SaveMinutes(context.Background(), values, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid saturation: %v", err)
	}
	values[0].SaturationMicrosMillis = &zero
	values[0].Kind = "charity"
	if err := f.repository.SaveMinutes(context.Background(), values, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("category saturation: %v", err)
	}
}
