package diagnostic

import (
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

// assertBoundary is the invariant every bounded diagnostic must satisfy: within
// the byte limit, valid UTF-8, and free of every line/control separator this
// package normalizes (CR/LF/TAB).
func assertBoundary(t *testing.T, got string, limit int) {
	t.Helper()
	if limit <= 0 || limit > MaxBytes {
		limit = MaxBytes
	}
	if len(got) > limit {
		t.Fatalf("len = %d, want <= %d: %q", len(got), limit, got)
	}
	if !utf8.ValidString(got) {
		t.Fatalf("not valid UTF-8: %q", got)
	}
	if strings.ContainsAny(got, "\r\n\t") {
		t.Fatalf("contains CR/LF/TAB (line forgery vector): %q", got)
	}
}

func TestBoundShortUnchanged(t *testing.T) {
	if got := Bound("short"); got != "short" {
		t.Fatalf("Bound(short) = %q, want unchanged", got)
	}
	if got := Bound(""); got != "" {
		t.Fatalf("Bound(\"\") = %q, want empty", got)
	}
}

func TestBoundControlNormalization(t *testing.T) {
	// CR, LF, and TAB all become spaces; the result is single-line.
	const want = "before   after"
	if got := Bound("before\r\n\tafter"); got != want {
		t.Fatalf("Bound controls = %q, want %q", got, want)
	}
	assertBoundary(t, want, MaxBytes)
}

func TestBoundC0AndDELStripped(t *testing.T) {
	// NUL, 0x01, BEL, BS, ESC and DEL carry no diagnostic value and are
	// stripped; printable ASCII survives.
	in := "a\x00b\x01c\x07d\x08e\x1bf\x7fg"
	got := Bound(in)
	if got != "abcdefg" {
		t.Fatalf("Bound C0/DEL = %q, want %q", got, "abcdefg")
	}
	assertBoundary(t, got, MaxBytes)
}

func TestBoundInvalidUTF8Repaired(t *testing.T) {
	// A single invalid byte becomes one replacement rune.
	got := Bound(string([]byte{'a', 0xff, 'b'}))
	if !utf8.ValidString(got) || got != "a\uFFFDb" {
		t.Fatalf("Bound invalid UTF-8 = %q, valid=%v", got, utf8.ValidString(got))
	}
	// A run of consecutive invalid bytes collapses into a single replacement.
	got = Bound(string([]byte{'a', 0xff, 0xfe, 'b'}))
	if !utf8.ValidString(got) || got != "a\uFFFDb" {
		t.Fatalf("Bound invalid run = %q, valid=%v", got, utf8.ValidString(got))
	}
	// Truncation never splits a rune, so output is always valid UTF-8 even when
	// invalid bytes appear near the boundary.
	big := strings.Repeat("\xff", MaxBytes+64)
	got = Bound(big)
	assertBoundary(t, got, MaxBytes)
	// The whole input is invalid, so it collapses to replacement runes; with a
	// 3-byte replacement and a 14-byte marker, the result is well under limit.
	if !utf8.ValidString(got) {
		t.Fatalf("big invalid input produced invalid UTF-8: %q", got)
	}
}

func TestBoundExactByteBoundary(t *testing.T) {
	exact := strings.Repeat("x", MaxBytes)
	if got := Bound(exact); got != exact {
		t.Fatalf("exact boundary changed: got len=%d want len=%d", len(got), len(exact))
	}
	// A boundary-sized input whose last byte is a control char still normalizes
	// to exactly MaxBytes without a marker (the LF -> space keeps the length).
	exactWithControl := strings.Repeat("x", MaxBytes-1) + "\n"
	got := Bound(exactWithControl)
	if want := strings.Repeat("x", MaxBytes-1) + " "; got != want {
		t.Fatalf("exact normalized boundary = len %d, want %d, suffix=%q",
			len(got), MaxBytes, got[len(got)-4:])
	}
	assertBoundary(t, got, MaxBytes)
}

func TestBoundOverASCIITruncatesWithMarker(t *testing.T) {
	over := strings.Repeat("x", MaxBytes+1)
	got := Bound(over)
	assertBoundary(t, got, MaxBytes)
	if !strings.HasSuffix(got, TruncationMarker) {
		t.Fatalf("over ASCII missing marker: suffix=%q", got[len(got)-len(TruncationMarker):])
	}
	// Content before the marker is a prefix of the input (not corrupted).
	content := strings.TrimSuffix(got, TruncationMarker)
	if !strings.HasPrefix(over, content) {
		t.Fatalf("truncated content not a prefix of input: %q", content)
	}
}

func TestBoundMultibyteAndFourByteRuneTruncation(t *testing.T) {
	// 3-byte CJK runes: a byte bound keeps ~1360 runes, not 4096 runes.
	cjk := strings.Repeat("公益", MaxBytes)
	got := Bound(cjk)
	assertBoundary(t, got, MaxBytes)
	if !strings.HasSuffix(got, TruncationMarker) {
		t.Fatalf("CJK truncation missing marker: %q", got[max(0, len(got)-len(TruncationMarker)):])
	}
	if rc := utf8.RuneCountInString(got); rc >= utf8.RuneCountInString(cjk) {
		t.Fatalf("CJK was not byte-truncated: rune count = %d", rc)
	}

	// 4-byte runes (emoji) must never be split across the boundary.
	emoji := strings.Repeat("🎉", MaxBytes) // U+1F389, 4 bytes each
	got = Bound(emoji)
	assertBoundary(t, got, MaxBytes)
	if !strings.HasSuffix(got, TruncationMarker) {
		t.Fatalf("emoji truncation missing marker")
	}
	// Every content rune is complete: decode the content prefix fully.
	content := strings.TrimSuffix(got, TruncationMarker)
	if !utf8.ValidString(content) {
		t.Fatalf("emoji content split a rune: %q", content)
	}
}

func TestBoundToClampsLimit(t *testing.T) {
	long := strings.Repeat("x", MaxBytes+128)
	// Non-positive and over-default limits both fall back to MaxBytes.
	if got := BoundTo(long, 0); got != Bound(long) {
		t.Fatalf("BoundTo(,0) differs from Bound")
	}
	if got := BoundTo(long, -1); got != Bound(long) {
		t.Fatalf("BoundTo(,-1) differs from Bound")
	}
	if got := BoundTo(long, MaxBytes+1); got != Bound(long) {
		t.Fatalf("BoundTo(,>Max) differs from Bound")
	}
	if got := BoundTo(long, 10_000); got != Bound(long) {
		t.Fatalf("BoundTo(,10000) differs from Bound")
	}
	// A smaller positive limit is honored.
	got := BoundTo(long, 64)
	assertBoundary(t, got, 64)
	if !strings.HasSuffix(got, TruncationMarker) {
		t.Fatalf("BoundTo small limit missing marker")
	}
}

func TestBoundToTinyLimits(t *testing.T) {
	long := strings.Repeat("x", 4096+16)
	// Limits below the first marker rune (3 bytes for "…") yield empty output,
	// since even the marker prefix cannot fit on a rune boundary.
	for _, n := range []int{1, 2} {
		if got := BoundTo(long, n); got != "" {
			t.Fatalf("BoundTo(,%d) = %q, want empty (marker cannot fit)", n, got)
		}
	}
	// Exactly 3 bytes fits the first marker rune.
	if got := BoundTo(long, 3); got != "…" {
		t.Fatalf("BoundTo(,3) = %q, want %q", got, "…")
	}
	// 4 bytes fits "…[" (marker prefix on a rune boundary).
	if got := BoundTo(long, 4); got != "…[" {
		t.Fatalf("BoundTo(,4) = %q, want %q", got, "…[")
	}
	// A limit equal to the marker length yields the whole marker, no content.
	if got := BoundTo(long, len(TruncationMarker)); got != TruncationMarker {
		t.Fatalf("BoundTo(,marker) = %q, want %q", got, TruncationMarker)
	}
	// One byte past the marker fits one content byte plus the marker.
	if got := BoundTo(long, len(TruncationMarker)+1); got != "x"+TruncationMarker {
		t.Fatalf("BoundTo(,marker+1) = %q, want %q", got, "x"+TruncationMarker)
	}
	// A multibyte rune that does not fit the content budget is dropped rather
	// than split; the marker fills the remainder on a rune boundary.
	if got := BoundTo("あい", 3); got != "…" {
		t.Fatalf("BoundTo(あい,3) = %q, want %q", got, "…")
	}
	if got := BoundTo("あい", 4); got != "…[" {
		t.Fatalf("BoundTo(あい,4) = %q, want %q", got, "…[")
	}
}

func TestBoundToLimitSmallerThanOneRune(t *testing.T) {
	// A 3-byte rune under a 1-byte limit: truncation triggers immediately and
	// the marker prefix (empty) is emitted, never an over-length byte.
	if got := BoundTo("あ", 1); got != "" {
		t.Fatalf("BoundTo(あ,1) = %q, want empty", got)
	}
	// When the single rune exactly fits, no truncation occurs.
	if got := BoundTo("あ", 3); got != "あ" {
		t.Fatalf("BoundTo(あ,3) = %q, want あ", got)
	}
}

func TestBoundLargeInputStopsAtBoundary(t *testing.T) {
	large := strings.Repeat("x", 32<<20) // 32 MiB
	got := Bound(large)
	assertBoundary(t, got, MaxBytes)
	if !strings.HasSuffix(got, TruncationMarker) {
		t.Fatalf("large input missing marker")
	}
	// A multibyte large input exercises byte (not rune) bounding.
	largeCJK := strings.Repeat("公益", 8<<20) // ~48 MiB of 3-byte runes
	got = Bound(largeCJK)
	assertBoundary(t, got, MaxBytes)
	if !strings.HasSuffix(got, TruncationMarker) {
		t.Fatalf("large CJK input missing marker")
	}

	// The output allocation must be bounded by the limit, never proportional to
	// the 32 MiB input: only the output buffer and the final string conversion
	// allocate, plus at most the slice header. Anything input-sized would blow
	// past this bound by orders of magnitude.
	allocs := testing.AllocsPerRun(5, func() {
		_ = Bound(large)
	})
	if allocs > 4 {
		t.Fatalf("large input allocations = %.1f, want bounded output allocations", allocs)
	}
}

func TestBoundDoesNotForgeLogLines(t *testing.T) {
	// Inputs crafted to mimic header/log/HTML/CSV control-char attacks. The
	// package's job is only its contract scope: no CR/LF/TAB survives, output is
	// valid UTF-8 and within the byte budget. Sink-specific escaping (HTML
	// tag/text rendering, CSV =/+/-/@, header CRLF rejection) is intentionally
	// NOT done here and must remain each sink's responsibility.
	cases := []struct {
		name string
		in   string
	}{
		{"header_crlf", "X-Header: value\r\nSet-Cookie: evil=1\r\n"},
		{"log_line_injection", "ok\n[ERROR] fake admin action"},
		{"csv_formula", "=\r\n+@-TAB\there"},
		{"html_script_close", "fine</script><img src=x>"},
		{"bom_and_unicode_space", "\xef\xbb\xbf=cmd\u3000data"},
		{"nul_terminator", "admin\x00\x00root"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Bound(c.in)
			assertBoundary(t, got, MaxBytes)
			// No line forgery: CR/LF/TAB are gone.
			if strings.ContainsAny(got, "\r\n\t") {
				t.Fatalf("line forgery survived: %q", got)
			}
			// The package must NOT do HTML/CSV escaping: those special bytes are
			// printable and must pass through untouched for the owning sink.
			if c.name == "html_script_close" && !strings.Contains(got, "</script>") {
				t.Fatalf("HTML escaping leaked into the diagnostic boundary: %q", got)
			}
			if c.name == "csv_formula" {
				// '=', '+', '@', '-' are printable and survive; only C0/DEL/CR/LF/TAB move.
				for _, ch := range "=+@-" {
					if !strings.ContainsRune(got, ch) {
						t.Fatalf("CSV formula char %q was stripped (not this package's job): %q", ch, got)
					}
				}
			}
		})
	}
}

