package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/rewards"
)

type loadMaintenanceResult struct {
	ExpiredSeeded    int64   `json:"expired_seeded"`
	ExpiredRemaining int64   `json:"expired_remaining"`
	RewardsMatched   int64   `json:"rewards_matched"`
	RewardsSuccess   int64   `json:"rewards_success"`
	SeedMS           float64 `json:"seed_ms"`
	MaintenanceMS    float64 `json:"maintenance_ms"`
	CompletionMS     float64 `json:"completion_ms"`
}

// Add a bounded, already-terminal history backlog during live synthetic work.
// The lifecycle coordinator and ordinary reward worker perform the actual work.
func (f *loadFixture) maintenanceOverlap(ctx context.Context) (out loadMaintenanceResult, err error) {
	started := time.Now()
	defer func() { out.CompletionMS = float64(time.Since(started)) / float64(time.Millisecond) }()
	tx, err := f.store.DB().BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	old := time.Now().Add(-31 * 24 * time.Hour).Unix()
	insert, err := tx.PrepareContext(ctx, `INSERT INTO game_fishing_batches(id,user_id,bait,count,unit_price_milli,entry_total_milli,payout_total_milli,operation_id,request_hash,state,ledger_rows_remaining,next_attempt_at,created_at)
VALUES(?,?,'worm',1,0,0,0,?,zeroblob(32),'reserved',x'00000000000000000000000000000001',?,?)`)
	if err != nil {
		return out, err
	}
	defer insert.Close()
	outcome, err := tx.PrepareContext(ctx, `INSERT INTO game_fishing_outcomes(batch_id,ordinal,species_key,tier,size_cm,payout_milli) VALUES(?,0,'boot','junk',0,0)`)
	if err != nil {
		return out, err
	}
	defer outcome.Close()
	settle, err := tx.PrepareContext(ctx, `UPDATE game_fishing_batches SET state='committed',settled_at=?,revealed_at=?,next_attempt_at=NULL,ledger_rows_remaining=zeroblob(16) WHERE id=?`)
	if err != nil {
		return out, err
	}
	defer settle.Close()
	// Preserve the one-reserved-batch-per-user constraint while seeding history.
	for i := 70000; i <= 71000; i++ {
		id, operation := fmt.Sprintf("fb_%021dA", i), fmt.Sprintf("op_%021dA", i)
		if _, err = insert.ExecContext(ctx, id, f.users[0].id, operation, old, old); err != nil {
			return out, err
		}
		if _, err = outcome.ExecContext(ctx, id); err != nil {
			return out, err
		}
		if _, err = settle.ExecContext(ctx, old, old, id); err != nil {
			return out, err
		}
		out.ExpiredSeeded++
	}
	if err = tx.Commit(); err != nil {
		return out, err
	}
	out.SeedMS = float64(time.Since(started)) / float64(time.Millisecond)

	body := `{"kind":"broadcast","title":"Synthetic delivery","enabled":true,"rewards":[{"asset":"general","quantity":"0.001"}],"conditions":{"op":"all"}}`
	request, err := http.NewRequestWithContext(ctx, "POST", f.server.URL+"/admin/api/rewards/create", strings.NewReader(body))
	if err != nil {
		return out, err
	}
	request.Host = auditAdminHost
	request.Header.Set("Origin", "http://"+auditAdminHost)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "synthetic-overlap-reward-0001")
	request.AddCookie(f.admin)
	response, err := f.client.Do(request)
	if err != nil {
		return out, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return out, fmt.Errorf("broadcast status %d", response.StatusCode)
	}
	var campaign rewards.Campaign
	if err = json.NewDecoder(response.Body).Decode(&campaign); err != nil {
		return out, err
	}
	out.RewardsMatched = campaign.Matched
	if campaign.Matched == 0 || out.ExpiredSeeded != 1001 {
		return out, fmt.Errorf("empty overlap fixture: %+v", out)
	}
	maintenanceStart := time.Now()
	err = f.app.lifecycle.RunMaintenance(ctx)
	out.MaintenanceMS = float64(time.Since(maintenanceStart)) / float64(time.Millisecond)
	if err != nil {
		return out, err
	}
	for {
		if err = f.store.DB().QueryRowContext(ctx, "SELECT count(*) FROM game_fishing_batches WHERE id BETWEEN ? AND ?", fmt.Sprintf("fb_%021dA", 70000), fmt.Sprintf("fb_%021dA", 71000)).Scan(&out.ExpiredRemaining); err != nil {
			return out, err
		}
		var pending int
		if err = f.store.DB().QueryRowContext(ctx, `SELECT COALESCE(sum(status='pending'),0),COALESCE(sum(status='success'),0) FROM reward_claims WHERE campaign_id=?`, campaign.ID).Scan(&pending, &out.RewardsSuccess); err != nil {
			return out, err
		}
		if pending == 0 {
			return out, nil
		}
		select {
		case <-ctx.Done():
			return out, ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
}
