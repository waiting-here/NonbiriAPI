package sessionheader

import (
	"database/sql"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"sync"
	"testing"
)

func TestConfigDefaultsZeroBoundsAndFormats(t *testing.T) {
	for _, format := range []string{"uuid_v4", "hex_32", "opencode_session", "opencode_message", "opencode_part"} {
		for _, idle := range []int{0, 1, 86400} {
			raw := fmt.Sprintf(`[{"name":"X-Session","idle_seconds":%d,"format":%q}]`, idle, format)
			values, err := Decode(raw)
			if err != nil || len(values) != 1 || values[0].IdleSeconds != idle || values[0].Format != format {
				t.Fatal(values, err)
			}
			encoded, err := Encode(values)
			if err != nil {
				t.Fatal(err)
			}
			roundtrip, err := Decode(encoded)
			if err != nil || roundtrip[0] != values[0] {
				t.Fatal(roundtrip, err)
			}
		}
	}
	defaults, err := Decode(`[{"name":"X-Session"}]`)
	if err != nil || defaults[0].IdleSeconds != 300 || defaults[0].Format != "uuid_v4" {
		t.Fatal(defaults, err)
	}
	for _, raw := range []string{`[{"name":"x","idle_seconds":86401}]`, `[{"name":"x","idle_seconds":0.1}]`, `[{"name":"x","idle_seconds":null}]`, `[{"name":"x","format":"opencode"}]`} {
		if _, err := Decode(raw); err == nil {
			t.Fatal("accepted", raw)
		}
	}
	if _, err := randomValue("unknown"); err == nil {
		t.Fatal("unknown format generated")
	}
}

func TestOpenCodeDirectionCounterCarryAndLow48Bits(t *testing.T) {
	const at int64 = 1_800_000_000_000
	openCodeSequence.Lock()
	openCodeSequence.timestamp, openCodeSequence.counter = at, 0
	openCodeSequence.Unlock()
	for i, tc := range []struct {
		format, prefix string
		reverse        bool
	}{
		{"opencode_session", "ses_", true}, {"opencode_message", "msg_", false}, {"opencode_part", "prt_", false}, {"opencode_session", "ses_", true},
	} {
		value, err := openCodeValue(tc.format, at)
		if err != nil || len(value) != 30 || value[:4] != tc.prefix || !regexp.MustCompile("^[0-9A-Za-z]{14}$").MatchString(value[16:]) {
			t.Fatal(value, err)
		}
		got, err := strconv.ParseUint(value[4:16], 16, 64)
		want := uint64(at)*0x1000 + uint64(i+1)
		if tc.reverse {
			want = ^want
		}
		if err != nil || got != want&0xffffffffffff {
			t.Fatalf("timestamp: got %x want %x: %v", got, want&0xffffffffffff, err)
		}
		request, _ := http.NewRequest("GET", "https://upstream.example", nil)
		if err := Apply(request, http.Header{"X-Session": {value}}, nil); err != nil {
			t.Fatal(err)
		}
	}
	openCodeSequence.Lock()
	openCodeSequence.timestamp, openCodeSequence.counter = at, 4095
	openCodeSequence.Unlock()
	value, err := openCodeValue("opencode_message", at)
	expected := fmt.Sprintf("%012x", (uint64(at)*0x1000+4096)&0xffffffffffff)
	if err != nil || value[4:16] != expected {
		t.Fatal(value, expected, err)
	}
	next, err := openCodeValue("opencode_part", at+1)
	expected = fmt.Sprintf("%012x", ((uint64(at)+1)*0x1000+1)&0xffffffffffff)
	if err != nil || next[4:16] != expected {
		t.Fatal(next, expected, err)
	}
}

func TestOpenCodeConcurrentGeneration(t *testing.T) {
	var wg sync.WaitGroup
	values := make(chan string, 128)
	for i := range 128 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			value, err := randomValue([]string{"opencode_session", "opencode_message", "opencode_part"}[i%3])
			if err != nil {
				t.Error(err)
				return
			}
			values <- value
		}()
	}
	wg.Wait()
	close(values)
	for value := range values {
		if !valuePattern.MatchString(value) {
			t.Error(value)
		}
	}
}

func acquirePersonal(t *testing.T, d *sql.DB, id string, user, model, at int64) http.Header {
	t.Helper()
	mustExec(t, d, "INSERT OR IGNORE INTO logical_requests VALUES(?)", id)
	tx, err := d.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	h, err := AcquirePersonal(t.Context(), tx, id, user, model, at)
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return h
}

