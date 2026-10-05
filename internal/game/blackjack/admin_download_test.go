package blackjack_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/game/blackjack"
	"github.com/waiting-here/NonbiriAPI/internal/game/historyexport"
)

func TestBlackjackZIPIncludesRecentAnonymousTablesAcrossPages(t *testing.T) {
	f := newFixture(t, 1)
	f.exec(`INSERT INTO users(username,is_admin,donation_credit_mag,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,created_at,updated_at) SELECT 'admin',1,donation_credit_mag,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,created_at,updated_at FROM users WHERE id=?`, f.users[0].UserID)
	play := func(start int64) string {
		f.clock.Store(start)
		f.join(0)
		id := f.read(0).Table.ID
		f.clock.Store(start + 5)
		f.read(0)
		f.clock.Store(start + 25)
		f.read(0)
		return id
	}
	oldID := play(120)
	f.clock.Store(146 + 30*24*60*60)
	if _, err := f.s.Retain(f.ctx, f.clock.Load(), 10, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	base := (f.clock.Load()/30 + 2) * 30
	ids := []string{oldID}
	for i := range 12 {
		ids = append(ids, play(base+60*int64(i)))
	}
	for _, dataset := range []string{"anonymous", "recent"} {
		w := httptest.NewRecorder()
		f.s.AdminDownload(w, httptest.NewRequest("GET", "/", nil).WithContext(f.ctx), blackjack.PageInput{Dataset: dataset})
		if w.Code != 200 {
			t.Fatalf("download %d: %s", w.Code, w.Body.String())
		}
		z, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
		if err != nil {
			t.Fatal(err)
		}
		var records []byte
		var summary historyexport.Summary
		for _, file := range z.File {
			input, err := file.Open()
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(input)
			input.Close()
			if err != nil {
				t.Fatal(err)
			}
			if file.Name == "records.ndjson" {
				records = body
			} else if json.Unmarshal(body, &summary) != nil {
				t.Fatal("manifest")
			}
		}
		want := 12
		if dataset == "anonymous" {
			want++
			for _, value := range append(ids, "user_id", "started_at", "terminal_at", "payment", "operations", "emote_at") {
				if bytes.Contains(records, []byte(value)) {
					t.Fatalf("private value %q", value)
				}
			}
		} else if !bytes.Contains(records, []byte(ids[1])) {
			t.Fatal("missing recent identity")
		}
		if summary.Records != want || !summary.Complete || bytes.Count(records, []byte{'\n'}) != want {
			t.Fatalf("incomplete %+v", summary)
		}
	}
}
