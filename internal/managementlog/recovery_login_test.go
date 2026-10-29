package managementlog_test

import (
	"testing"

	. "github.com/waiting-here/NonbiriAPI/internal/managementlog"
)

func TestInterruptedDeviceLoginRecoveryKeepsDurableTasksActive(t *testing.T) {
	s := fixture(t)
	pending := recordValue("pending-login", 7200000, 0)
	pending.Outcome, pending.CorrelationKind, pending.CorrelationID = Accepted, "codex_login", "login"
	pending.Items = []Item{{Action: "connect", TargetKind: "codex", Outcome: Accepted}}
	id := insert(t, s, pending)
	durable := recordValue("pending-scan", 7200001, 0)
	durable.Outcome, durable.CorrelationKind, durable.CorrelationID = Accepted, "risk_scan", "scan"
	durableID := insert(t, s, durable)
	for range 2 {
		if err := s.RecoverInterrupted(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Detail(t.Context(), 1, id)
	if err != nil || got.Outcome != Unknown || got.Targets != 2 {
		t.Fatalf("interrupted login: %+v %v", got, err)
	}
	items, err := s.Items(t.Context(), 1, id, 1, 20)
	if err != nil || len(items.Items) != 2 || items.Items[0].Outcome != Accepted || items.Items[1].Code != "process_interrupted" {
		t.Fatal(items, err)
	}
	got, err = s.Detail(t.Context(), 1, durableID)
	if err != nil || got.Outcome != Accepted {
		t.Fatalf("durable task: %+v %v", got, err)
	}
}
