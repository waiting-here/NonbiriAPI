package language

import "testing"

func TestExistingLanguageSelection(t *testing.T) {
	for _, test := range []struct{ input, want string }{
		{"zh", "zh"}, {"ZH", "zh"}, {"zh-CN", "zh"}, {"ZH-hant", "zh"},
		{"zh-CN,en;q=0.9", "zh"}, {"zh,en;q=0.9", "en"}, {" zh", "en"},
		{"zh_CN", "en"}, {"", "en"}, {"en", "en"}, {"fr", "en"},
	} {
		if got := NormalizeLanguage(test.input); got != test.want {
			t.Fatalf("%q=%q, want %q", test.input, got, test.want)
		}
		if got := Select(test.input, "Chinese text", "English text"); (got == "Chinese text") != (test.want == "zh") {
			t.Fatal("selection drift")
		}
	}
}
