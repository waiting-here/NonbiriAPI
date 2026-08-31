package activities

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"testing"
)

func TestActivitiesSnapshotFixtureMatchesClosedDTO(t *testing.T) {
	data, err := os.ReadFile("testdata/activities_snapshot.json")
	if err != nil {
		t.Fatal(err)
	}
	var snapshot ActivitiesSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 3 || fields["master"] == nil || fields["welfare"] == nil || fields["thursday"] == nil {
		t.Fatalf("snapshot fields=%v", fields)
	}
}

func TestAdminThursdayStateFixtureMatchesClosedDTO(t *testing.T) {
	data, err := os.ReadFile("testdata/admin_thursday_state.json")
	if err != nil {
		t.Fatal(err)
	}
	var state AdminThursdayState
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		t.Fatal(err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		t.Fatalf("fixture trailing JSON: %v", err)
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, bytes.TrimSpace(data)) {
		t.Fatalf("fixture is not the exact closed DTO\n got: %s\nwant: %s", encoded, data)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &top); err != nil {
		t.Fatal(err)
	}
	if len(top) != 1 || top["period"] == nil {
		t.Fatalf("admin Thursday fields=%v", top)
	}
	var period map[string]json.RawMessage
	if err := json.Unmarshal(top["period"], &period); err != nil {
		t.Fatal(err)
	}
	if len(period) != 15 || period["settlement"] == nil || period["terminal_at"] == nil {
		t.Fatalf("period fields=%v", period)
	}
}
