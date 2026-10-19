package gacha

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/limitedactivities"
)

type Config struct {
	Database *sql.DB
	Users    limitedactivities.UserAuthorizer
	Admins   limitedactivities.AdminAuthorizer
	Gate     limitedactivities.AdmissionGate
	Keys     limitedactivities.KeyDeriver
	Activity limitedactivities.ActivityRecorder
	Now      func() time.Time
}
type Service struct {
	config Config
	random randomSource
}

func New(c Config) (*Service, error) {
	if c.Database == nil || c.Users == nil || c.Admins == nil || c.Gate == nil || c.Keys == nil {
		return nil, ErrInvalid
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	return &Service{config: c, random: secureRandom}, nil
}
func (s *Service) authorize(ctx context.Context, tx *sql.Tx, user, at int64) error {
	if user <= 0 {
		return authz.ErrUnauthorized
	}
	if err := s.config.Users.AuthorizeUserMutation(ctx, tx, user); err != nil {
		return err
	}
	var admin, banned bool
	var until sql.NullInt64
	err := tx.QueryRowContext(ctx, "SELECT is_admin,is_banned,banned_until FROM users WHERE id=?", user).Scan(&admin, &banned, &until)
	if errors.Is(err, sql.ErrNoRows) {
		return authz.ErrUnauthorized
	}
	if err != nil {
		return err
	}
	if admin || banned && (!until.Valid || until.Int64 > at) {
		return authz.ErrForbidden
	}
	return nil
}
func (s *Service) now() (int64, error) {
	at := s.config.Now().Unix()
	if at < 0 || at > 253402300799-idempotency.ReplayWindowSeconds {
		return 0, ErrInvalid
	}
	return at, nil
}
func calendar(ctx context.Context, tx *sql.Tx, at int64) (day, month, zone string, err error) {
	offset, err := db.ResolveSiteTimezoneTx(ctx, tx)
	if err != nil {
		return "", "", "", err
	}
	date := time.Unix(at, 0).UTC().Add(time.Duration(offset) * time.Minute)
	sign := "+"
	if offset < 0 {
		sign = "-"
		offset = -offset
	}
	return date.Format("2006-01-02"), date.Format("2006-01"), fmt.Sprintf("UTC%s%02d:%02d", sign, offset/60, offset%60), nil
}
func points(amount *big.Int) string {
	negative := amount.Sign() < 0
	n := new(big.Int).Abs(amount)
	q, r := new(big.Int).QuoRem(n, big.NewInt(1000), new(big.Int))
	out := q.String()
	if r.Sign() != 0 {
		out += "." + strings.TrimRight(fmt.Sprintf("%03d", r.Int64()), "0")
	}
	if negative {
		out = "-" + out
	}
	return out
}
func price(value string) (ledger.Amount, error) {
	if len(value) < 1 || len(value) > 20 {
		return ledger.Amount{}, ErrInvalid
	}
	parts := strings.Split(value, ".")
	if len(parts) > 2 || parts[0] == "" || len(parts[0]) > 1 && parts[0][0] == '0' {
		return ledger.Amount{}, ErrInvalid
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
		if len(fraction) < 1 || len(fraction) > 3 {
			return ledger.Amount{}, ErrInvalid
		}
	}
	for _, v := range parts {
		for _, c := range v {
			if c < '0' || c > '9' {
				return ledger.Amount{}, ErrInvalid
			}
		}
	}
	n, err := strconv.ParseInt(parts[0]+fraction+strings.Repeat("0", 3-len(fraction)), 10, 64)
	if err != nil || n <= 0 || n > db.MaxMoneyMilli {
		return ledger.Amount{}, ErrInvalid
	}
	return ledger.AmountFromMilli(n), nil
}

type settingsSnapshot struct {
	rules        Rules
	shop         ShopSettings
	revision     int64
	entryEnabled bool
	rates        map[Rules][4]float64
}

func readSettings(ctx context.Context, tx *sql.Tx) (*settingsSnapshot, error) {
	out := &settingsSnapshot{rates: make(map[Rules][4]float64)}
	var rawRules, rawShop string
	err := tx.QueryRowContext(ctx, "SELECT rules,shop,revision,COALESCE(json_extract(shop,'$.entry_enabled'),0) FROM gacha_settings WHERE id=1").Scan(&rawRules, &rawShop, &out.revision, &out.entryEnabled)
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal([]byte(rawRules), &out.rules); err != nil {
		return nil, err
	}
	if err = json.Unmarshal([]byte(rawShop), &out.shop); err != nil {
		return nil, err
	}
	out.shop.Revision = out.revision
	return out, nil
}

func (s *settingsSnapshot) consolidatedRates(rules Rules) ([4]float64, error) {
	if rates, ok := s.rates[rules]; ok {
		return rates, nil
	}
	rates, err := ConsolidatedRates(rules)
	if err == nil {
		s.rates[rules] = rates
	}
	return rates, err
}

func (s *settingsSnapshot) permanentPool() Pool {
	return Pool{ID: "permanent", Name: "永续智能", NameEN: "Perpetual intelligence", Cover: "permanent", Kind: "permanent", Group: "permanent", Rules: s.rules, Featured: []string{}, Published: true, Revision: s.revision}
}

func readPool(ctx context.Context, tx *sql.Tx, id string, settings *settingsSnapshot) (Pool, error) {
	if id == "permanent" {
		return settings.permanentPool(), nil
	}
	var pool Pool
	var raw string
	err := tx.QueryRowContext(ctx, "SELECT config,revision FROM gacha_pools WHERE id=?", id).Scan(&raw, &pool.Revision)
	if errors.Is(err, sql.ErrNoRows) {
		return pool, ErrClosed
	}
	if err != nil {
		return pool, err
	}
	revision := pool.Revision
	if err := json.Unmarshal([]byte(raw), &pool); err != nil {
		return Pool{}, err
	}
	pool.Revision = revision
	return pool, nil
}

func readPools(ctx context.Context, tx *sql.Tx, at int64, admin bool, settings *settingsSnapshot) ([]Pool, error) {
	permanent := settings.permanentPool()
	rates, err := settings.consolidatedRates(permanent.Rules)
	if err != nil {
		return nil, err
	}
	permanent.Rates = rates
	pools := []Pool{permanent}
	rows, err := tx.QueryContext(ctx, "SELECT config,revision FROM gacha_pools ORDER BY created_at,id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		var revision int64
		var p Pool
		if err = rows.Scan(&raw, &revision); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(raw), &p); err != nil {
			return nil, err
		}
		if !admin && (!p.Published || p.EndsAt != nil && *p.EndsAt <= at) {
			continue
		}
		p.Revision = revision
		p.Rates, err = settings.consolidatedRates(p.Rules)
		if err != nil {
			return nil, err
		}
		pools = append(pools, p)
	}
	return pools, rows.Err()
}