func TestBoundConcurrentNoSharedState(t *testing.T) {
	// The boundary holds no package-level mutable state and allocates a fresh
	// buffer per call, so concurrent callers must not race. Run under -race.
	inputs := []string{
		strings.Repeat("x", MaxBytes+32),
		"before\r\n\tafter",
		string([]byte{'a', 0xff, 0xfe, 'b'}),
		strings.Repeat("公益🎉", 1024),
		"",
	}
	const goroutines = 64
	const iterations = 200
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(seed int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				in := inputs[(seed+j)%len(inputs)]
				limit := []int{0, -1, MaxBytes + 1, 16, 3, 4096}[(seed+j)%6]
				got := BoundTo(in, limit)
				assertBoundary(t, got, limit)
			}
		}(i)
	}
	wg.Wait()
}

func TestMarkerIsSafe(t *testing.T) {
	if !utf8.ValidString(TruncationMarker) {
		t.Fatalf("TruncationMarker is not valid UTF-8: %q", TruncationMarker)
	}
	if strings.ContainsAny(TruncationMarker, "\r\n\t") {
		t.Fatalf("TruncationMarker contains a line separator: %q", TruncationMarker)
	}
	if len(TruncationMarker) >= MaxBytes {
		t.Fatalf("TruncationMarker len = %d, must be < MaxBytes = %d", len(TruncationMarker), MaxBytes)
	}
}

func TestAppendMarkerDefensiveClamps(t *testing.T) {
	// appendMarker's content-budget clamp (end > len(output)) is defensive: it
	// is unreachable through BoundTo, because truncation only fires once output
	// already exceeds maxBytes - maxRuneSize, so the content budget can never
	// exceed the accumulated output. Exercise it directly to prove it caps at
	// the available output and still yields valid, bounded, marker-suffixed
	// UTF-8.
	got := appendMarker([]byte("x"), 100)
	if !utf8.ValidString(got) || !strings.HasSuffix(got, TruncationMarker) {
		t.Fatalf("appendMarker defensive clamp = %q", got)
	}
	if len(got) != 1+len(TruncationMarker) {
		t.Fatalf("appendMarker clamp len = %d, want %d", len(got), 1+len(TruncationMarker))
	}

	// markerPrefix caps at the marker length even when handed a maxBytes larger
	// than the marker (also defensive: appendMarker only calls it with
	// maxBytes <= len(marker)).
	if got := markerPrefix(len(TruncationMarker) + 10); got != TruncationMarker {
		t.Fatalf("markerPrefix over-cap = %q, want %q", got, TruncationMarker)
	}
}
