package duel_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/game/duel"
	"github.com/waiting-here/NonbiriAPI/internal/game/historyexport"
)

func TestHistoryZIPCombinesArchivesAndAnonymizedRecentPages(t *testing.T) {
	f := adminFixture(t, "likes")
	oldID := f.terminalMatch(true, 1)
	f.clock.Store(100 + duel.RetentionSeconds)
	if _, err := f.s.Retain(f.ctx, f.clock.Load(), 100, time.Now().Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}
	f.clock.Add(10)
	recentTime := f.clock.Load()
	var ids []string
	for i := range 51 {
		f.clock.Add(61)
		ids = append(ids, f.terminalMatch(true, i%2))
	}
	mux := http.NewServeMux()
	if err := f.s.RegisterAdminRoutes(adminRegistrar{mux}); err != nil {
		t.Fatal(err)
	}
	read := func(query string) ([]byte, historyexport.Summary) {
		t.Helper()
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/admin/api/games/likes/history/download"+query, nil).WithContext(adminContext())
		mux.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("download: %d %s", w.Code, w.Body.String())
		}
		z, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
		if err != nil {
			t.Fatal(err)
		}
		var records []byte
		var summary historyexport.Summary
		for _, f := range z.File {
			input, err := f.Open()
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(input)
			input.Close()
			if err != nil {
				t.Fatal(err)
			}
			if f.Name == "records.ndjson" {
				records = body
			} else if json.Unmarshal(body, &summary) != nil {
				t.Fatal("manifest")
			}
		}
		return records, summary
	}
	body, summary := read("?dataset=anonymous")
	if !summary.Complete || summary.Records != 104 || !summary.IncludesUndated {
		t.Fatalf("summary=%+v", summary)
	}
	for _, private := range append(ids, oldID, "user_id", "display_name", "general_paid", "game_paid", "operation_id", "started_at", "terminal_at") {
		if bytes.Contains(body, []byte(private)) {
			t.Fatalf("anonymous data contains %q", private)
		}
	}
	var previous string
	for _, line := range bytes.Split(bytes.TrimSpace(body), []byte{'\n'}) {
		var item struct{ Kind, MatchRef string }
		var wire map[string]json.RawMessage
		if json.Unmarshal(line, &wire) != nil {
			t.Fatal("record")
		}
		json.Unmarshal(wire["kind"], &item.Kind)
		json.Unmarshal(wire["match_ref"], &item.MatchRef)
		if item.Kind == "match" {
			previous = item.MatchRef
		} else if item.MatchRef != previous {
			t.Fatal("cross-page round lost its anonymous match")
		}
		if !strings.HasPrefix(item.MatchRef, "dah_") {
			t.Fatal("original reference")
		}
	}
	_, summary = read("?dataset=anonymous&from=" + strconv.FormatInt(recentTime, 10))
	if summary.Records != 102 || summary.IncludesUndated {
		t.Fatalf("dated export=%+v", summary)
	}
	_, summary = read("?dataset=anonymous&to=" + strconv.FormatInt(recentTime-1, 10))
	if summary.Records != 0 {
		t.Fatalf("out-of-range export=%+v", summary)
	}
	body, summary = read("?dataset=recent")
	if summary.Records != 102 || !bytes.Contains(body, []byte(ids[0])) || !bytes.Contains(body, []byte("user_id")) {
		t.Fatal("recent projection changed")
	}
	for _, query := range []string{"?dataset=other", "?dataset=recent&from=2&to=1", "?dataset=recent&limit=1", "?dataset=anonymous&cursor=x"} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", "/admin/api/games/likes/history/download"+query, nil).WithContext(adminContext()))
		if w.Code != 400 {
			t.Fatalf("query=%s status=%d", query, w.Code)
		}
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/admin/api/games/likes/history/download?dataset=anonymous", nil))
	if w.Code != 403 {
		t.Fatalf("unauthorized download=%d", w.Code)
	}
}
