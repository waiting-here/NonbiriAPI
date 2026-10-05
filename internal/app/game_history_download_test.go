package app

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/game/historyexport"
)

func TestGameHistoryDownloadRequiresAdminSession(t *testing.T) {
	f := newDuelWireFixture(t)
	for _, game := range []string{"bidding", "likes", "blackjack"} {
		t.Run(game, func(t *testing.T) {
			path := "/admin/api/games/" + game + "/history/download?dataset=anonymous"
			for _, cookies := range [][]*http.Cookie{nil, f.cookies} {
				r := testApplicationRequest(t, f.app.handler, "GET", auditAdminHost, path, "", cookies, nil)
				if r.Code != http.StatusUnauthorized {
					t.Fatalf("non-admin download status: %d", r.Code)
				}
			}
			r := f.admin("GET", path, nil, http.StatusOK)
			if r.Header().Get("Content-Type") != "application/zip" || r.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("download headers: %v", r.Header())
			}
			archive, err := zip.NewReader(bytes.NewReader(r.Body.Bytes()), int64(r.Body.Len()))
			if err != nil {
				t.Fatal(err)
			}
			manifest, err := archive.Open("manifest.json")
			if err != nil {
				t.Fatal(err)
			}
			defer manifest.Close()
			var summary historyexport.Summary
			if err := json.NewDecoder(manifest).Decode(&summary); err != nil || !summary.Complete || !summary.IncludesUndated || summary.Dataset != "anonymous" {
				t.Fatalf("incomplete download: %+v, %v", summary, err)
			}
		})
	}
}
