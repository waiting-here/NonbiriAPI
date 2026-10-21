package claim

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
)

func BenchmarkPrepareAccountDeletionHistory(b *testing.B) {
	for _, count := range []int{0, 100, 1_000} {
		for _, claims := range []int{0, 1, 3} {
			if count == 0 && claims != 0 {
				continue
			}
			b.Run(fmt.Sprintf("requests=%d/claims=%d", count, claims), func(b *testing.B) {
				f := newClaimFixture(b)
				f.useLedgerAccounting()
				userID := f.seedUser("history-benchmark", false)
				f.seedLedgerUser(userID, 1_000)
				seedTerminalDeletionHistory(b, f, userID, count, claims)
				otherID := f.seedUser("unrelated-history", false)
				seedTerminalDeletionHistory(b, f, otherID, 3, 1)
				key := f.seedKey(userID, "active-benchmark")
				for _, dispatched := range []bool{false, true} {
					request := f.acceptSelf(userID, 2)
					handle, err := f.service.Claim(context.Background(), ClaimInput{
						RequestID: request.ID, ActorUserID: userID, AttemptSeq: 1, Purpose: PurposeSelf, Candidate: key.candidate,
					})
					if err != nil {
						b.Fatal(err)
					}
					if dispatched {
						dispatch, err := f.service.TakeForDispatch(context.Background(), handle)
						if err != nil {
							b.Fatal(err)
						}
						dispatch.Clear()
					}
				}
				before := deletionSnapshot(b, f.db)
				var expected string
				var expectedIDs map[string]int64
				for _, implementation := range []struct {
					name    string
					prepare func(context.Context, *sql.Tx, int64, int64) error
				}{
					{"individual", f.prepareAccountDeletionIndividually},
					{"batch", f.service.PrepareAccountDeletion},
				} {
					// Check both implementations against exactly the same database,
					// including active ledger work, outside the measured interval.
					tx := f.beginLedgerTx()
					defer tx.Rollback()
					if err := implementation.prepare(context.Background(), tx, userID, 2_000); err != nil {
						b.Fatal(err)
					}
					// Ledger operations generate opaque IDs directly from crypto/rand.
					// Map both primary and foreign IDs by their unique ledger sequence;
					// every other field and the entry-to-operation relation stays intact.
					operationIDs := deletionLedgerIdentities(b, tx)
					after := deletionSnapshotWithOperationIDs(b, tx, operationIDs)
					if expected == "" {
						expected = after
						expectedIDs = operationIDs
					} else if after != expected {
						b.Fatal("individual and batch deletion produce different persisted facts")
					} else {
						changedIDs := 0
						for id := range operationIDs {
							if _, exists := expectedIDs[id]; !exists {
								changedIDs++
							}
						}
						b.Logf("all persisted fields and ledger relations agree; %d independently generated operation IDs differ", changedIDs)
					}
					if err := tx.Rollback(); err != nil {
						b.Fatal(err)
					}
					b.Run(implementation.name, func(b *testing.B) {
						b.ReportAllocs()
						b.ResetTimer()
						for index := 0; index < b.N; index++ {
							tx, err := f.db.BeginTx(context.Background(), nil)
							if err != nil {
								b.Fatal(err)
							}
							if err := implementation.prepare(context.Background(), tx, userID, 2_000); err != nil {
								_ = tx.Rollback()
								b.Fatal(err)
							}
							if err := tx.Rollback(); err != nil {
								b.Fatal(err)
							}
						}
						b.StopTimer()
						if after := deletionSnapshot(b, f.db); after != before {
							b.Fatal("benchmark rollback changed the input fixture")
						}
					})
				}
			})
		}
	}
}

func deletionLedgerIdentities(t testing.TB, query deletionQuerier) map[string]int64 {
	t.Helper()
	rows, err := query.Query("SELECT id,ledger_seq FROM credit_operations ORDER BY ledger_seq")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	identities := make(map[string]int64)
	sequences := make(map[int64]bool)
	for rows.Next() {
		var id string
		var seq int64
		if err := rows.Scan(&id, &seq); err != nil {
			t.Fatal(err)
		}
		if _, exists := identities[id]; exists || sequences[seq] || seq <= 0 {
			t.Fatal("ledger identities must form a one-to-one sequence mapping")
		}
		identities[id], sequences[seq] = seq, true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return identities
}

// This reference retains the former per-request terminal traversal for a
// bounded before/after benchmark. Active work uses the unchanged service path.
func (f *claimFixture) prepareAccountDeletionIndividually(ctx context.Context, tx *sql.Tx, userID, at int64) error {
	for {
		var id string
		err := tx.QueryRowContext(ctx, `SELECT id FROM logical_requests WHERE user_id=? ORDER BY created_at,id LIMIT 1`, userID).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			break
		}
		if err != nil {
			return err
		}
		request, err := loadRequestTx(ctx, tx, id)
		if err != nil {
			return err
		}
		if request.UserID == nil || *request.UserID != userID {
			return ErrConflict
		}
		if request.State != RequestTerminal {
			if err := f.service.prepareRequestDeletionTx(ctx, tx, request, userID, at); err != nil {
				return err
			}
			continue
		}
		claims, err := requestClaimIDsTx(ctx, tx, id)
		if err != nil {
			return err
		}
		var rejected bool
		if err := tx.QueryRowContext(ctx, `SELECT rejection_stage IS NOT NULL FROM logical_requests WHERE id=?`, id).Scan(&rejected); err != nil {
			return err
		}
		if rejected && (request.AccountingDisposition != AccountingNone || len(claims) != 0) {
			return ErrInvariant
		}
		remaining, err := readRequestCapacityTx(ctx, tx, id)
		if err != nil {
			return err
		}
		if remaining != 0 || request.ReservedMilli != 0 ||
			request.Route == RouteDiscovery && request.AccountingDisposition != AccountingNone ||
			!rejected && request.Route != RouteDiscovery && request.AccountingDisposition != AccountingCommit && request.AccountingDisposition != AccountingRelease {
			return ErrInvariant
		}
		for _, claimID := range claims {
			record, err := loadClaimTx(ctx, tx, claimID)
			if err != nil {
				return err
			}
			if record.state == StateClaimed || record.state == StateDispatched {
				return ErrInvariant
			}
		}
		if err := detachTerminalRequestTx(ctx, tx, id, userID); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE charity_reservations SET user_id=NULL WHERE user_id=?`, userID); err != nil {
		return err
	}
	var attached int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM charity_reservations WHERE user_id=?)`, userID).Scan(&attached); err != nil {
		return err
	}
	if attached != 0 {
		return ErrInvariant
	}
	return nil
}
