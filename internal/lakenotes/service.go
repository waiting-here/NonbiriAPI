package lakenotes

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"sync"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/lakenotes/rules"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/limitedactivities"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
)

type Service struct {
	database     *sql.DB
	users        limitedactivities.UserAuthorizer
	admins       limitedactivities.AdminAuthorizer
	gate         limitedactivities.AdmissionGate
	keys         limitedactivities.KeyDeriver
	activity     limitedactivities.ActivityRecorder
	now          func() time.Time
	random       rules.Random53
	seed         func() (uint32, error)
	budgetMu     sync.Mutex
	budgetSecond int64
	budgetTotal  int
	budgetUsers  map[int64]int
}

func New(c Config) (*Service, error) {
	if c.Database == nil || c.Users == nil || c.Admins == nil || c.Gate == nil || c.Keys == nil {
		return nil, ErrInvalid
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.Random == nil {
		c.Random = rules.CryptoRandom{}
	}
	if c.MotionSeed == nil {
		c.MotionSeed = rules.SampleMotionSeed
	}
	return &Service{database: c.Database, users: c.Users, admins: c.Admins, gate: c.Gate, keys: c.Keys, activity: c.Activity, now: c.Now, random: c.Random, seed: c.MotionSeed, budgetUsers: map[int64]int{}}, nil
}
func revision(s string, zero bool) (int64, error) {
	n, e := strconv.ParseInt(s, 10, 64)
	if e != nil || n < 0 || !zero && n < 1 || n == math.MaxInt64 || strconv.FormatInt(n, 10) != s {
		return 0, ErrInvalid
	}
	return n, nil
}
func rev(n int64) string { return strconv.FormatInt(n, 10) }
func (s *Service) clock() (time.Time, error) {
	n := s.now()
	if n.Unix() < 0 || n.Unix() > maxUnix-idempotency.ReplayWindowSeconds || n.UnixNano() < 0 {
		return time.Time{}, ErrInvalid
	}
	return n, nil
}
func (s *Service) authorize(ctx context.Context, tx *sql.Tx, user int64, admin bool) error {
	if user <= 0 {
		return authz.ErrUnauthorized
	}
	if admin {
		return s.admins.AuthorizeAdmin(ctx, tx, user)
	}
	return s.users.AuthorizeUserMutation(ctx, tx, user)
}
func (s *Service) openTx(ctx context.Context, tx *sql.Tx, user int64, now int64) (Period, error) {
	if e := s.gate.AuthorizeUserActivity(ctx, tx, user); e != nil {
		return Period{}, e
	}
	var visible, paused bool
	var starts, ends sql.NullInt64
	e := tx.QueryRowContext(ctx, "SELECT visible,paused,starts_at,ends_at FROM limited_activity_configs WHERE activity_key=?", Key).Scan(&visible, &paused, &starts, &ends)
	if errors.Is(e, sql.ErrNoRows) {
		return Period{}, ErrClosed
	}
	if e != nil {
		return Period{}, e
	}
	if !visible || paused || starts.Valid && now < starts.Int64 || ends.Valid && now >= ends.Int64 {
		return Period{}, ErrClosed
	}
	p, e := currentPeriodTx(ctx, tx, now)
	if errors.Is(e, ErrNotFound) {
		return Period{}, ErrClosed
	}
	return p, e
}
func (s *Service) qualifiedTx(ctx context.Context, tx *sql.Tx, user int64, now int64) (Period, error) {
	p, e := s.openTx(ctx, tx, user, now)
	if e != nil {
		return p, e
	}
	_, e = entryReceiptTx(ctx, tx, user, p.ID)
	if errors.Is(e, ErrNotFound) {
		return p, ErrClosed
	}
	return p, e
}
func (s *Service) begin(ctx context.Context, tx *sql.Tx, user int64, admin bool, key, method, route string, body any, now int64) (idempotency.Decision, error) {
	actor := "user"
	if admin {
		actor = "admin"
	}
	ah, e := idempotency.ActorScopeHash(actor, rev(user))
	if e != nil {
		return idempotency.Decision{}, ErrInvalid
	}
	raw, e := idempotency.CanonicalJSON(body)
	if e != nil || len(raw) > 16384 {
		return idempotency.Decision{}, ErrInvalid
	}
	digest, e := idempotency.RequestDigest(idempotency.DigestInput{ActorScopeHash: ah, Method: method, Route: route, Body: raw})
	if e != nil {
		return idempotency.Decision{}, ErrInvalid
	}
	secret, e := s.keys.DeriveGenerationTwoSubkey([]byte("NonbiriAPI/lake-notes-idempotency/v1"))
	if e != nil {
		return idempotency.Decision{}, e
	}
	defer clear(secret)
	if len(secret) != 32 {
		return idempotency.Decision{}, ErrInvariant
	}
	h := hmac.New(sha256.New, secret)
	_, _ = h.Write(digest[:])
	copy(digest[:], h.Sum(nil))
	d, e := idempotency.Begin(ctx, tx, idempotency.BeginInput{Scope: idempotency.ScopeLakeNotes, ActorHash: ah, Key: key, RequestHash: digest, DecisionNow: now})
	if errors.Is(e, idempotency.ErrConflict) || errors.Is(e, idempotency.ErrInProgress) {
		e = ErrConflict
	}
	return d, e
}
func mutate[T any](ctx context.Context, s *Service, user int64, admin bool, key, method, route string, input any, fn func(*sql.Tx, time.Time) (T, error)) (MutationResult[T], error) {
	var out MutationResult[T]
	if _, e := idempotency.KeyHash(key); e != nil {
		return out, ErrInvalid
	}
	now, e := s.clock()
	if e != nil {
		return out, e
	}
	tx, e := s.database.BeginTx(ctx, nil)
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	if e = s.authorize(ctx, tx, user, admin); e != nil {
		return out, e
	}
	d, e := s.begin(ctx, tx, user, admin, key, method, route, input, now.Unix())
	if e != nil {
		return out, e
	}
	if d.Kind == idempotency.Replay {
		if e = json.Unmarshal(d.ResponseBody, &out.Value); e != nil {
			return out, ErrInvariant
		}
		out.Replayed = true
	} else {
		out.Value, e = fn(tx, now)
		if e != nil {
			return out, e
		}
		raw, e := json.Marshal(out.Value)
		if e != nil || len(raw) > 65536 {
			return out, ErrCapacity
		}
		if e = idempotency.Complete(ctx, tx, d, 200, raw); e != nil {
			return out, e
		}
		if !admin && s.activity != nil {
			if e = s.activity.RecordLimitedActivityTx(ctx, tx, user, now.Unix()); e != nil {
				return out, e
			}
		}
	}
	return out, tx.Commit()
}

type profileRow struct {
	profile  rules.Profile
	revision int64
	rulesID  string
}

func profileTx(ctx context.Context, tx *sql.Tx, user int64, create bool, now int64) (profileRow, error) {
	var row profileRow
	var raw, coinRaw []byte
	e := tx.QueryRowContext(ctx, "SELECT revision,rules_id,coin_mag,profile FROM lake_notes_profiles WHERE user_id=?", user).Scan(&row.revision, &row.rulesID, &coinRaw, &raw)
	if errors.Is(e, sql.ErrNoRows) {
		row = profileRow{rules.InitialProfile(), 1, rules.RulesID}
		if !create {
			return row, nil
		}
		raw, e = rules.EncodeProfile(row.profile)
		if e != nil {
			return row, e
		}
		_, e = tx.ExecContext(ctx, "INSERT INTO lake_notes_profiles(user_id,revision,rules_id,coin_mag,profile,updated_at) VALUES(?,1,?,zeroblob(16),?,?)", user, rules.RulesID, raw, now)
		return row, e
	}
	if e != nil {
		return row, e
	}
	row.profile, e = rules.DecodeProfile(raw)
	if e != nil {
		return row, ErrInvariant
	}
	coin, e := db.DecodeU128(coinRaw)
	if e != nil || coin.Decimal() != string(row.profile.Coins) || row.rulesID != rules.RulesID {
		return row, ErrInvariant
	}
	return row, nil
}
func saveProfileTx(ctx context.Context, tx *sql.Tx, user int64, row *profileRow, now int64) error {
	if row.revision == math.MaxInt64 {
		return ErrCapacity
	}
	raw, e := rules.EncodeProfile(row.profile)
	if e != nil {
		return e
	}
	coin, e := db.ParseU128Decimal(string(row.profile.Coins))
	if e != nil {
		return ErrInvariant
	}
	r, e := tx.ExecContext(ctx, "UPDATE lake_notes_profiles SET profile=?,coin_mag=?,revision=revision+1,updated_at=? WHERE user_id=? AND revision=?", raw, db.EncodeU128(coin), now, user, row.revision)
	if e != nil {
		return e
	}
	n, e := r.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return ErrConflict
	}
	row.revision++
	return nil
}
func walletTx(ctx context.Context, tx *sql.Tx, user int64) (Wallet, error) {
	w := Wallet{"0", "0"}
	for a, dest := range map[ledger.Asset]*string{ledger.General: &w.GeneralMilli, ledger.Game: &w.GameMilli} {
		v, e := ledger.UserAssetAccount(ctx, tx, user, a)
		if e != nil {
			return w, e
		}
		*dest = v.Balance.Decimal()
	}
	return w, nil
}
func (s *Service) profileViewTx(ctx context.Context, tx *sql.Tx, user int64, now time.Time) (ProfileView, error) {
	row, e := profileTx(ctx, tx, user, false, now.Unix())
	if e != nil {
		return ProfileView{}, e
	}
	w, e := walletTx(ctx, tx, user)
	if e != nil {
		return ProfileView{}, e
	}
	out := ProfileView{Revision: rev(row.revision), RulesID: row.rulesID, Profile: row.profile, Wallet: w}
	if _, e = s.qualifiedTx(ctx, tx, user, now.Unix()); e != nil {
		if !closedError(e) {
			return out, e
		}
		out.Readonly = true
	}

	p, e := currentPeriodTx(ctx, tx, now.Unix())
	if e == nil {
		out.Period = &p
		r, e := entryReceiptTx(ctx, tx, user, p.ID)
		if e == nil {
			out.Entitlement = &r
		} else if !errors.Is(e, ErrNotFound) {
			return out, e
		}
	} else if !errors.Is(e, ErrNotFound) {
		return out, e
	}
	c, e := currentCastTx(ctx, tx, user)
	if e == nil {
		v := c.view(row.revision)
		if out.Readonly && !c.state.Terminal() {
			v.Readonly = true
			v.RecoveryAction = "closed"
		}
		out.Cast = &v
	} else if !errors.Is(e, ErrNotFound) {
		return out, e
	}
	return out, nil
}
func (s *Service) Profile(ctx context.Context, user int64) (ProfileView, error) {
	now, e := s.clock()
	if e != nil {
		return ProfileView{}, e
	}
	tx, e := s.database.BeginTx(ctx, nil)
	if e != nil {
		return ProfileView{}, e
	}
	defer tx.Rollback()
	if e = s.authorize(ctx, tx, user, false); e != nil {
		return ProfileView{}, e
	}
	if e = s.expireUserTx(ctx, tx, user, now); e != nil {
		return ProfileView{}, e
	}
	out, e := s.profileViewTx(ctx, tx, user, now)
	if e != nil {
		return out, e
	}
	return out, tx.Commit()
}
func (s *Service) Action(ctx context.Context, user int64, key string, input ActionInput) (MutationResult[ActionResult], error) {
	expected, e := revision(input.ExpectedProfileRevision, false)
	if e != nil {
		return MutationResult[ActionResult]{}, e
	}
	return mutate(ctx, s, user, false, key, "POST", baseRoute+"/actions", input, func(tx *sql.Tx, now time.Time) (ActionResult, error) {
		if _, e := s.qualifiedTx(ctx, tx, user, now.Unix()); e != nil {
			return ActionResult{}, e
		}
		if _, e := currentCastTx(ctx, tx, user); e == nil {
			return ActionResult{}, ErrConflict
		} else if !errors.Is(e, ErrNotFound) {
			return ActionResult{}, e
		}
		row, e := profileTx(ctx, tx, user, true, now.Unix())
		if e != nil {
			return ActionResult{}, e
		}
		if row.revision != expected {
			return ActionResult{}, ErrConflict
		}
		action, e := rules.ApplyAction(row.profile, input.Action)
		if e != nil {
			return ActionResult{}, e
		}
		row.profile = action.Profile
		if e = saveProfileTx(ctx, tx, user, &row, now.Unix()); e != nil {
			return ActionResult{}, e
		}
		v, e := s.profileViewTx(ctx, tx, user, now)
		return ActionResult{v, action.CoinDelta}, e
	})
}

func closedError(e error) bool {
	return errors.Is(e, ErrClosed) || errors.Is(e, maintenance.ErrMaintenanceOn) || errors.Is(e, authz.ErrForbidden)
}
