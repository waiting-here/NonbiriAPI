package likes

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/game/likes/engine"
)

func TestPresentationRetainsPersistentCacheLayerCountsWithoutInventingLegacyState(t *testing.T) {
	persistent := int64(2)
	effect := engine.Status{Key: "B07:原版", BuffID: "B07:原版", Kind: "CACHE", Layers: 3, PersistentLayers: &persistent}
	frame := engine.Frame{Stage: "after"}
	frame.Players[0].Effects = []engine.Status{effect}
	cue := compactFrame(frame)
	if cue.Players[0].Effects[0].PersistentLayers == nil || *cue.Players[0].Effects[0].PersistentLayers != 2 {
		t.Fatal("compact presentation lost persistent layers")
	}
	data, err := json.Marshal(cue)
	if err != nil || !strings.Contains(string(data), `"persistent_layers":2`) {
		t.Fatal(string(data), err)
	}
	legacy := strings.ReplaceAll(string(data), `,"persistent_layers":2`, "")
	var old frameCue
	if json.Unmarshal([]byte(legacy), &old) != nil || old.Players[0].Effects[0].PersistentLayers != nil {
		t.Fatal("legacy summary acquired an invented persistence count")
	}
	frame.Players[0].Effects[0].PersistentLayers = nil
	data, err = json.Marshal(compactFrame(frame))
	if err != nil || strings.Contains(string(data), "persistent_layers") {
		t.Fatal("unknown layer count should remain absent", err)
	}
}
