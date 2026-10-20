package db

import (
	"reflect"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/charitydiscount"
)

func TestRecurringDiscountStorageDefaultsAndBounds(t *testing.T) {
	database := openGenerationTwoDDLForTest(t)
	defer database.Close()
	for _, insert := range []string{
		`INSERT INTO charity_models(provider,model,full_name,pricing_mode,created_at,updated_at) VALUES('provider','model','[公益]provider/model','per_request',0,0)`,
		`INSERT INTO charity_price_templates(name,description,enabled,revision,pricing,discount,created_at,updated_at) VALUES('template','',1,1,'{}','{}',0,0)`,
	} {
		if _, err := database.Exec(insert); err != nil {
			t.Fatal(err)
		}
	}
	for _, table := range []string{"charity_models", "charity_price_templates"} {
		var encoded string
		if err := database.QueryRow("SELECT recurring_discount FROM " + table).Scan(&encoded); err != nil {
			t.Fatal(err)
		}
		value, err := charitydiscount.Decode(encoded)
		if err != nil || !reflect.DeepEqual(value, charitydiscount.Default()) {
			t.Fatal(table, value, err)
		}
		for _, invalid := range []any{nil, "[]", "null", "{", `{"padding":"` + strings.Repeat("x", 4096) + `"}`} {
			if _, err := database.Exec("UPDATE "+table+" SET recurring_discount=?", invalid); err == nil {
				t.Fatal(table, "accepted invalid stored object")
			}
		}
	}
}
