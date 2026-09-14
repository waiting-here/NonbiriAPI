package engine

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"sort"
	"testing"
)

type differentialCase struct {
	Name    string `json:"name"`
	Mode    string `json:"mode"`
	Seed    uint32 `json:"seed"`
	Initial State  `json:"initial"`
	Steps   []struct {
		Plans    [2]Plan `json:"plans"`
		Resolved State   `json:"resolved"`
		Ready    *State  `json:"ready"`
		Events   []struct {
			Kind string         `json:"kind"`
			Seat *int           `json:"seat"`
			Data map[string]any `json:"data"`
		} `json:"events"`
		Draws []struct {
			CandidateCount int `json:"candidate_count"`
			Index          int `json:"index"`
		} `json:"draws"`
	} `json:"steps"`
}

func stateFacts(s State) State { s.EventSeq = 0; s.DrawSeq = 0; return s }

// Compare every normalized state field and every data field present in the
// reference events. Presentation-only fields have their own behavioral tests.
func TestDifferentialCatalogVariantsAndRounds(t *testing.T) {
	compressed, err := os.ReadFile("testdata/reference.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	body, err := io.ReadAll(io.LimitReader(reader, 32<<20))
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []differentialCase `json:"cases"`
	}
	if err := json.Unmarshal(body, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Cases) < 264 {
		t.Fatal("incomplete variant matrix")
	}
	for _, item := range fixture.Cases {
		t.Run(item.Name, func(t *testing.T) {
			e, err := New(item.Mode)
			if err != nil {
				t.Fatal(err)
			}
			s := item.Initial
			seed := item.Seed
			pick := func(count int) (int, error) {
				seed ^= seed << 13
				seed ^= seed >> 17
				seed ^= seed << 5
				return int(seed % uint32(count)), nil
			}
			for index, step := range item.Steps {
				next, record, err := e.Resolve(s, step.Plans, pick)
				if err != nil {
					t.Fatalf("round %d: %v", index+1, err)
				}
				assertJSONEqual(t, fmt.Sprintf("round[%d].resolved", index+1), stateFacts(step.Resolved), stateFacts(next))
				actualEvents := []Event{}
				for _, event := range record.Events {
					if event.Kind != "skill-cancelled" {
						actualEvents = append(actualEvents, event)
					}
				}
				if len(actualEvents) != len(step.Events) {
					t.Fatalf("round %d event count: want %d, got %d: %+v", index+1, len(step.Events), len(actualEvents), actualEvents)
				}
				for eventIndex, want := range step.Events {
					got := actualEvents[eventIndex]
					where := fmt.Sprintf("round[%d].events[%d]", index+1, eventIndex)
					if want.Kind != got.Kind || !reflect.DeepEqual(want.Seat, got.Seat) {
						t.Fatalf("%s identity: want %s/%v got %s/%v", where, want.Kind, want.Seat, got.Kind, got.Seat)
					}
					if want.Data != nil {
						selected := map[string]any{}
						for key := range want.Data {
							selected[key] = got.Data[key]
						}
						assertJSONEqual(t, where+".data", want.Data, selected)
					}
				}
				if len(record.Draws) != len(step.Draws) {
					t.Fatalf("round %d random count differs", index+1)
				}
				for i, want := range step.Draws {
					got := record.Draws[i]
					if got.CandidateCount != want.CandidateCount || got.Index != want.Index {
						t.Fatalf("round %d random draw %d differs", index+1, i)
					}
				}
				if step.Ready != nil {
					next, _, err = e.BeginNextRound(next)
					if err != nil {
						t.Fatal(err)
					}
					assertJSONEqual(t, fmt.Sprintf("round[%d].ready", index+1), stateFacts(*step.Ready), stateFacts(next))
				}
				s = next
			}
		})
	}
}

func assertJSONEqual(t *testing.T, path string, want, got any) {
	t.Helper()
	normalize := func(value any) any {
		body, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var decoded any
		if err := json.Unmarshal(body, &decoded); err != nil {
			t.Fatal(err)
		}
		return decoded
	}
	if diff := firstDifference(path, normalize(want), normalize(got)); diff != "" {
		t.Fatal(diff)
	}
}
func firstDifference(path string, want, got any) string {
	if reflect.DeepEqual(want, got) {
		return ""
	}
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			break
		}
		keys := []string{}
		for key := range w {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			value, exists := g[key]
			if !exists {
				return path + "." + key + ": missing"
			}
			if diff := firstDifference(path+"."+key, w[key], value); diff != "" {
				return diff
			}
		}
		if len(w) != len(g) {
			return fmt.Sprintf("%s: object fields differ", path)
		}
	case []any:
		g, ok := got.([]any)
		if !ok {
			break
		}
		if len(w) != len(g) {
			return fmt.Sprintf("%s: length want %d, got %d", path, len(w), len(g))
		}
		for i := range w {
			if diff := firstDifference(fmt.Sprintf("%s[%d]", path, i), w[i], g[i]); diff != "" {
				return diff
			}
		}
	}
	return fmt.Sprintf("%s: want %v, got %v", path, want, got)
}
