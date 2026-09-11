package strictjson

import (
	"fmt"
	"strings"
	"testing"
)

func TestBulkFieldBudgetDoesNotRelaxExistingDecoders(t *testing.T) {
	var body strings.Builder
	body.WriteString(`{"keys":[`)
	for i := 0; i < 300; i++ {
		if i > 0 {
			body.WriteByte(',')
		}
		fmt.Fprintf(&body, `{"secret":"fixture-%d"}`, i)
	}
	body.WriteString(`]}`)
	if ValidateObject([]byte(body.String())) == nil {
		t.Fatal("default field budget expanded")
	}
	if err := ValidateObjectWithFieldLimit([]byte(body.String()), 301); err != nil {
		t.Fatal(err)
	}
	if ValidateObjectWithFieldLimit([]byte(body.String()), 300) == nil {
		t.Fatal("bulk budget exceeded")
	}
	for _, invalid := range []string{`{"a":1,"\u0061":2}`, `{"keys":[{"note":"x","note":"y"}]}`, `{"note":"\ud800"}`, `{} {}`} {
		if ValidateObjectWithFieldLimit([]byte(invalid), 16384) == nil {
			t.Fatalf("invalid syntax accepted: %s", invalid)
		}
	}
}
