package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/elevation"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle/adapters"
)

func TestAccountExportIncludesRegisteredCompanionGames(t *testing.T) {
	f := newGameWireFixture(t)
	games := []string{"bidding", "likes", "gwent"}
	type section struct {
		Preferences struct {
			Memory     string
			Contribute bool
		}
		Matches []struct {
			Match struct {
				ID, Game, Program, Status string
				Version                   int
			}
			Memory     string
			Contribute bool
			Actions    []json.RawMessage
			Locked     json.RawMessage
		}
		Programs []json.RawMessage
	}
	export := func() map[string]json.RawMessage {
		t.Helper()
		hash := sha256.Sum256([]byte(f.cookies[0].Value))
		token, _, err := f.app.authRuntime.ElevationManager().IssueBound(f.userID, elevation.KindUser, hex.EncodeToString(hash[:]))
		if err != nil {
			t.Fatal(err)
		}
		rec := testApplicationRequest(t, f.app.handler, http.MethodPost, auditUserHost, "/api/account/export", "", f.cookies,
			map[string]string{"Origin": "http://" + auditUserHost, "X-Elevated-Token": token})
		if rec.Code != 200 {
			t.Fatalf("account export HTTP %d", rec.Code)
		}
		var doc map[string]json.RawMessage
		if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
			t.Fatal(err)
		}
		if string(doc["schema_version"]) != "39" {
			t.Fatal("unexpected export version")
		}
		return doc
	}
	readSection := func(doc map[string]json.RawMessage, game string) section {
		t.Helper()
		var v struct {
			Companion section
			History   []json.RawMessage
		}
		if err := json.Unmarshal(doc[game], &v); err != nil {
			t.Fatal(game, err)
		}
		if v.History == nil || v.Companion.Programs == nil || v.Companion.Matches == nil {
			t.Fatalf("%s missing a game collection", game)
		}
		return v.Companion
	}

	empty := export()
	ids := map[string]string{}
	for i, game := range games {
		v := readSection(empty, game)
		if len(v.Matches) != 0 || len(v.Programs) != 0 || v.Preferences.Memory != "none" || v.Preferences.Contribute {
			t.Fatalf("%s unexpected initial companion state", game)
		}
		if _, err := f.store.DB().Exec("UPDATE site_config SET value='1' WHERE key=?", "game_"+game+"_enabled"); err != nil {
			t.Fatal(err)
		}
		path := "/api/games/" + game + "/companion"
		request := func(method, suffix, body string) map[string]any {
			t.Helper()
			key := fmt.Sprintf("%020d%02d", i, len(suffix))
			rec := testApplicationRequest(t, f.app.handler, method, auditUserHost, path+suffix, body, f.cookies,
				map[string]string{"Content-Type": "application/json", "Origin": "http://" + auditUserHost, "Idempotency-Key": key})
			if rec.Code != 200 {
				t.Fatalf("%s %s HTTP %d: %s", game, suffix, rec.Code, rec.Body.String())
			}
			var result map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			return result
		}
		request("PUT", "/preferences", `{"memory":"opponent","contribute":true}`)
		match := request("POST", "/matches", `{"mode":"standard","program":"official","difficulty":1}`)
		ids[game] = match["id"].(string)
		if game == "likes" {
			options := match["space"].(map[string]any)["examples"].([]any)
			action, err := json.Marshal(map[string]any{"decision_id": match["decision_id"], "action": options[0]})
			if err != nil {
				t.Fatal(err)
			}
			request("POST", "/matches/"+ids[game]+"/actions", string(action))
		}
		if game != "gwent" {
			request("POST", "/matches/"+ids[game]+"/abandon", "{}")
		}
	}
	doc := export()
	for _, game := range games {
		v := readSection(doc, game)
		if v.Preferences.Memory != "opponent" || !v.Preferences.Contribute || len(v.Matches) != 1 ||
			v.Matches[0].Match.ID != ids[game] || v.Matches[0].Match.Game != game || v.Matches[0].Match.Program != "official" ||
			v.Matches[0].Match.Version != 0 || v.Matches[0].Memory != "opponent" || !v.Matches[0].Contribute {
			t.Fatalf("%s companion preferences and game not preserved", game)
		}
		expected := "abandoned"
		if game == "gwent" {
			expected = "active"
		}
		if v.Matches[0].Match.Status != expected {
			t.Fatalf("%s exported the wrong match status", game)
		}
		if game == "likes" && len(v.Matches[0].Actions) == 0 && string(v.Matches[0].Locked) == "null" {
			t.Fatal("missing the player's submitted loadout")
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(doc[game], &fields); err != nil {
			t.Fatal(err)
		}
		for _, hidden := range []string{`"chance"`, `"program_id"`, `"user_id"`} {
			if strings.Contains(string(fields["companion"]), hidden) {
				t.Fatalf("%s exposed %s", game, hidden)
			}
		}
	}

	tx, err := f.store.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, game := range games {
		out, _, err := adapters.NewRegisteredDuel(f.app.homeGames, game).ExportDuel(t.Context(), tx,
			lifecycle.ExportRequest{UserID: f.adminID, DecisionNow: f.now, Limit: 100})
		if err != nil {
			t.Fatal(err)
		}
		var other section
		if err := json.Unmarshal(out.Companion, &other); err != nil || len(other.Matches) != 0 || other.Preferences.Memory != "none" {
			t.Fatalf("%s exported another participant's matches or preferences", game)
		}
	}
}
