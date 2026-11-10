package donation

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/agentplan"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
)

func TestAgentSharedKeyQueueUsesExistingManagementPermission(t *testing.T) {
	e := newDonationTestEnv(t)
	admin := e.seedUser(t, "", nil, true)
	six, five := int64(6), int64(5)
	steward := e.seedUser(t, "queue-manager", &six, false)
	trainee := e.seedUser(t, "queue-scoped-manager", &five, false)
	owner := e.seedUser(t, "queue-owner", nil, false)
	_, physical := e.seedEndpointKey(t, owner, 'u')
	d := approvedResetDonation(t, e, owner, physical)
	op := agentplan.Operation{Kind: "donation.key.update", TargetID: d.ID, Input: json.RawMessage(`{"key_id":"` + d.Keys[0].ID + `","expected_revision":"2","expected_key_limits_revision":"1","concurrency_overflow":"queue","rpm_overflow":"queue","concurrency_queue_limit":6,"rpm_queue_limit":7}`)}
	policy := authz.DefaultStewardPermissions(5)
	policy.Permissions["charity.non_mainstream"] = true
	policy.DonationScope = authz.DonationScopeFull
	policy.Permissions["donations.manage_keys"] = false
	writeDonationPolicy(t, e, trainee, policy)
	ctx := t.Context()
	tx, err := e.store.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.service.AgentApply(ctx, tx, agentplan.Actor{ID: trainee, Role: "level5"}, op, allowAgent); !errors.Is(err, authz.ErrPermissionDenied) {
		t.Fatal("queue behavior bypassed management permission", err)
	}
	tx.Rollback()
	policy.Permissions["donations.manage_keys"] = true
	writeDonationPolicy(t, e, trainee, policy)
	for _, actor := range []agentplan.Actor{{ID: admin, Role: "admin"}, {ID: steward, Role: "level6"}, {ID: trainee, Role: "level5"}} {
		tx, err := e.store.DB().BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		body, err := e.service.AgentApply(ctx, tx, actor, op, allowAgent)
		if err != nil {
			tx.Rollback()
			t.Fatalf("%s queue edit: %v", actor.Role, err)
		}
		var receipt AdminDonation
		if err := json.Unmarshal(body, &receipt); err != nil || len(receipt.Keys) != 1 || receipt.Keys[0].RPMOverflow == nil || *receipt.Keys[0].RPMOverflow != "queue" || *receipt.Keys[0].RPMQueueLimit != 7 {
			tx.Rollback()
			t.Fatalf("%s queue receipt: %s %v", actor.Role, body, err)
		}
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
	}
}
