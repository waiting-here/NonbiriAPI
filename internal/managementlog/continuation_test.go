package managementlog_test

import (
	"context"
	"testing"

	. "github.com/waiting-here/NonbiriAPI/internal/managementlog"
)

func TestChunkedResultsRetainEarlierOutcomes(t *testing.T) {
	for _, test := range []struct {
		name   string
		chunks [][]Outcome
		want   Outcome
	}{
		{"single_rejection", [][]Outcome{{Rejected}}, Rejected},
		{"earlier_partial", [][]Outcome{{Partial}, {Succeeded}}, Partial},
		{"all_rejected", [][]Outcome{{Rejected}, {Rejected}, {}}, Rejected},
		{"skipped_and_succeeded", [][]Outcome{{NoChange}, {Succeeded}}, Partial},
		{"unknown_then_failed", [][]Outcome{{Unknown}, {Failed}}, Partial},
		{"empty", [][]Outcome{{}}, NoChange},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := fixture(t)
			meta := recordValue(test.name, 7200000, 0).Metadata
			ctx := WithOperation(context.Background(), meta)
			for i, chunk := range test.chunks {
				var items []Item
				for _, outcome := range chunk {
					items = append(items, Item{Action: "delete", TargetKind: "request", Outcome: outcome})
				}
				tx, err := s.DB.Begin()
				if err != nil {
					t.Fatal(err)
				}
				err = AppendItemsContextTx(ctx, tx, items, i == len(test.chunks)-1)
				if err == nil {
					err = tx.Commit()
				}
				if err != nil {
					tx.Rollback()
					t.Fatal(err)
				}
			}
			page, err := s.Query(ctx, 1, Query{View: "operations", Filter: Filter{EventID: test.name}})
			if err != nil || len(page.Operations) != 1 || page.Operations[0].Outcome != test.want {
				t.Fatalf("outcome: %+v, %v; want %s", page, err, test.want)
			}
			groups, err := s.Query(ctx, 1, Query{View: "groups"})
			if err != nil || groups.Total != 1 || groups.Groups[0].Outcome != test.want {
				t.Fatalf("rollup: %+v, %v", groups, err)
			}
		})
	}
}
