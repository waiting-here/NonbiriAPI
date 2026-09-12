package charity

import (
	"context"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func TestKnownUsageChargesBeyondReservation(t *testing.T) {
	for _, route := range []claim.RouteKind{claim.RouteCharityChat, claim.RouteCharityEmbeddings} {
		t.Run(string(route), func(t *testing.T) {
			e := newCharityTestEnv(t)
			rail := e.billingRail(t)
			ctx := context.Background()
			tx := beginTestTx(t, e.store.DB())
			defer tx.Rollback()
			caller, err := ledger.UserAccount(ctx, tx, e.callerID)
			if err != nil {
				t.Fatal(err)
			}
			external, err := ledger.CodedAccount(ctx, tx, "external")
			if err != nil {
				t.Fatal(err)
			}
			plan, err := ledger.NewAdminUserAdjustment(ledger.Meta{OperationID: mustOpaqueID(t, "op_"), ActorUserID: e.callerID, CreatedAt: charityTestNow}, caller.ID, external.ID, ledger.AmountFromMilli(-9995), 0, ledger.Amount{}, "leave only the reservation")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ledger.Apply(ctx, tx, plan); err != nil {
				t.Fatal(err)
			}
			commitTestTx(t, tx)

			request := e.billingRequest(t, rail, e.tokenModel, 5, 1, route)
			e.billingBalance(t, e.callerID, 0)
			handle := e.billingDispatch(t, rail, request.ID, 1)
			if err := rail.MarkResponseStarted(ctx, handle); err != nil {
				t.Fatal(err)
			}
			if _, err := rail.CompleteAttempt(ctx, handle, claim.AttemptOutcome{Kind: claim.ResultResponse, UpstreamStatus: 200, ProtocolSuccess: true, ResponseStarted: true, Usage: connectorcontract.Usage{Present: true, UncachedInputTokens: 2}}); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if _, err := rail.CompleteRequest(ctx, claim.CompleteRequestInput{RequestID: request.ID, Caller: claim.CallerResult{Class: claim.ResultSuccess, Status: 200}, Disposition: claim.AccountingCommit, ActualChargeMilli: 5}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := rail.RecoverNonterminal(ctx, 100); err != nil {
				t.Fatal(err)
			}
			e.billingBalance(t, e.callerID, -2)
			e.billingBalance(t, e.donorID, 10004)

			var original, charge, price, reward, tokens, calls int64
			if err := e.store.DB().QueryRow(`SELECT cr.original_charge_milli,cr.user_charge_milli,u.price_actual_milli,c.donor_reward_actual_milli,u.tokens_actual,u.calls_actual FROM charity_reservations cr JOIN dispatch_claims c ON c.logical_request_id=cr.logical_request_id JOIN donation_usage_reservations u ON u.claim_id=c.id WHERE cr.logical_request_id=?`, request.ID).Scan(&original, &charge, &price, &reward, &tokens, &calls); err != nil {
				t.Fatal(err)
			}
			if original != 8 || charge != 7 || price != 8 || reward != 4 || tokens != 2 || calls != 1 {
				t.Fatalf("original/charge/price/reward/tokens/calls=%d/%d/%d/%d/%d/%d", original, charge, price, reward, tokens, calls)
			}
			var usedPrice, reservedPrice, usedCalls, reservedCalls, usedTokens, reservedTokens []byte
			if err := e.store.DB().QueryRow(`SELECT price_used_mag,price_reserved_mag,calls_used,calls_reserved,tokens_used,tokens_reserved FROM donation_keys WHERE id=?`, e.donationKey).Scan(&usedPrice, &reservedPrice, &usedCalls, &reservedCalls, &usedTokens, &reservedTokens); err != nil {
				t.Fatal(err)
			}
			assertU128(t, usedPrice, 8, "used price")
			assertU128(t, usedCalls, 1, "used calls")
			assertU128(t, usedTokens, 2, "used tokens")
			assertU128(t, reservedPrice, 0, "reserved price")
			assertU128(t, reservedCalls, 0, "reserved calls")
			assertU128(t, reservedTokens, 0, "reserved tokens")

			rows, err := e.store.DB().Query(`SELECT a.kind,COALESCE(a.code,''),e.delta_sign,e.delta_mag FROM credit_entries e JOIN credit_operations o ON o.id=e.operation_id JOIN credit_accounts a ON a.id=e.account_id WHERE o.kind='charity_settle' AND o.source_id=?`, request.ID)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			want := map[string]string{"user/": "-2", "platform/charity_reserve": "-5", "platform/platform": "7"}
			for rows.Next() {
				var kind, code string
				var sign int
				var mag []byte
				if err := rows.Scan(&kind, &code, &sign, &mag); err != nil {
					t.Fatal(err)
				}
				value, err := db.NewSM128(sign, mag)
				key := kind + "/" + code
				if err != nil || value.Decimal() != want[key] {
					t.Fatalf("settlement entry %s=%s want=%s err=%v", key, value.Decimal(), want[key], err)
				}
				delete(want, key)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			if len(want) != 0 {
				t.Fatalf("missing settlement entries: %v", want)
			}
		})
	}
}

func TestMixedUnknownUsageKeepsReservationCap(t *testing.T) {
	e := newCharityTestEnv(t)
	rail := e.billingRail(t)
	request := e.billingRequest(t, rail, e.tokenModel, 5, 2)
	for seq := 1; seq <= 2; seq++ {
		handle := e.billingDispatch(t, rail, request.ID, seq)
		if err := rail.MarkResponseStarted(context.Background(), handle); err != nil {
			t.Fatal(err)
		}
		usage := connectorcontract.Usage{}
		if seq == 1 {
			usage = connectorcontract.Usage{Present: true, UncachedInputTokens: 2}
		}
		if _, err := rail.CompleteAttempt(context.Background(), handle, claim.AttemptOutcome{Kind: claim.ResultResponse, UpstreamStatus: 200, ProtocolSuccess: true, ResponseStarted: true, Usage: usage}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := rail.CompleteRequest(context.Background(), claim.CompleteRequestInput{RequestID: request.ID, Caller: claim.CallerResult{Class: claim.ResultSuccess, Status: 200}, Disposition: claim.AccountingCommit}); err != nil {
		t.Fatal(err)
	}
	e.billingBalance(t, e.callerID, 9995)
	e.billingBalance(t, e.donorID, 10004)
	var original, charge, unknown int
	if err := e.store.DB().QueryRow(`SELECT original_charge_milli,user_charge_milli,usage_unknown FROM charity_reservations WHERE logical_request_id=?`, request.ID).Scan(&original, &charge, &unknown); err != nil {
		t.Fatal(err)
	}
	if original != 8 || charge != 5 || unknown != 1 {
		t.Fatalf("mixed usage original/charge/unknown=%d/%d/%d", original, charge, unknown)
	}
}
