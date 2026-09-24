package charity

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

func TestSelfDonationRequiresCurrentLevelSixAndKeepsReward(t *testing.T) {
	for _, level := range []int{1, 5, 6} {
		t.Run(fmt.Sprint(level), func(t *testing.T) {
			e := newCharityTestEnv(t)
			rail := e.billingRail(t)
			e.callerID = e.donorID
			if _, err := e.store.DB().Exec(`UPDATE users SET level=? WHERE id=?`, level, e.callerID); err != nil {
				t.Fatal(err)
			}
			if _, err := e.store.DB().Exec(`UPDATE charity_model_access SET allowed_level_mask=63`); err != nil {
				t.Fatal(err)
			}
			request := e.billingRequest(t, rail, e.requestModel, 2400, 1)
			handle, err := rail.Claim(context.Background(), e.quotaClaimInput(request.ID, 1))
			if level != 6 {
				if !errors.Is(err, claim.ErrNotFound) {
					t.Fatalf("self donor admitted at level %d: %v", level, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			grant, err := rail.TakeForDispatch(context.Background(), handle)
			if err != nil {
				t.Fatal(err)
			}
			grant.Clear()
			if err := rail.MarkResponseStarted(context.Background(), handle); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if _, err := rail.CompleteAttempt(context.Background(), handle, claim.AttemptOutcome{Kind: claim.ResultResponse, UpstreamStatus: 200, ProtocolSuccess: true, ResponseStarted: true, Usage: contract.Usage{Present: true}}); err != nil {
					t.Fatal(err)
				}
				if _, err := rail.CompleteRequest(context.Background(), claim.CompleteRequestInput{RequestID: request.ID, Caller: claim.CallerResult{Class: claim.ResultSuccess, Status: 200}, Disposition: claim.AccountingCommit, ActualChargeMilli: 2400}); err != nil {
					t.Fatal(err)
				}
			}
			e.billingBalance(t, e.callerID, 8850)
			var reward int64
			var credit []byte
			if err := e.store.DB().QueryRow(`SELECT c.donor_reward_actual_milli,u.donation_credit_mag FROM dispatch_claims c JOIN users u ON u.id=c.receiver_user_id WHERE c.id=?`, handle.ClaimID()).Scan(&reward, &credit); err != nil {
				t.Fatal(err)
			}
			if reward != 1250 {
				t.Fatalf("reward=%d", reward)
			}
			assertU128(t, credit, 1250, "self donor credit")
		})
	}
}

func TestSelfDonationRoleLossBeforeDispatch(t *testing.T) {
	e := newCharityTestEnv(t)
	rail := e.billingRail(t)
	e.callerID = e.donorID
	if _, err := e.store.DB().Exec(`UPDATE users SET level=6 WHERE id=?`, e.callerID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.DB().Exec(`UPDATE charity_model_access SET allowed_level_mask=63`); err != nil {
		t.Fatal(err)
	}
	request := e.billingRequest(t, rail, e.requestModel, 2400, 1)
	handle, err := rail.Claim(context.Background(), e.quotaClaimInput(request.ID, 1))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.DB().Exec(`UPDATE users SET level=5 WHERE id=?`, e.callerID); err != nil {
		t.Fatal(err)
	}
	if _, err := rail.TakeForDispatch(context.Background(), handle); !errors.Is(err, claim.ErrNotFound) {
		t.Fatal("revoked exemption was used", err)
	}
}

func TestDonorBanEligibilityAtClaimAndFinalDispatch(t *testing.T) {
	for _, protective := range []bool{false, true} {
		for _, late := range []bool{false, true} {
			t.Run(fmt.Sprintf("protective=%t/late=%t", protective, late), func(t *testing.T) {
				e := newCharityTestEnv(t)
				rail := e.billingRail(t)
				ban := func() {
					kind := ""
					if protective {
						kind = "protective_inactivity"
					}
					if _, err := e.store.DB().Exec(`UPDATE users SET is_banned=1,banned_until=NULL,ban_kind=? WHERE id=?`, kind, e.donorID); err != nil {
						t.Fatal(err)
					}
				}
				if !late {
					ban()
				}
				request := e.billingRequest(t, rail, e.requestModel, 2400, 1)
				handle, err := rail.Claim(context.Background(), e.quotaClaimInput(request.ID, 1))
				if !late && !protective {
					if !errors.Is(err, claim.ErrNotFound) {
						t.Fatal("banned donor admitted", err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if late {
					ban()
				}
				grant, err := rail.TakeForDispatch(context.Background(), handle)
				if protective {
					if err != nil {
						t.Fatal("protective donor refused", err)
					}
					grant.Clear()
				} else if !errors.Is(err, claim.ErrNotFound) {
					t.Fatal("late ban ignored", err)
				}
			})
		}
	}
}
