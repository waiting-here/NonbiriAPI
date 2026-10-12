package rewards

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"time"
)

func (s *Service) attempt(user, at int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.minute != at/60 {
		s.minute = at / 60
		s.attempts = map[int64]int{}
	}
	if len(s.attempts) >= 8192 && s.attempts[user] == 0 {
		return false
	}
	s.attempts[user]++
	return s.attempts[user] <= 12
}
func (s *Service) Redeem(ctx context.Context, user int64, code string) (Redemption, error) {
	var out Redemption
	at, err := s.now()
	if err != nil {
		return out, err
	}
	if !s.attempt(user, at) {
		return out, ErrRate
	}
	code, err = normalizeCode(code)
	if err != nil {
		return out, ErrUnavailable
	}
	tx, err := s.config.Database.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err = s.config.Users.AuthorizeUserMutation(ctx, tx, user); err != nil {
		return out, err
	}
	if err = s.config.Gate.AuthorizeUserActivity(ctx, tx, user); err != nil {
		return out, err
	}
	var id string
	var actor sql.NullInt64
	err = tx.QueryRowContext(ctx, "SELECT id,actor_user_id FROM reward_campaigns WHERE code=? AND kind='code'", code).Scan(&id, &actor)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrUnavailable
	}
	if err != nil {
		return out, err
	}
	c, err := readConfiguration(ctx, tx, id)
	if err != nil {
		return out, err
	}
	// A committed receipt remains recoverable after expiry or exhaustion.
	var claimID int64
	err = tx.QueryRowContext(ctx, "SELECT id FROM reward_claims WHERE campaign_id=? AND user_id=? AND status='success'", id, user).Scan(&claimID)
	if err == nil {
		out.Receipt, err = readReceipt(ctx, tx, user, claimID)
		out.AlreadyClaimed = err == nil
		return out, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	if !c.Enabled || c.StartsAt != nil && at < *c.StartsAt || c.EndsAt != nil && at >= *c.EndsAt || c.Quota != nil && c.Claimed >= *c.Quota {
		return out, ErrUnavailable
	}
	f, err := loadFacts(ctx, tx, user, at, usesAssets(c.Conditions))
	if err != nil {
		return out, err
	}
	if !matches(c.Conditions, f) {
		return out, ErrConditions
	}
	results, err := grant(ctx, tx, user, actor.Int64, at, c.Title, c.Rewards)
	if err != nil {
		return out, err
	}
	r, err := tx.ExecContext(ctx, "INSERT INTO reward_claims(campaign_id,user_id,status,receipt,created_at,completed_at) VALUES(?,?,'success',?,?,?)", id, user, jsonText(results), at, at)
	if err != nil {
		return out, err
	}
	claimID, err = r.LastInsertId()
	if err != nil {
		return out, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE reward_campaigns SET claimed=claimed+1 WHERE id=?", id); err != nil {
		return out, err
	}
	if s.config.Activity != nil {
		if err = s.config.Activity.RecordLimitedActivityTx(ctx, tx, user, at); err != nil {
			return out, err
		}
	}
	out.Receipt, err = readReceipt(ctx, tx, user, claimID)
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}
func readReceipt(ctx context.Context, tx *sql.Tx, user, id int64) (Receipt, error) {
	var out Receipt
	var raw string
	err := tx.QueryRowContext(ctx, "SELECT r.id,r.campaign_id,c.title,c.kind,r.status,r.receipt,r.created_at,r.completed_at FROM reward_claims r JOIN reward_campaigns c ON c.id=r.campaign_id WHERE r.user_id=? AND r.id=?", user, id).
		Scan(&out.ID, &out.CampaignID, &out.Title, &out.Kind, &out.Status, &raw, &out.CreatedAt, &out.CompletedAt)
	if err != nil {
		return out, err
	}
	err = json.Unmarshal([]byte(raw), &out.Results)
	return out, err
}
func (s *Service) History(ctx context.Context, user int64, cursor string) (ReceiptPage, error) {
	out := ReceiptPage{Items: []Receipt{}}
	before, err := cursorValue(cursor)
	if err != nil {
		return out, err
	}
	tx, err := s.config.Database.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err = s.config.Users.AuthorizeUserMutation(ctx, tx, user); err != nil {
		return out, err
	}
	ids, err := receiptIDs(ctx, tx, user, before, 51)
	if err != nil {
		return out, err
	}
	if len(ids) > 50 {
		out.Next = strconv.FormatInt(ids[49], 10)
		ids = ids[:50]
	}
	for _, id := range ids {
		r, e := readReceipt(ctx, tx, user, id)
		if e != nil {
			return out, e
		}
		out.Items = append(out.Items, r)
	}
	return out, nil
}
func receiptIDs(ctx context.Context, tx *sql.Tx, user, before int64, limit int) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, "SELECT id FROM reward_claims WHERE user_id=? AND id<? AND status='success' ORDER BY id DESC LIMIT ?", user, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ProcessBatch resumes the immutable recipient set. Each recipient is atomic.
func (s *Service) ProcessBatch(ctx context.Context, limit int) (int, error) {
	if limit < 1 || limit > 100 {
		return 0, ErrInvalid
	}
	done := 0
	for done < limit {
		if err := ctx.Err(); err != nil {
			return done, err
		}
		more, err := s.processOne(ctx)
		if err != nil {
			return done, err
		}
		if !more {
			break
		}
		done++
	}
	return done, nil
}
func (s *Service) processOne(ctx context.Context) (bool, error) {
	at, err := s.now()
	if err != nil {
		return false, err
	}
	tx, err := s.config.Database.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var id int64
	var user, actor sql.NullInt64
	var campaign string
	err = tx.QueryRowContext(ctx, "SELECT r.id,r.user_id,r.campaign_id,c.actor_user_id FROM reward_claims r JOIN reward_campaigns c ON c.id=r.campaign_id WHERE r.status='pending' AND c.kind='broadcast' ORDER BY r.id LIMIT 1").Scan(&id, &user, &campaign, &actor)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	c, err := readConfiguration(ctx, tx, campaign)
	if err != nil {
		return false, err
	}
	var alive bool
	if user.Valid {
		err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM users WHERE id=? AND is_admin=0 AND NOT EXISTS(SELECT 1 FROM user_deletion_markers WHERE user_id=users.id))", user.Int64).Scan(&alive)
		if err != nil {
			return false, err
		}
	}
	if !alive {
		if _, err = tx.ExecContext(ctx, "UPDATE reward_claims SET user_id=NULL,status='skipped',completed_at=? WHERE id=?", at, id); err != nil {
			return false, err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE reward_campaigns SET skipped=skipped+1 WHERE id=?", campaign); err != nil {
			return false, err
		}
	} else {
		if _, err = tx.ExecContext(ctx, "SAVEPOINT reward_delivery"); err != nil {
			return false, err
		}
		results, deliveryErr := grant(ctx, tx, user.Int64, actor.Int64, at, c.Title, c.Rewards)
		if deliveryErr != nil {
			// Transient storage failures keep the recipient pending for automatic retry.
			if !errors.Is(deliveryErr, ErrCapacity) {
				return false, deliveryErr
			}
			if _, err = tx.ExecContext(ctx, "ROLLBACK TO reward_delivery"); err != nil {
				return false, err
			}
			if _, err = tx.ExecContext(ctx, "RELEASE reward_delivery"); err != nil {
				return false, err
			}
			failure := "delivery"
			if errors.Is(deliveryErr, ErrCapacity) {
				failure = "capacity"
			}
			if _, err = tx.ExecContext(ctx, "UPDATE reward_claims SET status='failed',failure=?,completed_at=? WHERE id=?", failure, at, id); err != nil {
				return false, err
			}
		} else {
			if _, err = tx.ExecContext(ctx, "RELEASE reward_delivery"); err != nil {
				return false, err
			}
			if _, err = tx.ExecContext(ctx, "UPDATE reward_claims SET status='success',receipt=?,completed_at=? WHERE id=?", jsonText(results), at, id); err != nil {
				return false, err
			}
			if _, err = tx.ExecContext(ctx, "UPDATE reward_campaigns SET claimed=claimed+1 WHERE id=?", campaign); err != nil {
				return false, err
			}
		}
	}
	return true, tx.Commit()
}
func (s *Service) Run(ctx context.Context, report func(error)) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var last time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			work, cancel := context.WithTimeout(ctx, 5*time.Second)
			_, err := s.ProcessBatch(work, 100)
			cancel()
			if err != nil && ctx.Err() == nil && time.Since(last) > time.Minute {
				last = time.Now()
				report(err)
			}
		}
	}
}
func (s *Service) ExportUserTx(ctx context.Context, tx *sql.Tx, user int64, limit int) ([]Receipt, error) {
	ids, err := receiptIDs(ctx, tx, user, 9223372036854775807, limit+1)
	if err != nil {
		return nil, err
	}
	if len(ids) > limit {
		return nil, ErrExportLimit
	}
	out := []Receipt{}
	for _, id := range ids {
		r, err := readReceipt(ctx, tx, user, id)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}
func (s *Service) DeleteUserTx(ctx context.Context, tx *sql.Tx, user int64) error {
	// Aggregate counts retain economic facts without a former account identity.
	if _, err := tx.ExecContext(ctx, "UPDATE reward_campaigns SET skipped=skipped+(SELECT count(*) FROM reward_claims WHERE campaign_id=reward_campaigns.id AND user_id=? AND status IN ('pending','failed')) WHERE kind='broadcast'", user); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, "DELETE FROM reward_claims WHERE user_id=?", user)
	return err
}
