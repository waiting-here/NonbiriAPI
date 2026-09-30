package modelname

import "testing"

func TestPublicCharityMarkerCompatibility(t *testing.T) {
	// Keep the public literal independent from the implementation constant.
	if CharityPrefix != "[\u516c\u76ca]" {
		t.Fatalf("wire prefix changed: %q", CharityPrefix)
	}
	for _, test := range []struct {
		name string
		want bool
	}{
		{"[\u516c\u76ca]provider/model", true}, {"[\u516c\u76ca]", true},
		{"[\u516c\u76ca]suffix", true}, {"[Charity]provider/model", false},
		{" [\u516c\u76ca]provider/model", false}, {"provider/[\u516c\u76ca]model", false},
		{"provider/model", false}, {"", false},
	} {
		if got := IsCharity(test.name); got != test.want {
			t.Fatalf("marker %q=%v", test.name, got)
		}
	}
}
