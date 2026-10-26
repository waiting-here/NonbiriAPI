package blackjack

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

type historyAuthorizer struct{ afterRead func(context.Context) error }

func (a *historyAuthorizer) AuthorizeUserMutation(ctx context.Context, tx *sql.Tx, user int64) error {
	var allowed bool
	if err := tx.QueryRowContext(ctx, `SELECT is_banned=0 FROM users WHERE id=?`, user).Scan(&allowed); err != nil {
		return err
	}
	if !allowed {
		return resources.ErrForbidden
	}
	if a.afterRead != nil {
		return a.afterRead(ctx)
	}
	return nil
}
func (a *historyAuthorizer) AuthorizeAdminMutation(ctx context.Context, tx *sql.Tx) error {
	var user int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM users WHERE is_admin=1`).Scan(&user); err != nil {
		return err
	}
	if a.afterRead != nil {
		return a.afterRead(ctx)
	}
	return nil
}

func TestHistoryReadsLeaveWriterAvailable(t *testing.T) {
	store, user, now := historyQueryFixture(t, 5)
	if _, err := store.DB().Exec(`UPDATE maintenance_state SET enabled=0 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	_, err := store.DB().Exec(`INSERT INTO users(username,is_admin,donation_credit_mag,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,created_at,updated_at)
SELECT 'history admin',1,donation_credit_mag,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,created_at,updated_at FROM users WHERE id=?`, user)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.DB().Exec(`UPDATE game_blackjack_clock SET observed_at=? WHERE id=1`, now+60)
	if err != nil {
		t.Fatal(err)
	}
	a := &historyAuthorizer{}
	s := &Service{database: store.DB(), readDB: store.ReadDB(), authorizer: a, adminAuthorizer: a, now: func() time.Time { return time.Unix(now, 0) }}
	id := "bjt_000000000000000000001A"
	identity := Identity{UserID: user}
	reads := []struct {
		name string
		run  func(context.Context) error
	}{
		{"history", func(ctx context.Context) error {
			p, err := s.History(ctx, identity, PageInput{})
			if err == nil && len(p.Items) != 3 {
				t.Errorf("history items=%d", len(p.Items))
			}
			return err
		}},
		{"detail", func(ctx context.Context) error { _, err := s.HistoryDetail(ctx, identity, id); return err }},
		{"admin history", func(ctx context.Context) error {
			_, err := s.AdminHistory(ctx, PageInput{Dataset: "recent"})
			return err
		}},
		{"admin detail", func(ctx context.Context) error { _, err := s.AdminDetail(ctx, "recent", id); return err }},
		{"admin export", func(ctx context.Context) error {
			_, err := s.AdminExport(ctx, PageInput{Dataset: "recent"})
			return err
		}},
	}
	for _, read := range reads {
		t.Run(read.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			reached, resume := make(chan struct{}), make(chan struct{})
			a.afterRead = func(ctx context.Context) error {
				close(reached)
				select {
				case <-resume:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			done := make(chan error, 1)
			go func() { done <- read.run(ctx) }()
			select {
			case <-reached:
			case err := <-done:
				t.Fatalf("read ended early: %v", err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if store.DB().Stats().InUse != 0 || store.ReadDB().Stats().InUse != 1 {
				t.Errorf("writer=%+v reader=%+v", store.DB().Stats(), store.ReadDB().Stats())
			}
			_, writeErr := store.DB().ExecContext(ctx, `UPDATE site_config SET value='history probe' WHERE key='site_name'`)
			close(resume)
			readErr := <-done
			if writeErr != nil || readErr != nil {
				t.Fatalf("writer=%v read=%v", writeErr, readErr)
			}
		})
	}
	a.afterRead = nil
	tx, observed, err := s.beginRead(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if observed != now+60 {
		t.Errorf("clock regressed to %d", observed)
	}
	if _, err := tx.Exec(`UPDATE game_blackjack_clock SET observed_at=0 WHERE id=1`); err == nil {
		t.Error("read transaction accepted a write")
	}
	tx.Rollback()
	a.afterRead = func(ctx context.Context) error {
		_, err := store.DB().ExecContext(ctx, `UPDATE users SET is_banned=1 WHERE id=?`, user)
		return err
	}
	ctx, cancelSnapshot := context.WithTimeout(t.Context(), 3*time.Second)
	page, err := s.History(ctx, identity, PageInput{})
	cancelSnapshot()
	if err != nil || len(page.Items) != 3 {
		t.Fatalf("authorized snapshot changed: items=%d err=%v", len(page.Items), err)
	}
	a.afterRead = nil
	if _, err := s.History(t.Context(), identity, PageInput{}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("revoked user read: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.History(ctx, identity, PageInput{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled history: %v", err)
	}
}
