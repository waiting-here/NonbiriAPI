package textprivacy

import "testing"

func TestIPv4Presentation(t *testing.T) {
	cases := map[string]string{
		"dial 192.0.2.1:443":                           "dial [redacted]:443",
		"https://203.0.113.9/x?peer=10.0.0.1":          "https://[redacted]/x?peer=[redacted]",
		"来源(0.0.0.0)，目标255.255.255.255。":               "来源([redacted])，目标[redacted]。",
		"address192.0.2.1suffix and 127.0.0.1.":        "address[redacted]suffix and [redacted].",
		"256.0.0.1 999.192.0.2.1 1.2.3.4.5 1192.0.2.1": "256.0.0.1 999.192.0.2.1 1.2.3.4.5 1192.0.2.1",
		"01.2.3.4 ::1 2001:db8::1":                     "01.2.3.4 ::1 2001:db8::1",
		"no address [redacted]":                        "no address [redacted]",
	}
	for input, want := range cases {
		if got := IPv4(input); got != want {
			t.Errorf("IPv4(%q)=%q, want %q", input, got, want)
		}
	}
}

func BenchmarkIPv4NoAddress(b *testing.B) {
	for b.Loop() {
		_ = IPv4("The upstream service is temporarily unavailable.")
	}
}
