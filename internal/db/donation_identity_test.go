package db

import "testing"

func TestDonationCreditDeletionKeepsFinancialFactsAndIdentityGuards(t *testing.T) {
	for _, kind := range []string{"gacha_theme_reward", "lake_collection_reward"} {
		t.Run(kind, func(t *testing.T) {
			database := openGenerationTwoConstraintFixture(t)
			user := hostileInsertUser(t, database, "reward-owner", 0, 0)
			id := hostileOID("op_")
			insert := "INSERT INTO credit_operations(id,ledger_seq,kind,source_type,source_id,source_seq,actor_user_id,donation_credit_user_id,donation_credit_delta_sign,donation_credit_delta_mag,donation_credit_after,created_at) VALUES(?,1,?,'operation',?,zeroblob(16),?,?,1,x'0000000000000000000000000000C350',x'0000000000000000000000000000C350',1)"
			// New rewards require a live recipient. De-identification is only a
			// consequence of deleting that recipient, never a mutable assignment.
			hostileMustFail(t, database, insert, id, kind, id, user, nil)
			hostileMustExec(t, database, insert, id, kind, id, user, user)
			hostileMustFail(t, database, "UPDATE credit_operations SET donation_credit_user_id=NULL WHERE id=?", id)
			hostileMustExec(t, database, "DELETE FROM users WHERE id=?", user)
			var anonymous bool
			var delta, after []byte
			if err := database.QueryRow("SELECT actor_user_id IS NULL AND donation_credit_user_id IS NULL,donation_credit_delta_mag,donation_credit_after FROM credit_operations WHERE id=?", id).Scan(&anonymous, &delta, &after); err != nil {
				t.Fatal(err)
			}
			amount, err := DecodeU128(delta)
			balance, balanceErr := DecodeU128(after)
			if !anonymous || err != nil || balanceErr != nil || amount.Decimal() != "50000" || balance.Decimal() != "50000" {
				t.Fatal(anonymous, amount, balance, err, balanceErr)
			}
			hostileMustFail(t, database, "UPDATE credit_operations SET donation_credit_delta_mag=zeroblob(16) WHERE id=?", id)
		})
	}
}
