package activities

import (
	"context"
	"errors"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/game"
	bidding "github.com/waiting-here/NonbiriAPI/internal/game/bidding/config"
	blackjack "github.com/waiting-here/NonbiriAPI/internal/game/blackjack/config"
	fishing "github.com/waiting-here/NonbiriAPI/internal/game/fishing/config"
	likes "github.com/waiting-here/NonbiriAPI/internal/game/likes/config"
	linklink "github.com/waiting-here/NonbiriAPI/internal/game/linklink/config"
	rps "github.com/waiting-here/NonbiriAPI/internal/game/rps/config"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func TestExportAllSixGamesWithoutTruncatingNewRewards(t *testing.T) {
	f := newActivityFixture(t, 1800000000)
	user, wallet := f.seedUser("all-rewards", false)
	tx, err := f.store.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	external, err := ledger.CodedAccount(context.Background(), tx, "external")
	if err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := tx.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	zero := db.EncodeU128(db.U128{})
	count, total := 0, int64(0)
	for _, module := range []game.ModuleDescriptor{fishing.Descriptor(), linklink.Descriptor(), rps.Descriptor(), bidding.Descriptor(), likes.Descriptor(), blackjack.Descriptor()} {
		for _, task := range module.Onboarding {
			count++
			total += task.RewardMilli
			id := f.operationID()
			amount, _ := db.ParseU128Decimal(ledger.AmountFromMilli(task.RewardMilli).Big().String())
			cumulative, _ := db.ParseU128Decimal(ledger.AmountFromMilli(total).Big().String())
			exec(`INSERT INTO credit_operations(id,ledger_seq,kind,source_type,source_id,source_seq,actor_user_id,donation_credit_delta_sign,donation_credit_delta_mag,created_at) VALUES(?,?,'game_onboarding_reward','operation',?,?,?,0,?,?)`, id, count, id, zero, user, zero, f.clock.Load())
			exec(`INSERT INTO credit_entries(operation_id,line_no,account_id,account_kind_snapshot,asset_type,delta_sign,delta_mag) VALUES(?,0,?,'user','general',1,?)`, id, wallet.ID, db.EncodeU128(amount))
			exec(`INSERT INTO credit_entries(operation_id,line_no,account_id,account_kind_snapshot,asset_type,delta_sign,delta_mag,balance_after_sign,balance_after_mag) VALUES(?,1,?,'external','general',-1,?,-1,?)`, id, external.ID, db.EncodeU128(amount), db.EncodeU128(cumulative))
			exec(`INSERT INTO game_onboarding_completions(user_id,game_key,task_key,award_milli,operation_id,completed_at) VALUES(?,?,?,?,?,?)`, user, module.ID, task.Key, task.RewardMilli, id, f.clock.Load())
		}
	}
	if count != 22 {
		t.Fatal("incomplete reward fixture", count)
	}
	all, err := f.repository.ExportUserTx(context.Background(), tx, user, 10000)
	if err != nil || len(all.GameOnboarding) != 22 {
		t.Fatal("reward export incomplete", len(all.GameOnboarding), err)
	}
	if _, err := f.repository.ExportUserTx(context.Background(), tx, user, 10); !errors.Is(err, ErrResourceLimit) {
		t.Fatal("overflow must fail rather than truncate", err)
	}
}
