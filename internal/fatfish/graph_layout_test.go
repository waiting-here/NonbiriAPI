package fatfish

import (
	"context"
	"errors"
	"testing"
)

func TestGraphLayoutIsIndependentAndReplaysLostReply(t *testing.T) {
	f := newFishFixture(t)
	periodID, nodeID := f.publishOne(t)
	ctx := context.Background()
	before, err := f.s.AdminPeriod(ctx, f.admin, periodID)
	if err != nil {
		t.Fatal(err)
	}
	layout, err := f.s.GraphLayout(ctx, f.admin, periodID)
	if err != nil || layout.Revision != "0" || len(layout.Nodes) != 1 {
		t.Fatal(layout, err)
	}
	var rows int
	if err = f.db.QueryRow("SELECT count(*) FROM fatfish_graph_layouts").Scan(&rows); err != nil || rows != 0 {
		t.Fatal("GET wrote layout", rows, err)
	}
	input := GraphLayoutInput{ExpectedRevision: "0", Nodes: []GraphPosition{{NodeID: nodeID, MapX: 321, MapY: -123}}}
	saved, err := f.s.SaveGraphLayout(ctx, f.admin, periodID, input, fishKey(8100))
	if err != nil || saved.Revision != "1" {
		t.Fatal(saved, err)
	}
	replay, err := f.s.SaveGraphLayout(ctx, f.admin, periodID, input, fishKey(8100))
	if err != nil || replay.Revision != "1" {
		t.Fatal(replay, err)
	}
	after, err := f.s.AdminPeriod(ctx, f.admin, periodID)
	if err != nil || after.Revision != before.Revision || after.Nodes[0].Revision != before.Nodes[0].Revision || after.Nodes[0].MapX != 321 || after.Nodes[0].MapY != -123 {
		t.Fatal(after, err)
	}
	node, err := f.s.AdminNode(ctx, f.admin, periodID, nodeID)
	if err != nil || node.MapX != 321 || node.Condition == nil {
		t.Fatal(node, err)
	}
	var legacyX int
	if err = f.db.QueryRow("SELECT map_x FROM fatfish_nodes WHERE id=?", nodeID).Scan(&legacyX); err != nil || legacyX != before.Nodes[0].MapX {
		t.Fatal("layout changed defaults", legacyX, err)
	}
	if _, err = f.s.SaveGraphLayout(ctx, f.admin, periodID, input, fishKey(8101)); !errors.Is(err, ErrConflict) {
		t.Fatal("stale layout accepted", err)
	}
	if _, err = f.s.GraphLayout(ctx, f.user, periodID); err == nil {
		t.Fatal("user read admin layout")
	}
}

func TestGraphLayoutRejectsStaleNodeCollection(t *testing.T) {
	f := newFishFixture(t)
	periodID, nodeID := f.publishOne(t)
	ctx := context.Background()
	before, err := f.s.AdminPeriod(ctx, f.admin, periodID)
	if err != nil {
		t.Fatal(err)
	}
	node := before.Nodes[0]
	next, err := f.s.SaveNode(ctx, f.admin, periodID, "", NodeInput{Title: "Added node", MapX: 48, MapY: 88, Order: 1, VersionID: node.VersionID, Condition: []byte("{}"), Amounts: *node.Amounts, ExpectedPeriodRevision: before.Revision}, fishKey(8110))
	if err != nil {
		t.Fatal(err)
	}
	stale := GraphLayoutInput{ExpectedRevision: "0", Nodes: []GraphPosition{{NodeID: nodeID, MapX: 3, MapY: 4}}}
	if _, err = f.s.SaveGraphLayout(ctx, f.admin, periodID, stale, fishKey(8111)); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	current, err := f.s.GraphLayout(ctx, f.admin, periodID)
	if err != nil || len(current.Nodes) != 2 || current.Revision != "0" {
		t.Fatal(current, err)
	}
	stale.Nodes = append(stale.Nodes, GraphPosition{NodeID: next.ID, MapX: 9, MapY: 10})
	if _, err = f.s.SaveGraphLayout(ctx, f.admin, periodID, stale, fishKey(8112)); err != nil {
		t.Fatal(err)
	}
	period, err := f.s.AdminPeriod(ctx, f.admin, periodID)
	if err != nil || period.Revision != "4" {
		t.Fatal("layout advanced business revision", period.Revision, err)
	}
	stale.ExpectedRevision = "1"
	stale.Nodes = append(stale.Nodes, stale.Nodes[0])
	if _, err = f.s.SaveGraphLayout(ctx, f.admin, periodID, stale, fishKey(8113)); !errors.Is(err, ErrInvalid) {
		t.Fatal("duplicate accepted", err)
	}
}
