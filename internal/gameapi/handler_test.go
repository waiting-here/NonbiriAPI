package gameapi

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStrictGameJSONRejectsDuplicateTrailingAndOversize(t *testing.T) {
	for _, body := range []string{
		`{"bait":"worm","bait":"lure"}`,
		`{"bait":"worm"}{"bait":"worm"}`,
		`{"bait":"worm","extra": [1,2,3]}` + strings.Repeat(" ", maxGameJSONBodyBytes),
	} {
		r := httptest.NewRequest("POST", "/api/games/fishing/rounds", strings.NewReader(body))
		if _, err := decodeStrictObject(r); err == nil {
			t.Fatalf("strict decoder accepted malformed body length=%d", len(body))
		}
	}
}

func TestStrictGameJSONBoundsDepthAndFields(t *testing.T) {
	deep := strings.Repeat("[", maxGameJSONDepth+1) + "0" + strings.Repeat("]", maxGameJSONDepth+1)
	if _, err := decodeStrictObject(httptest.NewRequest("POST", "/", strings.NewReader(deep))); err == nil {
		t.Fatal("strict decoder accepted over-deep value")
	}
	var fields strings.Builder
	fields.WriteString(`{"bait":"worm"`)
	for i := 0; i <= maxGameJSONFields; i++ {
		fmt.Fprintf(&fields, `,"x%d":0`, i)
	}
	fields.WriteByte('}')
	if _, err := decodeStrictObject(httptest.NewRequest("POST", "/", strings.NewReader(fields.String()))); err == nil {
		t.Fatal("strict decoder accepted over-budget fields")
	}
}

func TestGamesConfigPatchRejectsUnknownNullAndCanonicalizesAmounts(t *testing.T) {
	if _, err := parseGamesConfigPatch(map[string]json.RawMessage{"unknown": json.RawMessage(`true`)}); err == nil {
		t.Fatal("unknown top-level config field accepted")
	}
	if _, err := parseGamesConfigPatch(map[string]json.RawMessage{"master_enabled": json.RawMessage(`null`)}); err == nil {
		t.Fatal("null switch accepted")
	}
	changes, err := parseGamesConfigPatch(map[string]json.RawMessage{
		"fishing": json.RawMessage(`{"bait_prices":{"worm":"2500000"},"rtp_percent":{"standard":90}}`),
	})
	if err != nil || changes["game_fishing_bait_worm_price_milli"] != "2500000" || changes["game_fishing_rtp"] != "90" {
		t.Fatalf("valid partial patch = %#v, err=%v", changes, err)
	}
}

func TestLeaderboardAvatarAllowlistAndAnonymousShape(t *testing.T) {
	if got := safeLeaderboardAvatar("123", "hash", ""); got != "https://cdn.discordapp.com/avatars/123/hash.png?size=64" {
		t.Fatalf("global avatar = %q", got)
	}
	for _, raw := range []string{
		"https://evil.example/avatars/123/hash.png?size=64",
		"https://cdn.discordapp.com/guilds/1/users/999/avatars/hash.gif?size=64",
		"https://cdn.discordapp.com/guilds/1/users/123/avatars/hash.webp?size=64",
	} {
		if got := safeLeaderboardAvatar("123", "", raw); got != "" {
			t.Fatalf("unsafe avatar accepted %q -> %q", raw, got)
		}
	}
	if got := safeLeaderboardAvatar("123", "global", "https://cdn.discordapp.com/guilds/1/users/123/avatars/a_anim.gif?size=64"); got != "https://cdn.discordapp.com/guilds/1/users/123/avatars/a_anim.png?size=64" {
		t.Fatalf("animated guild avatar = %q", got)
	}
	if got := safeLeaderboardAvatar("123", "a_global", ""); got != "https://cdn.discordapp.com/avatars/123/a_global.png?size=64" {
		t.Fatalf("animated global avatar = %q", got)
	}
	entry := leaderboardEntryResponse{Rank: 2, IsMe: true}
	if entry.DisplayName != "" || entry.AvatarURL != "" || entry.Level4Badge {
		t.Fatal("anonymous projection carried identity fields")
	}
}

func TestStrictBoardQueryRejectsMalformedAndUnknownParameters(t *testing.T) {
	tests := []struct {
		target string
		board  string
		valid  bool
	}{
		{target: "/leaderboard", board: "single", valid: true},
		{target: "/leaderboard?board=single", board: "single", valid: true},
		{target: "/leaderboard?board=total", board: "total", valid: true},
		{target: "/leaderboard?%zz", valid: false},
		{target: "/leaderboard?board=%zz", valid: false},
		{target: "/leaderboard?board=single&board=total", valid: false},
		{target: "/leaderboard?board=single&extra=1", valid: false},
		{target: "/leaderboard?extra=1", valid: false},
		{target: "/leaderboard?board=", valid: false},
	}
	for _, test := range tests {
		r := httptest.NewRequest("GET", test.target, nil)
		board, valid := strictBoardQuery(r)
		if valid != test.valid || string(board) != test.board {
			t.Errorf("strictBoardQuery(%q) = (%q, %v), want (%q, %v)", test.target, board, valid, test.board, test.valid)
		}
	}
}
