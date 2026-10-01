package stewardautomation

import "testing"

func TestAutomationNullPolicySurvivesSharedDecode(t *testing.T) {
	for _, test := range []struct {
		body string
		want bool
	}{
		{"{\"description\":null}", false},
		{"{\"keys\":[null]}", false},
		{"{\"keys\":[{\"label\":null}]}", false},
		{"{\"keys\":[{\"expires_at\":null}]}", true},
		{"{\"description\":\"ok\"}", true},
	} {
		var input createInput
		if got := decode([]byte(test.body), &input) == nil; got != test.want {
			t.Fatalf("%s accepted=%v", test.body, got)
		}
	}
}
