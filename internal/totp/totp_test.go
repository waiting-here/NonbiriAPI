package totp

import "testing"

func TestRFC6238SHA1Vectors(t *testing.T) {
	seed := []byte("12345678901234567890")
	for _, v := range []struct {
		at   int64
		code string
	}{
		{59, "287082"}, {1111111109, "081804"}, {1111111111, "050471"},
		{1234567890, "005924"}, {2000000000, "279037"}, {20000000000, "353130"},
	} {
		if got := Code(seed, v.at/30); got != v.code {
			t.Fatalf("RFC 6238 at %d: %s != %s", v.at, got, v.code)
		}
	}
}
func TestWindowAndReplay(t *testing.T) {
	seed := []byte("12345678901234567890")
	for _, step := range []int64{98, 99, 100, 101, 102} {
		got, ok := Match(seed, Code(seed, step), 3000, -1)
		want := step >= 99 && step <= 101
		if ok != want || ok && got != step {
			t.Fatalf("step %d accepted=%t", step, ok)
		}
		if _, ok := Match(seed, Code(seed, step), 3000, step); ok {
			t.Fatal("consumed step accepted")
		}
	}
	for _, code := range []string{"", "12345", "1234567", "12345x", " 12345"} {
		if _, ok := Match(seed, code, 3000, -1); ok {
			t.Fatal("noncanonical code accepted")
		}
	}
}
