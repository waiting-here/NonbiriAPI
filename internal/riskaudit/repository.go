package riskaudit

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
)

type FinalAuthorizer interface {
	AuthorizeAdmin(context.Context, *sql.Tx, int64) error
	AuthorizeStewardMutation(context.Context, *sql.Tx, int64) error
}

type RepositoryOptions struct {
	Now           func() time.Time
	ConfigChanged func(Config)
	FinalAuth     FinalAuthorizer
}
type Repository struct {
	db            *sql.DB
	now           func() time.Time
	configChanged func(Config)
	finalAuth     FinalAuthorizer
}

func NewRepository(database *sql.DB, options RepositoryOptions) (*Repository, error) {
	if database == nil || options.FinalAuth == nil {
		return nil, ErrInvalid
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &Repository{db: database, now: options.Now, configChanged: options.ConfigChanged, finalAuth: options.FinalAuth}, nil
}
func (r *Repository) authorize(ctx context.Context, tx *sql.Tx, actor Actor, now int64) error {
	if actor.UserID <= 0 {
		return ErrForbidden
	}
	var err error
	if actor.Admin {
		err = r.finalAuth.AuthorizeAdmin(ctx, tx, actor.UserID)
	} else {
		err = r.finalAuth.AuthorizeStewardMutation(ctx, tx, actor.UserID)
	}
	if err != nil {
		if errors.Is(err, authz.ErrUnauthorized) || errors.Is(err, authz.ErrForbidden) {
			return ErrForbidden
		}
		return ErrUnavailable
	}
	if actor.Admin {
		return nil
	}
	var allowed int
	err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE id=? AND is_admin=0 AND COALESCE(level,auto_level)=6 AND (is_banned=0 OR (banned_until IS NOT NULL AND banned_until<=?))`, actor.UserID, now).Scan(&allowed)
	if err != nil {
		return ErrUnavailable
	}
	if allowed != 1 {
		return ErrForbidden
	}
	return nil
}
func (r *Repository) begin(ctx context.Context, actor Actor, write bool) (*sql.Tx, error) {
	if r == nil || ctx == nil {
		return nil, ErrInvalid
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: !write})
	if err != nil {
		return nil, ErrUnavailable
	}
	if err = r.authorize(ctx, tx, actor, r.now().Unix()); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	return tx, nil
}
func readConfig(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (Config, error) {
	var c Config
	err := q.QueryRowContext(ctx, `SELECT threshold_percent,consecutive_minutes,shared_ip_hours,shared_ip_users,revision,updated_at FROM risk_audit_config WHERE id=1`).Scan(&c.ThresholdPercent, &c.ConsecutiveMinutes, &c.SharedIPHours, &c.SharedIPUsers, &c.Revision, &c.UpdatedAt)
	if err != nil || !c.Valid() {
		return Config{}, ErrUnavailable
	}
	return c, nil
}
func (r *Repository) CurrentConfig(ctx context.Context) (Config, error) {
	if r == nil || ctx == nil {
		return Config{}, ErrInvalid
	}
	return readConfig(ctx, r.db)
}
func (r *Repository) GetConfig(ctx context.Context, actor Actor) (Config, error) {
	tx, err := r.begin(ctx, actor, false)
	if err != nil {
		return Config{}, err
	}
	defer tx.Rollback()
	return readConfig(ctx, tx)
}
func (r *Repository) UpdateConfig(ctx context.Context, actor Actor, c Config) (Config, error) {
	if !actor.Admin {
		return Config{}, ErrForbidden
	}
	if !c.Valid() {
		return Config{}, ErrInvalid
	}
	tx, err := r.begin(ctx, actor, true)
	if err != nil {
		return Config{}, err
	}
	defer tx.Rollback()
	old, err := readConfig(ctx, tx)
	if err != nil {
		return Config{}, err
	}
	if c.Revision != old.Revision || old.Revision == 1<<63-1 {
		return Config{}, ErrConflict
	}
	c.Revision++
	c.UpdatedAt = r.now().Unix()
	_, err = tx.ExecContext(ctx, `UPDATE risk_audit_config SET threshold_percent=?,consecutive_minutes=?,shared_ip_hours=?,shared_ip_users=?,revision=?,updated_at=? WHERE id=1`, c.ThresholdPercent, c.ConsecutiveMinutes, c.SharedIPHours, c.SharedIPUsers, c.Revision, c.UpdatedAt)
	if err != nil || tx.Commit() != nil {
		return Config{}, ErrUnavailable
	}
	if r.configChanged != nil {
		r.configChanged(c)
	}
	return c, nil
}
func nullable(value int) any {
	if value == 0 {
		return nil
	}
	return value
}
func (r *Repository) SaveMinutes(ctx context.Context, minutes []Minute, gaps []Gap) error {
	if r == nil || ctx == nil || len(minutes) > 500 || len(gaps) > 500 {
		return ErrInvalid
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return ErrUnavailable
	}
	defer tx.Rollback()
	for _, m := range minutes {
		if m.UserID <= 0 || m.Minute < 0 || m.Minute%60 != 0 || m.Epoch == "" || len(m.Epoch) > 64 || (m.Kind != "total" && !validKind(m.Kind)) || m.RPMCommitted < 0 || m.RPMReleased < 0 || m.RPMPending < 0 || m.RPMDenied < 0 || m.ConcurrencyDenied < 0 || m.OccupancyMillis < 0 || m.Peak < 0 || m.Coverage < 0 || m.ConfigRevision < 1 || m.UpdatedAt < m.Minute {
			return ErrInvalid
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO risk_audit_minutes(epoch,user_id,minute,call_kind,rpm_committed,rpm_released,rpm_pending,rpm_denied,concurrency_denied,occupancy_millis,peak,rpm_limit,concurrency_limit,coverage,config_revision,updated_at)
SELECT ?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,? WHERE EXISTS(SELECT 1 FROM users WHERE id=?)
ON CONFLICT(epoch,user_id,minute,call_kind) DO UPDATE SET rpm_committed=excluded.rpm_committed,rpm_released=excluded.rpm_released,rpm_pending=excluded.rpm_pending,rpm_denied=excluded.rpm_denied,concurrency_denied=excluded.concurrency_denied,occupancy_millis=excluded.occupancy_millis,peak=excluded.peak,rpm_limit=excluded.rpm_limit,concurrency_limit=excluded.concurrency_limit,coverage=excluded.coverage,config_revision=excluded.config_revision,updated_at=excluded.updated_at WHERE excluded.updated_at>=risk_audit_minutes.updated_at`, m.Epoch, m.UserID, m.Minute, m.Kind, m.RPMCommitted, m.RPMReleased, m.RPMPending, m.RPMDenied, m.ConcurrencyDenied, m.OccupancyMillis, m.Peak, nullable(m.RPMLimit), nullable(m.ConcurrencyLimit), m.Coverage, m.ConfigRevision, m.UpdatedAt, m.UserID)
		if err != nil {
			return ErrUnavailable
		}
	}
	for _, g := range gaps {
		_, err = tx.ExecContext(ctx, `INSERT INTO risk_audit_gaps(epoch,minute,reason,lost_count) VALUES(?,?,?,?) ON CONFLICT(epoch,minute,reason) DO UPDATE SET lost_count=risk_audit_gaps.lost_count+excluded.lost_count`, g.Epoch, g.Minute, g.Reason, g.LostCount)
		if err != nil {
			return ErrUnavailable
		}
	}
	if tx.Commit() != nil {
		return ErrUnavailable
	}
	return nil
}