type profile struct {
	Balance                [6]int64
	Newcomer               bool
	Day, LimitedDay, Month string
	WelcomeGranted         bool
	Used                   [2]int64
	EnteredAt              int64
}

func readProfile(ctx context.Context, tx *sql.Tx, user int64) (profile, bool, error) {
	p := profile{Newcomer: true}
	err := tx.QueryRowContext(ctx, `SELECT permanent,limited,n_shards,r_shards,sr_shards,ssr_shards,newcomer,checked_day,month,monthly_permanent,monthly_limited,entered_at,limited_checked_day,welcome_granted FROM gacha_profiles WHERE user_id=?`, user).
		Scan(&p.Balance[0], &p.Balance[1], &p.Balance[2], &p.Balance[3], &p.Balance[4], &p.Balance[5], &p.Newcomer, &p.Day, &p.Month, &p.Used[0], &p.Used[1], &p.EnteredAt, &p.LimitedDay, &p.WelcomeGranted)
	if errors.Is(err, sql.ErrNoRows) {
		return p, false, nil
	}
	return p, err == nil, err
}
func saveProfile(ctx context.Context, tx *sql.Tx, user, at int64, p profile) error {
	for _, n := range p.Balance {
		if n < 0 || n > maxBalance {
			return ErrCapacity
		}
	}
	for _, n := range p.Used {
		if n < 0 || n > maxBalance {
			return ErrCapacity
		}
	}
	_, err := tx.ExecContext(ctx, `UPDATE gacha_profiles SET permanent=?,limited=?,n_shards=?,r_shards=?,sr_shards=?,ssr_shards=?,newcomer=?,checked_day=?,month=?,monthly_permanent=?,monthly_limited=?,updated_at=?,limited_checked_day=?,welcome_granted=?,entered_at=? WHERE user_id=?`,
		p.Balance[0], p.Balance[1], p.Balance[2], p.Balance[3], p.Balance[4], p.Balance[5], p.Newcomer, p.Day, p.Month, p.Used[0], p.Used[1], at, p.LimitedDay, p.WelcomeGranted, p.EnteredAt, user)
	return err
}
func readOwned(ctx context.Context, tx *sql.Tx, user int64) (map[string]Owned, error) {
	out := map[string]Owned{}
	rows, err := tx.QueryContext(ctx, "SELECT card_id,obtained_at,source FROM gacha_owned WHERE user_id=? ORDER BY card_id", user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var v Owned
		if err = rows.Scan(&id, &v.At, &v.Source); err != nil {
			return nil, err
		}
		out[id] = v
	}
	return out, rows.Err()
}
func readPity(ctx context.Context, tx *sql.Tx, user int64) (map[string]Pity, error) {
	out := map[string]Pity{}
	rows, err := tx.QueryContext(ctx, "SELECT group_id,sr,ssr,sr_limit,ssr_limit,sr_featured,ssr_featured,ssr_misses FROM gacha_pity WHERE user_id=?", user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var p Pity
		if err = rows.Scan(&id, &p.SR, &p.SSR, &p.SRLimit, &p.SSRLimit, &p.SRFeatured, &p.SSRFeatured, &p.SSRMisses); err != nil {
			return nil, err
		}
		out[id] = p
	}
	return out, rows.Err()
}
func savePity(ctx context.Context, tx *sql.Tx, user int64, group string, p Pity) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO gacha_pity(user_id,group_id,sr,ssr,sr_limit,ssr_limit,sr_featured,ssr_featured,ssr_misses) VALUES(?,?,?,?,?,?,?,?,?)
 ON CONFLICT(user_id,group_id) DO UPDATE SET sr=excluded.sr,ssr=excluded.ssr,sr_limit=excluded.sr_limit,ssr_limit=excluded.ssr_limit,sr_featured=excluded.sr_featured,ssr_featured=excluded.ssr_featured,ssr_misses=excluded.ssr_misses`,
		user, group, p.SR, p.SSR, p.SRLimit, p.SSRLimit, p.SRFeatured, p.SSRFeatured, p.SSRMisses)
	return err
}
func (s *Service) snapshot(ctx context.Context, tx *sql.Tx, user, at int64) (Snapshot, error) {
	settings, err := readSettings(ctx, tx)
	if err != nil {
		return Snapshot{}, err
	}
	return s.snapshotWithSettings(ctx, tx, user, at, settings)
}

func (s *Service) snapshotWithSettings(ctx context.Context, tx *sql.Tx, user, at int64, settings *settingsSnapshot) (Snapshot, error) {
	out := Snapshot{Tickets: map[string]int64{}, Fragments: map[Rarity]int64{}, Claimed: []string{}, Redeemed: []string{}, MonthUsed: map[string]int64{}, ServerTime: at}
	day, month, zone, err := calendar(ctx, tx, at)
	if err != nil {
		return out, err
	}
	out.TimeZone = zone
	p, _, err := readProfile(ctx, tx, user)
	if err != nil {
		return out, err
	}
	out.Tickets["permanent"], out.Tickets["limited"] = p.Balance[0], p.Balance[1]
	for i, r := range rarities {
		out.Fragments[r] = p.Balance[i+2]
	}
	out.Newcomer = p.Newcomer
	out.CheckedIn = p.Day == day
	out.LimitedCheckedIn = p.LimitedDay == day
	if p.Month != month {
		p.Used = [2]int64{}
	}
	out.MonthUsed["permanent"], out.MonthUsed["limited"] = p.Used[0], p.Used[1]
	out.Shop = settings.shop
	out.Pools, err = readPools(ctx, tx, at, false, settings)
	if err != nil {
		return out, err
	}
	out.Owned, err = readOwned(ctx, tx, user)
	if err != nil {
		return out, err
	}
	out.VisibleThemes, err = readVisibleThemes(ctx, tx, at, out.Owned)
	if err != nil {
		return out, err
	}
	out.Pity, err = readPity(ctx, tx, user)
	if err != nil {
		return out, err
	}
	for _, pool := range out.Pools {
		p, ok := out.Pity[pool.Group]
		if !ok {
			p = freshPity(pool.Rules)
		}
		out.Pity[pool.Group] = protectPity(p, pool.Rules)
	}
	general, err := ledger.UserAssetAccount(ctx, tx, user, ledger.General)
	if err != nil {
		return out, err
	}
	game, err := ledger.UserAssetAccount(ctx, tx, user, ledger.Game)
	if err != nil {
		return out, err
	}
	out.Credits = Credits{points(general.Balance.Big()), points(game.Balance.Big())}
	var donationRaw []byte
	if err = tx.QueryRowContext(ctx, "SELECT donation_credit_mag FROM users WHERE id=?", user).Scan(&donationRaw); err != nil {
		return out, err
	}
	donation, err := db.DecodeU128(donationRaw)
	if err != nil {
		return out, err
	}
	out.Donation = points(donation.Big())
	rows, err := tx.QueryContext(ctx, "SELECT kind,target,created_at FROM gacha_claims WHERE user_id=? ORDER BY kind,target", user)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var kind, id string
		var claimedAt int64
		if err = rows.Scan(&kind, &id, &claimedAt); err != nil {
			return out, err
		}
		if kind == "theme" {
			out.Claimed = append(out.Claimed, id)
		} else {
			active, e := productClaimActive(ctx, tx, id, claimedAt, day)
			if e != nil {
				return out, e
			}
			if active {
				out.Redeemed = append(out.Redeemed, id)
			}
		}
	}
	return out, rows.Err()
}
func (s *Service) State(ctx context.Context, user int64) (Snapshot, error) {
	at, err := s.now()
	if err != nil {
		return Snapshot{}, err
	}
	tx, err := s.config.Database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Snapshot{}, err
	}
	defer tx.Rollback()
	if err = s.authorize(ctx, tx, user, at); err != nil {
		return Snapshot{}, err
	}
	out, err := s.snapshot(ctx, tx, user, at)
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}
func (s *Service) digest(input Action) ([]byte, error) {
	key, err := s.config.Keys.DeriveGenerationTwoSubkey([]byte("NonbiriAPI/gacha-request/v1"))
	if err != nil {
		return nil, err
	}
	defer clear(key)
	if len(key) != 32 {
		return nil, ErrInvalid
	}
	h := hmac.New(sha256.New, key)
	_, _ = h.Write([]byte(jsonText(input)))
	return h.Sum(nil), nil
}
