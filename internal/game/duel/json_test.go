package duel

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDecodeRejectsAmbiguousCommands(t *testing.T) {
	type child struct {
		Card int `json:"card"`
	}
	type command struct {
		Kind   string          `json:"kind"`
		Action child           `json:"action"`
		Pair   [2]int          `json:"pair"`
		Data   json.RawMessage `json:"data"`
	}
	for _, body := range []string{
		`null`, `{"kind":"a","kind":"b"}`, `{"Kind":"a"}`, `{"unknown":1}`,
		`{"action":{"Card":1}}`, `{"action":{"card":1,"card":2}}`, `{"pair":[1]}`,
		`{"pair":[1,2,3]}`, `{"action":{"card":1.5}}`, `{"data":{"x":1,"x":2}}`,
		`{} {}`, `{"kind":"` + string([]byte{0xff}) + `"}`, `{"data":` + strings.Repeat("[", 34) + `0` + strings.Repeat("]", 34) + `}`,
	} {
		var value command
		if Decode([]byte(body), &value) == nil {
			t.Fatalf("accepted %s", body)
		}
	}
	var value command
	if err := Decode([]byte(`{"kind":"bid","action":{"card":13},"pair":[0,1],"data":{"facts":[1,true,null]}}`), &value); err != nil || value.Action.Card != 13 {
		t.Fatal(value, err)
	}
}
