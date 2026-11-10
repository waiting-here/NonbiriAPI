package riskaudit

import (
	"context"
	"encoding/json"
	"testing"
)

func TestSharedIPScanExposesItsFrozenIdentityThreshold(t *testing.T) {
	f := newAuditFixture(t)
	ctx := context.Background()
	actor := Actor{Admin: true, UserID: f.admin}
	for range 2 {
		f.source(f.user(1), "self", "192.0.2.44", "direct_peer", "Example/1", "model", "success", 0)
	}
	config := DefaultConfig()
	config.SharedIPUsers = 5
	config, err := f.repository.UpdateConfig(ctx, actor, config)
	if err != nil {
		t.Fatal(err)
	}
	old, err := f.repository.CreateScan(ctx, actor, ScanInput{RequestToken: "shared_ip_threshold_old", ScanKind: "shared_ips", LookbackHours: 24})
	if err != nil {
		t.Fatal(err)
	}
	config.SharedIPUsers = 2
	if _, err = f.repository.UpdateConfig(ctx, actor, config); err != nil {
		t.Fatal(err)
	}
	current, err := f.repository.CreateScan(ctx, actor, ScanInput{RequestToken: "shared_ip_threshold_new", ScanKind: "shared_ips", LookbackHours: 24})
	if err != nil {
		t.Fatal(err)
	}
	check := func(scan ClientScan, want int) {
		t.Helper()
		encoded, err := json.Marshal(taskScanView(scan))
		if err != nil {
			t.Fatal(err)
		}
		var response struct {
			Threshold int `json:"shared_ip_users"`
		}
		if err = json.Unmarshal(encoded, &response); err != nil || response.Threshold != want {
			t.Fatalf("frozen threshold: %s, want %d; %v", encoded, want, err)
		}
	}
	check(old, 5)
	check(current, 2)
	finishAggregateScan(t, f.repository)
	for _, test := range []struct {
		id        string
		threshold int
		matched   string
	}{{old.ID, 5, "0"}, {current.ID, 2, "1"}} {
		scan, err := f.repository.GetScan(ctx, actor, test.id)
		if err != nil {
			t.Fatal(err)
		}
		check(scan, test.threshold)
		result, err := f.repository.TaskResults(ctx, actor, test.id, 1, 20)
		if err != nil || result.TotalItems != test.matched {
			t.Fatal(result, err)
		}
		check(result.Scan, test.threshold)
	}
	recent, err := f.repository.RecentTasks(ctx, actor)
	if err != nil || len(recent) != 2 {
		t.Fatal(recent, err)
	}
	for _, scan := range recent {
		if scan.ID == old.ID {
			check(scan, 5)
		} else {
			check(scan, 2)
		}
	}
}

func TestSharedIPsApplyCallKindToThresholdAndAssociations(t *testing.T) {
	f := newAuditFixture(t)
	ctx := context.Background()
	actor := Actor{Admin: true, UserID: f.admin}
	first, second, third := f.user(1), f.user(1), f.user(1)
	seed := func(user int64, kind, ip string) {
		f.source(user, kind, ip, "trusted_forwarded", "Example/1", "model", "success", 0)
	}
	seed(first, "self", "192.0.2.44")
	seed(second, "charity", "192.0.2.44")
	seed(first, "self", "192.0.2.45")
	seed(second, "self", "192.0.2.45")
	seed(third, "charity", "192.0.2.45")
	seed(first, "unclassified", "192.0.2.46")
	seed(second, "unclassified", "192.0.2.46")
	config := DefaultConfig()
	config.SharedIPUsers = 2
	if _, err := f.repository.UpdateConfig(ctx, actor, config); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		kind string
		ips  []string
	}{{"", []string{"192.0.2.44", "192.0.2.45", "192.0.2.46"}}, {"total", []string{"192.0.2.44", "192.0.2.45", "192.0.2.46"}}, {"self", []string{"192.0.2.45"}}, {"charity", nil}, {"unclassified", []string{"192.0.2.46"}}} {
		t.Run(test.kind, func(t *testing.T) {
			page, err := f.repository.SharedIPs(ctx, actor, Window{Kind: test.kind}, "")
			if err != nil || len(page.Items) != len(test.ips) {
				t.Fatal(page, err)
			}
			for index, item := range page.Items {
				if item.IP != test.ips[index] {
					t.Fatal(item)
				}
				if test.kind == "" || test.kind == "total" {
					continue
				}
				if item.Users != 2 || item.Requests != 2 || len(item.Associations) != 2 {
					t.Fatal(item)
				}
				for _, association := range item.Associations {
					if association.Kind != test.kind {
						t.Fatal("association includes an excluded call kind", association)
					}
				}
			}
		})
	}
}