func TestPersonalIsolationRetryDeletionAndExpiry(t *testing.T) {
	d := sessionDB(t)
	a := acquirePersonal(t, d, "personal", 1, 1, 100).Get("X-Session-ID")
	for _, v := range []string{acquire(t, d, "charity", 1, 1, 100).Get("X-Session-ID"), acquirePersonal(t, d, "model", 1, 2, 100).Get("X-Session-ID"), acquirePersonal(t, d, "user", 2, 3, 100).Get("X-Session-ID")} {
		if v == a {
			t.Fatal("session crossed ownership or scope")
		}
	}
	if got := acquirePersonal(t, d, "personal", 1, 1, 1000).Get("X-Session-ID"); got != a {
		t.Fatal("retry changed")
	}
	tx, _ := d.BeginTx(t.Context(), nil)
	if _, err := AcquirePersonal(t.Context(), tx, "personal", 2, 1, 1000); err == nil {
		t.Fatal("foreign model accepted")
	}
	tx.Rollback()
	finish(t, d, "personal", 1000, true)
	if got := acquirePersonal(t, d, "reuse", 1, 1, 1299).Get("X-Session-ID"); got != a {
		t.Fatal("idle reuse lost")
	}
	finish(t, d, "reuse", 1299, false)
	if got := acquirePersonal(t, d, "expired", 1, 1, 1300).Get("X-Session-ID"); got == a {
		t.Fatal("expired value reused")
	}
	mustExec(t, d, "DELETE FROM models WHERE id=1")
	finish(t, d, "expired", 1400, true)
	var count int
	if err := d.QueryRow("SELECT count(*) FROM personal_header_sessions WHERE model_id=1").Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	mustExec(t, d, "DELETE FROM users WHERE id=2")
	finish(t, d, "user", 1500, true)
	if err := d.QueryRow("SELECT count(*) FROM personal_header_sessions WHERE user_id=2").Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
}

func TestZeroIdleConcurrentRequestsAndFrozenRetry(t *testing.T) {
	for _, personal := range []bool{false, true} {
		t.Run(fmt.Sprint(personal), func(t *testing.T) {
			d := sessionDB(t)
			table, prefix := "charity_models", "charity"
			get := acquire
			if personal {
				table, prefix, get = "models", "personal", acquirePersonal
			}
			get(t, d, "previous", 1, 1, 50)
			mustExec(t, d, "UPDATE "+table+" SET session_headers=? WHERE id=1", `[{"name":"x-session-id","idle_seconds":0,"format":"opencode_message"}]`)
			var wg sync.WaitGroup
			values := make(chan string, 8)
			for i := range 8 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					id := fmt.Sprintf("zero-%d", i)
					h := get(t, d, id, 1, 1, 100)
					retry := get(t, d, id, 1, 1, 1000)
					if h.Get("X-Session-ID") != retry.Get("X-Session-ID") {
						t.Error("retry changed")
					}
					values <- h.Get("X-Session-ID")
				}()
			}
			wg.Wait()
			close(values)
			seen := map[string]bool{}
			for value := range values {
				if seen[value] {
					t.Fatal("parallel request shared generation")
				}
				seen[value] = true
			}
			var count int
			if err := d.QueryRow("SELECT count(*) FROM " + prefix + "_header_sessions WHERE name='x-session-id'").Scan(&count); err != nil || count != 0 {
				t.Fatal(count, err)
			}
			for i := range 8 {
				finish(t, d, fmt.Sprintf("zero-%d", i), 1200, true)
			}
			finish(t, d, "previous", 1200, true)
			if err := d.QueryRow("SELECT count(*) FROM " + prefix + "_header_uses").Scan(&count); err != nil || count != 0 {
				t.Fatal(count, err)
			}
		})
	}
}

func TestPersonalCleanupIsBoundedAcrossScopes(t *testing.T) {
	d := sessionDB(t)
	acquire(t, d, "charity", 1, 1, 100)
	acquirePersonal(t, d, "personal", 1, 1, 100)
	finish(t, d, "charity", 100, true)
	finish(t, d, "personal", 100, true)
	tx, _ := d.BeginTx(t.Context(), nil)
	defer tx.Rollback()
	n, err := Cleanup(t.Context(), tx, 400, 2)
	if err != nil || n != 2 {
		t.Fatal(n, err)
	}
	n, err = Cleanup(t.Context(), tx, 400, 2)
	if err != nil || n != 1 {
		t.Fatal(n, err)
	}
}
