package lakenotes

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/lakenotes/rules"
)

func TestHTTPCheckpointPersistenceWithSystemClock(t *testing.T) {
	t.Run("fresh", func(t *testing.T) { testHTTPCheckpointPersistence(t, false) })
	t.Run("returning", func(t *testing.T) { testHTTPCheckpointPersistence(t, true) })
}
func testHTTPCheckpointPersistence(t *testing.T, returning bool) {
	f := newFixture(t)
	f.now.Store(time.Now().UnixNano())
	f.service.now = time.Now
	f.enable(t)
	view := f.profile(t)
	if returning {
		profile := view.Profile
		profile.Records["gold"] = rules.Record{Caught: "1", MaxLength: 15, BestQuality: 0, PerfectCount: "0"}
		raw, err := rules.EncodeProfile(profile)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.database.Exec("UPDATE lake_notes_profiles SET profile=? WHERE user_id=?", raw, f.user); err != nil {
			t.Fatal(err)
		}
		view = f.profile(t)
	}
	mux := http.NewServeMux()
	if err := RegisterRoutes(userRoutes{mux, f}, userRoutes{mux, f}, adminRoutes{mux, f}, f.service); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(mux)
	defer server.Close()
	client := server.Client()
	client.Timeout = 5 * time.Second
	request := func(method, path string, input any, key string) CastResult {
		t.Helper()
		raw, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		if method == "GET" {
			raw = nil
		}
		req, err := http.NewRequest(method, server.URL+path, bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusOK {
			t.Fatalf("%s checkpoint request: status %d, error %s", method, response.StatusCode, body)
		}
		var result CastResult
		if err = json.Unmarshal(body, &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	current := request("POST", baseRoute+"/casts", StartInput{view.Revision}, testKey(950))
	path := baseRoute + "/casts/" + current.Cast.ID
	first := checkpoint(current.Cast, 120)
	ack := request("POST", path+"/checkpoint", first, testKey(951))
	if ack.Cast.AckTick != 120 {
		t.Fatal("first segment not saved", ack.Cast.AckTick)
	}
	replay := request("POST", path+"/checkpoint", first, testKey(951))
	if !reflect.DeepEqual(ack, replay) {
		t.Fatal("duplicate HTTP request changed saved progress")
	}
	read := request("GET", path, nil, "")
	if read.Cast.AckTick != ack.Cast.AckTick || !reflect.DeepEqual(read.Cast.State, ack.Cast.State) {
		t.Fatal("read changed the confirmed checkpoint")
	}
	paused := request("POST", path+"/pause", control(read.Cast), testKey(952))
	if !paused.Cast.Paused {
		t.Fatal("pause was not persisted")
	}
	current = request("POST", path+"/resume", control(paused.Cast), testKey(953))
	if current.Cast.Generation == paused.Cast.Generation || current.Cast.State.Held {
		t.Fatal("resume did not acquire fresh released control")
	}
	// Real elapsed time authorizes the next complete HTTP segment.
	time.Sleep(2 * time.Second)
	current = request("POST", path+"/checkpoint", checkpoint(current.Cast, 120), testKey(960))
	read = request("GET", path, nil, "")
	if read.Cast.AckTick != 240 || !reflect.DeepEqual(read.Cast.State, current.Cast.State) {
		t.Fatal("subsequent acknowledgement was not committed")
	}
	if !returning {
		return
	}
	paused = request("POST", path+"/pause", control(read.Cast), testKey(961))
	// Restore a confirmed cast close to its loss boundary, keeping its encounter,
	// generation, tick, clock and RNG. This exercises terminal persistence through
	// the real HTTP/storage path without waiting for a complete animation.
	restored := paused.Cast.State
	if restored.Phase != "playing" || restored.Fish == nil {
		t.Fatal("expected a hooked fish")
	}
	restored.Progress = 1e-9
	restored.BarY = restored.Snapshot.BarHeight / 2
	if restored.Fish.Y < 0.5 {
		restored.BarY = 1 - restored.Snapshot.BarHeight/2
	}
	restored.BarVelocity = 0
	raw, err := rules.EncodeCast(restored)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.database.Exec("UPDATE lake_notes_casts SET snapshot=? WHERE id=?", raw, paused.Cast.ID); err != nil {
		t.Fatal(err)
	}
	read = request("GET", path, nil, "")
	if read.Cast.State.Progress != restored.Progress || read.Cast.State.Plan != restored.Plan {
		t.Fatal("restored progress changed")
	}
	current = request("POST", path+"/resume", control(read.Cast), testKey(962))
	time.Sleep(2 * time.Second)
	input := checkpoint(current.Cast, 120)
	input.Edges = []Edge{{Tick: input.FromTick, Held: true}}
	current = request("POST", path+"/checkpoint", input, testKey(963))
	if !current.Cast.State.Terminal() || current.Cast.Phase != "failed" || current.Cast.AckTick <= 240 {
		t.Fatalf("restored boundary phase=%s tick=%d progress=%g bar=%g fish=%g prep=%g", current.Cast.Phase, current.Cast.AckTick, current.Cast.State.Progress, current.Cast.State.BarY, current.Cast.State.Fish.Y, current.Cast.State.BitePreparationRemaining)
	}
	again := request("POST", path+"/checkpoint", input, testKey(963))
	if !reflect.DeepEqual(current, again) {
		t.Fatal("terminal retry changed the catch or profile")
	}

}
