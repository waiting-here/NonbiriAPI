package official

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/game/companion/historyio"
)

func TestOpeningUsesOnlyOfferedSnapshotAndRemembersIt(t *testing.T) {
	calls := 0
	ctx := historyio.WithReader(t.Context(), func(_ context.Context, op string, args json.RawMessage) (json.RawMessage, error) {
		calls++
		var a struct{ Snapshot string }
		if err := json.Unmarshal(args, &a); err != nil || a.Snapshot != "allowed" {
			t.Fatal("wrong history snapshot", string(args))
		}
		if op == "list" {
			return json.RawMessage(`{"items":[{"record":"1","bytes":250}]}`), nil
		}
		if op != "read" {
			t.Fatalf("unexpected operation %s", op)
		}
		return json.RawMessage(`{"initial":{"human_seat":0},"actions":[{"phase":"setup","actions":[{},{"role":"Gemini","faction":"gemini"}]},{"phase":"bid","actions":[{"card":12},{"card":5}]}],"result":{"winner":1}}`), nil
	})
	req := Request{}
	if hint := openingFromHistory(ctx, req); hint != (opening{}) || calls != 0 {
		t.Fatal(hint, calls)
	}
	req.History = map[string]historySource{"personal": {"allowed", 1}, "global": {"other", 20}}
	hint := openingFromHistory(ctx, req)
	if hint.BidShift <= 0 || hint.Role != "Gemini" || hint.Faction != "gemini" || calls != 2 {
		t.Fatal(hint, calls)
	}
	b, _ := json.Marshal(hint)
	req.Memory = string(b)
	if repeat := openingFromHistory(ctx, req); repeat != hint || calls != 2 {
		t.Fatal(repeat, calls)
	}
}
