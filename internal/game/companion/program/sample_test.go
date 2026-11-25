package program

import (
	"encoding/json"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/game/companion/kit"
)

func TestLikesUploadUsesPublishedSampleRules(t *testing.T) {
	sample, err := kit.Sample("likes")
	if err != nil {
		t.Fatal(err)
	}
	var req struct {
		Space struct{ Examples []json.RawMessage }
	}
	if json.Unmarshal(sample, &req) != nil || len(req.Space.Examples) == 0 || !validLikesSample(sample, req.Space.Examples[0]) {
		t.Fatal("published example failed")
	}
	for _, bad := range []string{`{"kind":"plan","plan":{}}`, `{"kind":"plan","plan":{"purchases":[],"extra":[],"main":{"skillId":"unknown"}}}`} {
		if validLikesSample(sample, json.RawMessage(bad)) {
			t.Fatal("accepted invalid sample", bad)
		}
	}
}
