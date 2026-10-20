package contract

import (
	"net/http"
	"testing"
	"time"
)

func TestRetryAfterParsing(t *testing.T) {
	received := time.Unix(1_700_000_000, 500_000_000)
	cases := []struct {
		name, header, value string
		until               int64
		valid               bool
	}{
		{"seconds", "Retry-After", "60", 1700000061, true},
		{"zero", "Retry-After", "0", 1700000000, true},
		{"whitespace", "Retry-After", " 	2	 ", 1700000003, true},
		{"leading zeros", "Retry-After", "0002", 1700000003, true},
		{"standard never epoch", "Retry-After", "1700000060", 3400000061, true},
		{"date", "Retry-After", "Tue, 14 Nov 2023 22:14:20 GMT", 1700000060, true},
		{"past", "Retry-After", "Tue, 14 Nov 2023 22:12:20 GMT", 1699999940, true},
		{"old date", "Retry-After", "Thu, 01 Jan 1970 00:00:00 GMT", 0, true},
		{"x seconds", "X-Retry-After", "999999999", 2700000000, true},
		{"x epoch seconds", "X-Retry-After", "1700000060", 1700000060, true},
		{"x epoch millis", "X-Retry-After", "1700000060001", 1700000061, true},
		{"x exact millis", "X-Retry-After", "1700000060000", 1700000060, true},
		{"x maximum seconds", "X-Retry-After", "9999999999", 9999999999, true},
		{"x maximum millis", "X-Retry-After", "9999999999999", 10000000000, true},
		{"x date", "X-Retry-After", "Tue, 14 Nov 2023 22:14:20 GMT", 1700000060, true},
		{"x ten zero padded", "X-Retry-After", "0000000060", 0, false},
		{"x eleven", "X-Retry-After", "17000000600", 0, false},
		{"x twelve", "X-Retry-After", "170000006000", 0, false},
		{"x fourteen", "X-Retry-After", "17000000600000", 0, false},
		{"empty", "Retry-After", "", 0, false},
		{"negative", "Retry-After", "-1", 0, false},
		{"plus", "Retry-After", "+1", 0, false},
		{"fraction", "Retry-After", "1.5", 0, false},
		{"exponent", "Retry-After", "1e3", 0, false},
		{"combined", "Retry-After", "60,120", 0, false},
		{"signed maximum", "Retry-After", "9223372036854775807", 0, false},
		{"signed overflow", "Retry-After", "9223372036854775808", 0, false},
		{"unsigned maximum", "Retry-After", "18446744073709551615", 0, false},
		{"x signed overflow", "X-Retry-After", "9223372036854775808", 0, false},
		{"overflow", "Retry-After", "18446744073709551616", 0, false},
		{"date overflow", "Retry-After", "253402300799", 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, valid := ParseRetryAfter(http.Header{tc.header: []string{tc.value}}, received)
			if got != tc.until || valid != tc.valid {
				t.Fatalf("got %d/%t, want %d/%t", got, valid, tc.until, tc.valid)
			}
		})
	}
	headers := http.Header{"Retry-After": {"5", "invalid", "Tue, 14 Nov 2023 22:14:20 GMT"}, "X-Retry-After": {"1700000120001", "-10"}}
	if got, valid := ParseRetryAfter(headers, received); !valid || got != 1700000121 {
		t.Fatalf("combined got %d/%t", got, valid)
	}
}

func TestRetryAfterDeadlineOverflow(t *testing.T) {
	for _, received := range []time.Time{time.Unix(maxRetryAfterUnix, 0), time.Unix(maxRetryAfterUnix, 1)} {
		if _, valid := ParseRetryAfter(http.Header{"Retry-After": {"1"}}, received); valid {
			t.Fatal("deadline exceeded storage range")
		}
		if until, valid := ParseRetryAfter(http.Header{"Retry-After": {"0"}}, received); !valid || until != maxRetryAfterUnix {
			t.Fatal(until, valid)
		}
	}
}