type CleanupResult struct {
	Processed int
	Deleted   int
	More      bool
}

func (r *Repository) Cleanup(ctx context.Context, now time.Time, limit int) error {
	_, err := r.CleanupBatch(ctx, now, limit)
	return err
}

// CleanupBatch shares one row budget across all ordinary audit retention data.
func (r *Repository) CleanupBatch(ctx context.Context, now time.Time, limit int) (CleanupResult, error) {
	if r == nil || ctx == nil || limit < 1 || limit > 100 {
		return CleanupResult{}, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return CleanupResult{}, ErrUnavailable
	}
	defer tx.Rollback()
	cutoff := now.Add(-Retention).Unix()
	result, err := tx.ExecContext(ctx, `DELETE FROM risk_audit_minutes WHERE rowid IN (SELECT rowid FROM risk_audit_minutes WHERE minute<? ORDER BY minute LIMIT ?)`, cutoff, limit)
	if err != nil {
		return CleanupResult{}, ErrUnavailable
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return CleanupResult{}, ErrUnavailable
	}
	if remaining := int64(limit) - deleted; remaining > 0 {
		result, err = tx.ExecContext(ctx, `DELETE FROM risk_audit_gaps WHERE rowid IN (SELECT rowid FROM risk_audit_gaps WHERE minute<? ORDER BY minute LIMIT ?)`, cutoff, remaining)
		if err != nil {
			return CleanupResult{}, ErrUnavailable
		}
		gapDeleted, err := result.RowsAffected()
		if err != nil {
			return CleanupResult{}, ErrUnavailable
		}
		deleted += gapDeleted
	}
	var more bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM risk_audit_minutes WHERE minute<? LIMIT 1) OR EXISTS(SELECT 1 FROM risk_audit_gaps WHERE minute<? LIMIT 1)`, cutoff, cutoff).Scan(&more); err != nil {
		return CleanupResult{}, ErrUnavailable
	}
	if tx.Commit() != nil {
		return CleanupResult{}, ErrUnavailable
	}
	return CleanupResult{Processed: int(deleted), Deleted: int(deleted), More: more}, nil
}

type scanner interface{ Scan(...any) error }

const ruleColumns = `id,name,status,enabled,revision,conditions_json,evidence_note,evidence_url,created_by_role,created_by_user_id,updated_by_role,updated_by_user_id,created_at,updated_at`

func scanRule(row scanner) (Rule, error) {
	var rule Rule
	var raw string
	err := row.Scan(&rule.ID, &rule.Name, &rule.Status, &rule.Enabled, &rule.Revision, &raw, &rule.EvidenceNote, &rule.EvidenceURL, &rule.CreatedByRole, &rule.CreatedByUserID, &rule.UpdatedByRole, &rule.UpdatedByUserID, &rule.CreatedAt, &rule.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Rule{}, ErrNotFound
	}
	if err != nil || len(raw) > 16384 || json.Unmarshal([]byte(raw), &rule.Conditions) != nil || validateRule(rule) != nil {
		return Rule{}, ErrUnavailable
	}
	return rule, nil
}
func rulesTx(ctx context.Context, tx *sql.Tx) ([]Rule, error) {
	rows, err := tx.QueryContext(ctx, `SELECT `+ruleColumns+` FROM risk_client_rules ORDER BY id LIMIT 1001`)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer rows.Close()
	rules := make([]Rule, 0)
	for rows.Next() {
		rule, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		rules = append(rules, rule)
		if len(rules) > MaxRules {
			return nil, ErrUnavailable
		}
	}
	if rows.Err() != nil {
		return nil, ErrUnavailable
	}
	return rules, nil
}
func (r *Repository) Rules(ctx context.Context, actor Actor) ([]Rule, error) {
	tx, err := r.begin(ctx, actor, false)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	return rulesTx(ctx, tx)
}
func actorFields(actor Actor) (string, any) {
	if actor.Admin {
		return "admin", actor.UserID
	}
	return "level6", actor.UserID
}
func (r *Repository) PutRule(ctx context.Context, actor Actor, rule Rule, create bool) (Rule, error) {
	if validateRule(rule) != nil {
		return Rule{}, ErrInvalid
	}
	tx, err := r.begin(ctx, actor, true)
	if err != nil {
		return Rule{}, err
	}
	defer tx.Rollback()
	now := r.now().Unix()
	role, user := actorFields(actor)
	conditions, _ := json.Marshal(rule.Conditions)
	if len(conditions) > 16384 {
		return Rule{}, ErrInvalid
	}
	if create {
		if rule.ID != "" || rule.Revision != 0 {
			return Rule{}, ErrInvalid
		}
		var count int
		if tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM risk_client_rules`).Scan(&count) != nil {
			return Rule{}, ErrUnavailable
		}
		if count >= MaxRules {
			return Rule{}, ErrConflict
		}
		rule.ID, err = db.GenerateOpaqueID("rsk_")
		if err != nil {
			return Rule{}, ErrUnavailable
		}
		rule.Revision = 1
		_, err = tx.ExecContext(ctx, `INSERT INTO risk_client_rules(`+ruleColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, rule.ID, rule.Name, rule.Status, rule.Enabled, rule.Revision, string(conditions), rule.EvidenceNote, rule.EvidenceURL, role, user, role, user, now, now)
	} else {
		if len(rule.ID) != 26 || rule.Revision < 1 || rule.Revision == 1<<63-1 {
			return Rule{}, ErrInvalid
		}
		var result sql.Result
		result, err = tx.ExecContext(ctx, `UPDATE risk_client_rules SET name=?,status=?,enabled=?,revision=revision+1,conditions_json=?,evidence_note=?,evidence_url=?,updated_by_role=?,updated_by_user_id=?,updated_at=? WHERE id=? AND revision=?`, rule.Name, rule.Status, rule.Enabled, string(conditions), rule.EvidenceNote, rule.EvidenceURL, role, user, now, rule.ID, rule.Revision)
		if err == nil {
			n, _ := result.RowsAffected()
			if n != 1 {
				return Rule{}, ErrConflict
			}
		}
	}
	if err != nil {
		return Rule{}, ErrUnavailable
	}
	out, err := scanRule(tx.QueryRowContext(ctx, `SELECT `+ruleColumns+` FROM risk_client_rules WHERE id=?`, rule.ID))
	if err != nil {
		return Rule{}, err
	}
	if tx.Commit() != nil {
		return Rule{}, ErrUnavailable
	}
	return out, nil
}
func (r *Repository) DeleteRule(ctx context.Context, actor Actor, id string, revision int64) error {
	if len(id) != 26 || revision < 1 {
		return ErrInvalid
	}
	tx, err := r.begin(ctx, actor, true)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `DELETE FROM risk_client_rules WHERE id=? AND revision=?`, id, revision)
	if err != nil {
		return ErrUnavailable
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return ErrConflict
	}
	if tx.Commit() != nil {
		return ErrUnavailable
	}
	return nil
}
