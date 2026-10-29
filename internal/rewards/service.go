package rewards

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/waiting-here/NonbiriAPI/internal/antiabuse"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/managementlog"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type Service struct {
	config Config
	abuse  *antiabuse.Service
}

func New(c Config) (*Service, error) {
	if c.Database == nil || c.Users == nil || c.Admins == nil || c.Gate == nil || c.Keys == nil {
		return nil, ErrInvalid
	}
	if c.ReadDatabase == nil {
		c.ReadDatabase = c.Database
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	return &Service{config: c}, nil
}

// AttachAbuse connects the shared restriction service before serving requests.
func (s *Service) AttachAbuse(abuse *antiabuse.Service) error {
	if abuse == nil || s.abuse != nil {
		return ErrInvalid
	}
	s.abuse = abuse
	return nil
}
func (s *Service) now() (int64, error) {
	at := s.config.Now().Unix()
	if at < 0 || at > 253402300799 {
		return 0, ErrInvalid
	}
	return at, nil
}
func jsonText(v any) string { b, _ := json.Marshal(v); return string(b) }
func normalizeCode(code string) (string, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if len(code) < 4 || len(code) > 64 {
		return "", ErrInvalid
	}
	for _, c := range code {
		if !(c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return "", ErrInvalid
		}
	}
	return code, nil
}
func validateWindow(quota, starts, ends *int64) error {
	if quota != nil && (*quota < 1 || *quota > maxCount) || starts != nil && (*starts < 0 || *starts > 253402300799) || ends != nil && (*ends < 1 || *ends > 253402300799) || starts != nil && ends != nil && *starts >= *ends {
		return ErrInvalid
	}
	return nil
}
func validateInput(in Input) error {
	if strings.TrimSpace(in.Title) == "" || utf8.RuneCountInString(in.Title) > 80 || len(in.Title) > 320 {
		return ErrInvalid
	}
	if err := validateRewards(in.Rewards); err != nil {
		return err
	}
	if err := validateCondition(in.Conditions); err != nil {
		return err
	}
	if err := validateWindow(in.Quota, in.StartsAt, in.EndsAt); err != nil {
		return err
	}
	switch in.Kind {
	case "broadcast":
		if in.Code != "" || in.Quota != nil || in.StartsAt != nil || in.EndsAt != nil {
			return ErrInvalid
		}
	case "code":
		if in.Code != "" {
			_, err := normalizeCode(in.Code)
			return err
		}
	default:
		return ErrInvalid
	}
	return nil
}
func (s *Service) Preview(ctx context.Context, actor int64, in Input) (Preview, error) {
	var out Preview
	if err := validateInput(in); err != nil {
		return out, err
	}
	at, err := s.now()
	if err != nil {
		return out, err
	}
	tx, err := s.config.ReadDatabase.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err = s.config.Admins.AuthorizeAdmin(ctx, tx, actor); err != nil {
		return out, err
	}
	out, err = s.selectRecipients(ctx, tx, in.Conditions, at, "")
	return out, err
}
func (s *Service) selectRecipients(ctx context.Context, tx *sql.Tx, c Condition, at int64, campaign string) (Preview, error) {
	out := Preview{At: at}
	last := int64(0)
	for {
		rows, err := tx.QueryContext(ctx, "SELECT id FROM users WHERE id>? AND is_admin=0 AND NOT EXISTS(SELECT 1 FROM user_deletion_markers WHERE user_id=users.id) ORDER BY id LIMIT 256", last)
		if err != nil {
			return out, err
		}
		ids := []int64{}
		for rows.Next() {
			var id int64
			if err = rows.Scan(&id); err != nil {
				break
			}
			ids = append(ids, id)
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return out, err
		}
		if len(ids) == 0 {
			break
		}
		for _, id := range ids {
			f, err := loadFacts(ctx, tx, id, at, usesAssets(c))
			if err != nil {
				return out, err
			}
			if !matches(c, f) {
				continue
			}
			out.Matched++
			if f.banned {
				out.Banned++
			}
			if campaign != "" {
				if _, err = tx.ExecContext(ctx, "INSERT INTO reward_claims(campaign_id,user_id,status,created_at) VALUES(?,?,'pending',?)", campaign, id, at); err != nil {
					return out, err
				}
			}
		}
		last = ids[len(ids)-1]
	}
	return out, nil
}
func (s *Service) Create(ctx context.Context, actor int64, key string, in Input) (Campaign, error) {
	var out Campaign
	if err := validateInput(in); err != nil {
		return out, err
	}
	keyHash, err := idempotency.KeyHash(key)
	if err != nil {
		return out, ErrInvalid
	}
	if in.Code != "" {
		in.Code, _ = normalizeCode(in.Code)
	}
	digest := sha256.Sum256([]byte(jsonText(in)))
	at, err := s.now()
	if err != nil {
		return out, err
	}
	tx, err := s.config.Database.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err = s.config.Admins.AuthorizeAdmin(ctx, tx, actor); err != nil {
		return out, err
	}
	var id string
	var previous []byte
	err = tx.QueryRowContext(ctx, "SELECT id,request_hash FROM reward_campaigns WHERE key_hash=?", keyHash[:]).Scan(&id, &previous)
	if err == nil {
		if subtle.ConstantTimeCompare(previous, digest[:]) != 1 {
			return out, ErrConflict
		}
		managementlog.MarkReplay(ctx)
		return readCampaign(ctx, tx, id)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	if in.Kind == "code" && in.Code == "" {
		var random [12]byte
		if _, err = rand.Read(random[:]); err != nil {
			return out, err
		}
		in.Code = strings.ToUpper(hex.EncodeToString(random[:]))
	}
	if in.Kind == "code" {
		var exists bool
		if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM reward_campaigns WHERE code=?)", in.Code).Scan(&exists); err != nil {
			return out, err
		}
		if exists {
			return out, ErrConflict
		}
	}
	id, err = db.GenerateOpaqueID("rw_")
	if err != nil {
		return out, err
	}
	var code any
	if in.Kind == "code" {
		code = in.Code
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO reward_campaigns(id,kind,title,rewards,conditions,code,enabled,quota,starts_at,ends_at,actor_user_id,key_hash,request_hash,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
		id, in.Kind, in.Title, jsonText(in.Rewards), jsonText(in.Conditions), code, in.Enabled, in.Quota, in.StartsAt, in.EndsAt, actor, keyHash[:], digest[:], at, at)
	if err != nil {
		return out, err
	}
	if in.Kind == "broadcast" {
		selected, e := s.selectRecipients(ctx, tx, in.Conditions, at, id)
		if e != nil {
			return out, e
		}
		if _, err = tx.ExecContext(ctx, "UPDATE reward_campaigns SET matched=? WHERE id=?", selected.Matched, id); err != nil {
			return out, err
		}
	}
	out, err = readCampaign(ctx, tx, id)
	if err != nil {
		return out, err
	}
	metadata, _ := json.Marshal(in)
	managementlog.ChangedFields(ctx, metadata)
	managementlog.SetTarget(ctx, "reward_campaign", id, in.Title)
	managementlog.SetCorrelation(ctx, "reward_campaign", id)
	if in.Kind == "broadcast" && out.Matched > 0 {
		managementlog.SetResult(ctx, managementlog.Result{Outcome: managementlog.Accepted})
	}
	return out, managementlog.Commit(ctx, tx)
}

const campaignColumns = "id,kind,title,rewards,conditions,COALESCE(code,''),enabled,quota,starts_at,ends_at,revision,claimed,matched,skipped,created_at"

type scanner interface{ Scan(...any) error }

func scanCampaign(row scanner) (Campaign, error) {
	var c Campaign
	var items, condition string
	err := row.Scan(&c.ID, &c.Kind, &c.Title, &items, &condition, &c.Code, &c.Enabled, &c.Quota, &c.StartsAt, &c.EndsAt, &c.Revision, &c.Claimed, &c.Matched, &c.Skipped, &c.CreatedAt)
	if err != nil {
		return c, err
	}
	if err = json.Unmarshal([]byte(items), &c.Rewards); err != nil {
		return c, err
	}
	err = json.Unmarshal([]byte(condition), &c.Conditions)
	return c, err
}
func readConfiguration(ctx context.Context, tx *sql.Tx, id string) (Campaign, error) {
	return scanCampaign(tx.QueryRowContext(ctx, "SELECT "+campaignColumns+" FROM reward_campaigns WHERE id=?", id))
}
func readCampaign(ctx context.Context, tx *sql.Tx, id string) (Campaign, error) {
	c, err := readConfiguration(ctx, tx, id)
	if err != nil {
		return c, err
	}
	err = tx.QueryRowContext(ctx, "SELECT COALESCE(sum(status='pending'),0),COALESCE(sum(status='failed'),0) FROM reward_claims WHERE campaign_id=?", id).Scan(&c.Pending, &c.Failed)
	return c, err
}
func cursorValue(cursor string) (int64, error) {
	if cursor == "" {
		return 9223372036854775807, nil
	}
	v, e := strconv.ParseInt(cursor, 10, 64)
	if e != nil || v < 1 || strconv.FormatInt(v, 10) != cursor {
		return 0, ErrInvalid
	}
	return v, nil
}
func (s *Service) List(ctx context.Context, actor int64, cursor string) (CampaignPage, error) {
	out := CampaignPage{Items: []Campaign{}}
	before, err := cursorValue(cursor)
	if err != nil {
		return out, err
	}
	tx, err := s.config.ReadDatabase.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err = s.config.Admins.AuthorizeAdmin(ctx, tx, actor); err != nil {
		return out, err
	}
	rows, err := tx.QueryContext(ctx, "SELECT rowid,id FROM reward_campaigns WHERE rowid<? ORDER BY rowid DESC LIMIT 51", before)
	if err != nil {
		return out, err
	}
	type entry struct {
		row int64
		id  string
	}
	ids := []entry{}
	for rows.Next() {
		var e entry
		if err = rows.Scan(&e.row, &e.id); err != nil {
			break
		}
		ids = append(ids, e)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return out, err
	}
	if len(ids) > 50 {
		out.Next = strconv.FormatInt(ids[49].row, 10)
		ids = ids[:50]
	}
	for _, e := range ids {
		c, err := readCampaign(ctx, tx, e.id)
		if err != nil {
			return out, err
		}
		out.Items = append(out.Items, c)
	}
	return out, nil
}
func (s *Service) Update(ctx context.Context, actor int64, in Edit) (Campaign, error) {
	var out Campaign
	if !db.ValidateOpaqueID(in.ID, "rw_") || in.Revision < 1 || in.Revision >= maxCount || strings.TrimSpace(in.Title) == "" || utf8.RuneCountInString(in.Title) > 80 {
		return out, ErrInvalid
	}
	if err := validateWindow(in.Quota, in.StartsAt, in.EndsAt); err != nil {
		return out, err
	}
	at, err := s.now()
	if err != nil {
		return out, err
	}
	tx, err := s.config.Database.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err = s.config.Admins.AuthorizeAdmin(ctx, tx, actor); err != nil {
		return out, err
	}
	c, err := readCampaign(ctx, tx, in.ID)
	if err != nil {
		return out, err
	}
	if c.Kind != "code" {
		return out, ErrInvalid
	}
	if c.Revision != in.Revision {
		if c.Title == in.Title && c.Enabled == in.Enabled && jsonText(c.Quota) == jsonText(in.Quota) && jsonText(c.StartsAt) == jsonText(in.StartsAt) && jsonText(c.EndsAt) == jsonText(in.EndsAt) {
			managementlog.SetTarget(ctx, "reward_campaign", in.ID, in.Title)
			managementlog.SetResult(ctx, managementlog.Result{Outcome: managementlog.NoChange, Code: "unchanged"})
			return c, managementlog.Commit(ctx, tx)
		}
		return out, ErrConflict
	}
	_, err = tx.ExecContext(ctx, "UPDATE reward_campaigns SET title=?,enabled=?,quota=?,starts_at=?,ends_at=?,revision=revision+1,updated_at=? WHERE id=?", in.Title, in.Enabled, in.Quota, in.StartsAt, in.EndsAt, at, in.ID)
	if err != nil {
		return out, err
	}
	out, err = readCampaign(ctx, tx, in.ID)
	if err != nil {
		return out, err
	}
	metadata, _ := json.Marshal(in)
	managementlog.ChangedFields(ctx, metadata)
	managementlog.SetTarget(ctx, "reward_campaign", in.ID, in.Title)
	return out, managementlog.Commit(ctx, tx)
}
func (s *Service) Retry(ctx context.Context, actor int64, id string) (Campaign, error) {
	var out Campaign
	if !db.ValidateOpaqueID(id, "rw_") {
		return out, ErrInvalid
	}
	tx, err := s.config.Database.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err = s.config.Admins.AuthorizeAdmin(ctx, tx, actor); err != nil {
		return out, err
	}
	c, err := readCampaign(ctx, tx, id)
	if err != nil {
		return out, err
	}
	if c.Kind != "broadcast" {
		return out, ErrInvalid
	}
	retried, retryErr := tx.ExecContext(ctx, "UPDATE reward_claims SET status='pending',failure='' WHERE campaign_id=? AND status='failed'", id)
	if err = retryErr; err != nil {
		return out, err
	}
	out, err = readCampaign(ctx, tx, id)
	if err != nil {
		return out, err
	}
	count, err := retried.RowsAffected()
	if err != nil {
		return out, err
	}
	managementlog.SetTarget(ctx, "reward_campaign", id, c.Title)
	managementlog.SetCorrelation(ctx, "reward_campaign", id)
	outcome := managementlog.NoChange
	if count > 0 {
		if err = managementlog.AppendRelatedTx(ctx, tx, "reward_campaign", id, managementlog.Item{Action: "retry", TargetKind: "reward_campaign", TargetID: id, Outcome: managementlog.Partial, Code: "continued_by_retry"}, true); err != nil {
			return out, err
		}
		outcome = managementlog.Accepted
	}
	managementlog.SetResult(ctx, managementlog.Result{Outcome: outcome})
	return out, managementlog.Commit(ctx, tx)
}
