package donationquota

import (
	"context"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func TestBrowseSplitReservationsAndCumulativeLimits(t *testing.T) {
	q := newQuotaDB(t)
	if _, err := q.Exec(`UPDATE donation_keys SET token_reserve=99,input_token_reserve=2,output_token_reserve=3,input_token_limit_mag=? WHERE id=1`, db.EncodeU128(mag(2))); err != nil {
		t.Fatal(err)
	}
	replace(t, q, testNow, rule("tokens", "6"), rule("input_tokens", "2"), rule("output_tokens", "3"))
	check := func(want bool) {
		t.Helper()
		budget, err := ReadTokenBudget(context.Background(), q, 1)
		if err != nil {
			t.Fatal(err)
		}
		cumulative, err := budget.Available()
		if err != nil {
			t.Fatal(err)
		}
		periodic, err := Available(context.Background(), q, 1, testNow, budget.Reservation.Amounts())
		if err != nil {
			t.Fatal(err)
		}
		var sqlResult bool
		if err := q.QueryRow(`SELECT `+AvailabilityPredicate("dk.id", "?", "0", "dk.token_reserve")+` FROM donation_keys dk WHERE id=1`, testNow).Scan(&sqlResult); err != nil {
			t.Fatal(err)
		}
		if (cumulative && periodic) != want || sqlResult != want {
			t.Fatal("SQL and transactional availability differ", cumulative, periodic, sqlResult, want)
		}
		var effective int64
		if err := q.QueryRow(`SELECT ` + EffectiveTokenReserveSQL("dk.id", "dk.token_reserve") + ` FROM donation_keys dk WHERE id=1`).Scan(&effective); err != nil || effective != budget.Reservation.Total {
			t.Fatal(effective, budget.Reservation, err)
		}
	}
	check(true)
	if _, err := q.Exec(`UPDATE donation_keys SET input_tokens_used=? WHERE id=1`, db.EncodeU128(mag(1))); err != nil {
		t.Fatal(err)
	}
	check(false)
	if _, err := q.Exec(`UPDATE donation_keys SET input_token_limit_mag=NULL,output_token_reserve=4 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	check(false)
	if _, err := q.Exec(`UPDATE donation_keys SET output_token_reserve=0,output_token_limit_mag=zeroblob(16) WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	check(false)
}
