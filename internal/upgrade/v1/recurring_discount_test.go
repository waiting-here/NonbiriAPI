package v1

import (
	"database/sql"
	"reflect"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/charitydiscount"
	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func TestRecurringDiscountUpgradePreservesFixedPrices(t *testing.T) {
	path, vault := sourceFixtureFromDDL(t, schema25SQLForTest(t), 25, stableSchema25Manifest)
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, database, `INSERT INTO charity_models(provider,model,full_name,pricing_mode,discount_enabled,discount_percent,discount_start_at,discount_end_at,created_at,updated_at) VALUES('provider','model','[公益]provider/model','per_request',1,80,100,200,0,0)`)
	discount := `{"enabled":true,"percent":0,"start_at":100,"end_at":200}`
	mustExec(t, database, `INSERT INTO charity_price_templates(name,description,enabled,revision,pricing,discount,created_at,updated_at) VALUES('template','',1,1,'{}',?,0,0)`, discount)
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	result, err := Run(t.Context(), path, vault, Options{Apply: true})
	if err != nil || result.Status != "converted" {
		t.Fatal(result, err)
	}
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, table := range []string{"charity_models", "charity_price_templates"} {
		var encoded string
		if err := store.DB().QueryRow("SELECT recurring_discount FROM " + table).Scan(&encoded); err != nil {
			t.Fatal(err)
		}
		value, err := charitydiscount.Decode(encoded)
		if err != nil || !reflect.DeepEqual(value, charitydiscount.Default()) {
			t.Fatal(value, err)
		}
	}
	var enabled, percent, start, end int64
	if err := store.DB().QueryRow("SELECT discount_enabled,discount_percent,discount_start_at,discount_end_at FROM charity_models").Scan(&enabled, &percent, &start, &end); err != nil || enabled != 1 || percent != 80 || start != 100 || end != 200 {
		t.Fatal(enabled, percent, start, end, err)
	}
	var stored string
	if err := store.DB().QueryRow("SELECT discount FROM charity_price_templates").Scan(&stored); err != nil || stored != discount {
		t.Fatal(stored, err)
	}
}
