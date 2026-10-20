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
	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/game/host"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	lakeconfig "github.com/waiting-here/NonbiriAPI/internal/lakenotes/config"
	"github.com/waiting-here/NonbiriAPI/internal/lakenotes/rules"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
	"github.com/waiting-here/NonbiriAPI/internal/observability"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

type Service struct {
	database     *sql.DB
	users        resources.FinalTxAuthorizer
	admins       host.AdminAuthorizer
	gate         AdmissionGate
	keys         KeyDeriver
	activity     func(context.Context, *sql.Tx, int64, int64) error
	reward       func(context.Context, *sql.Tx, int64, string, int64) error
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
	return &Service{database: c.Database, users: c.Users, admins: c.Admins, gate: c.Gate, keys: c.Keys, activity: c.Activity, reward: c.Reward, now: c.Now, random: c.Random, seed: c.MotionSeed, budgetUsers: map[int64]int{}}, nil
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
		return s.admins.AuthorizeAdminMutation(ctx, tx)
	}
	return s.users.AuthorizeUserMutation(ctx, tx, user)
}
func settingsTx(ctx context.Context, tx *sql.Tx) (_ Settings, resultErr error) {
	finish := lakePhase(ctx, "config")
	defer func() { finish(resultErr) }()
	raw := map[string]string{}
	for _, key := range []string{game.GamesEnabledKey, lakeconfig.EnabledKey, lakeconfig.ExchangesKey} {
		var value string
		if err := tx.QueryRowContext(ctx, "SELECT value FROM site_config WHERE key=?", key).Scan(&value); err != nil {
			return Settings{}, err
		}
		raw[key] = value
	}
	wire, err := lakeconfig.Compile(raw)
	if err != nil {
		return Settings{}, err
	}
	var version int64
	if err = tx.QueryRowContext(ctx, "SELECT revision FROM config_revisions WHERE domain='games'").Scan(&version); err != nil {
		return Settings{}, err
	}
	return Settings{Revision: rev(version), Wire: wire}, nil
}
func (s *Service) qualifiedTx(ctx context.Context, tx *sql.Tx, user int64, _ int64) (Settings, error) {
	if err := s.gate.AuthorizeUserActivity(ctx, tx, user); err != nil {
		return Settings{}, err
	}
	settings, err := settingsTx(ctx, tx)
	if err == nil && !settings.Enabled {
		err = ErrClosed
	}
	return settings, err
}
func (s *Service) prepare(user int64, admin bool, key, method, route string, body any, now int64) (idempotency.BeginInput, []byte, error) {
	actor := "user"
	if admin {
		actor = "admin"
	}
	ah, e := idempotency.ActorScopeHash(actor, rev(user))
	if e != nil {
		return idempotency.BeginInput{}, nil, ErrInvalid
	}
	raw, e := idempotency.CanonicalJSON(body)
	if e != nil || len(raw) > 16384 {
		return idempotency.BeginInput{}, nil, ErrInvalid
	}
	digest, e := idempotency.RequestDigest(idempotency.DigestInput{ActorScopeHash: ah, Method: method, Route: route, Body: raw})
	if e != nil {
		return idempotency.BeginInput{}, nil, ErrInvalid
	}
	secret, e := s.keys.DeriveGenerationTwoSubkey([]byte("NonbiriAPI/lake-notes-idempotency/v1"))
	if e != nil {
		return idempotency.BeginInput{}, nil, e
	}
	defer clear(secret)
	if len(secret) != 32 {
		return idempotency.BeginInput{}, nil, ErrInvariant
	}
	h := hmac.New(sha256.New, secret)
	_, _ = h.Write(digest[:])
	copy(digest[:], h.Sum(nil))
	return idempotency.BeginInput{Scope: idempotency.ScopeLakeNotes, ActorHash: ah, Key: key, RequestHash: digest, DecisionNow: now}, raw, nil
}
func mutate[T any](ctx context.Context, s *Service, user int64, admin bool, key, method, route string, input any, fn func(*sql.Tx, time.Time, []byte) (T, error)) (_ MutationResult[T], resultErr error) {
	var out MutationResult[T]
	if _, e := idempotency.KeyHash(key); e != nil {
		return out, ErrInvalid
	}
	now, e := s.clock()
	if e != nil {
		return out, e
	}
	prepare := lakePhase(ctx, "prepare")
	prepared, inputJSON, prepareErr := s.prepare(user, admin, key, method, route, input, now.Unix())
	prepare(prepareErr)
	tx, finish, e := observability.BeginMeasuredTx(ctx, s.database, nil, lakeOperation(ctx))
	if e != nil {
		return out, e
	}
	defer func() { finish(resultErr) }()
	authorize := lakePhase(ctx, "authorize")
	e = s.authorize(ctx, tx, user, admin)
	authorize(e)
	if e != nil {
		return out, e
	}
	// Preserve authorization precedence; pure preparation cannot reserve a key.
	if prepareErr != nil {
		return out, prepareErr
	}
	idempotent := lakePhase(ctx, "idempotency")
	d, e := idempotency.Begin(ctx, tx, prepared)
	if errors.Is(e, idempotency.ErrConflict) || errors.Is(e, idempotency.ErrInProgress) {
		e = ErrConflict
	}
	idempotent(e)
	if e != nil {
		return out, e
	}
	if d.Kind == idempotency.Replay {
		if e = json.Unmarshal(d.ResponseBody, &out.Value); e != nil {
			return out, ErrInvariant
		}
		out.Replayed = true
	} else {
		out.Value, e = fn(tx, now, inputJSON)
		if e != nil {
			return out, e
		}
		encode := lakePhase(ctx, "encode")
		raw, e := json.Marshal(out.Value)
		encode(e)
		if e != nil || len(raw) > 65536 {
			return out, ErrCapacity
		}
		write := lakePhase(ctx, "write")
		e = idempotency.Complete(ctx, tx, d, 200, raw)
		write(e)
		if e != nil {
			return out, e
		}
		if !admin && s.activity != nil {
			activity := lakePhase(ctx, "activity")
			e = s.activity(ctx, tx, user, now.Unix())
			activity(e)
			if e != nil {
				return out, e
			}
		}
	}
	commit := lakePhase(ctx, "commit")
	e = tx.Commit()
	commit(e)
	return out, e
}

type profileRow struct {
	profile  rules.Profile
	revision int64
}

func profileTx(ctx context.Context, tx *sql.Tx, user int64, create bool, now int64) (_ profileRow, resultErr error) {
	finish := lakePhase(ctx, "read")
	defer func() { finish(resultErr) }()
	var row profileRow
	var raw, coinRaw []byte
	var version int
	e := tx.QueryRowContext(ctx, "SELECT revision,storage_version,coin_mag,profile FROM lake_notes_profiles WHERE user_id=?", user).Scan(&row.revision, &version, &coinRaw, &raw)
	if errors.Is(e, sql.ErrNoRows) {
		row = profileRow{rules.InitialProfile(), 1}
		if !create {
			return row, nil
		}
		raw, e = rules.EncodeProfile(row.profile)
		if e != nil {
			return row, e
		}
		_, e = tx.ExecContext(ctx, "INSERT INTO lake_notes_profiles(user_id,revision,rules_id,storage_version,coin_mag,profile,updated_at) VALUES(?,1,?,?,zeroblob(16),?,?)", user, rules.RulesID, profileStorageVersion, raw, now)
		return row, e
	}
	if e != nil {
		return row, e
	}
	decode := lakePhase(ctx, "decode")
	row.profile, e = decodeStoredProfile(version, raw)
	decode(e)
	if e != nil {
		return row, ErrInvariant
	}
	coin, e := db.DecodeU128(coinRaw)
	if e != nil || coin.Decimal() != string(row.profile.Coins) {
		return row, ErrInvariant
	}
	return row, nil
}
func saveProfileTx(ctx context.Context, tx *sql.Tx, user int64, row *profileRow, now int64) error {
	if row.revision == math.MaxInt64 {
		return ErrCapacity
	}
	encode := lakePhase(ctx, "encode")
	raw, e := rules.EncodeProfile(row.profile)
	encode(e)
	if e != nil {
		return e
	}
	coin, e := db.ParseU128Decimal(string(row.profile.Coins))
	if e != nil {
		return ErrInvariant
	}
	write := lakePhase(ctx, "write")
	r, e := tx.ExecContext(ctx, "UPDATE lake_notes_profiles SET profile=?,coin_mag=?,rules_id=?,storage_version=?,revision=revision+1,updated_at=? WHERE user_id=? AND revision=?", raw, db.EncodeU128(coin), rules.RulesID, profileStorageVersion, now, user, row.revision)
	write(e)
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

// Inputs are valid only in this transaction after the caller's last mutation.
// Rules/save update row and cast in place; ledger writes invalidate wallet.
// Reward hooks may change any of these values and must use a fresh projection.
type profileProjection struct {
	row          *profileRow
	wallet       *Wallet
	settings     *Settings
	readonly     bool
	current      *castRow
	currentKnown bool
}

func (s *Service) profileViewTx(ctx context.Context, tx *sql.Tx, user int64, now time.Time) (ProfileView, error) {
	return s.profileViewFromTx(ctx, tx, user, now, profileProjection{})
}

func (s *Service) profileViewFromTx(ctx context.Context, tx *sql.Tx, user int64, now time.Time, known profileProjection) (_ ProfileView, resultErr error) {
	finish := lakePhase(ctx, "projection")
	defer func() { finish(resultErr) }()
	var row profileRow
	var e error
	if known.row != nil {
		row = *known.row
	} else {
		row, e = profileTx(ctx, tx, user, false, now.Unix())
		if e != nil {
			return ProfileView{}, e
		}
	}
	var w Wallet
	if known.wallet != nil {
		w = *known.wallet
	} else {
		w, e = walletTx(ctx, tx, user)
		if e != nil {
			return ProfileView{}, e
		}
	}
	out := ProfileView{Revision: rev(row.revision), RulesID: rules.RulesID, Profile: row.profile, Wallet: w}
	if known.settings != nil {
		out.Settings, out.Readonly = *known.settings, known.readonly
	} else {
		out.Settings, e = s.qualifiedTx(ctx, tx, user, now.Unix())
		if e != nil {
			if !closedError(e) {
				return out, e
			}
			out.Readonly = true
		}
	}

	// A closed admission gate may return before settings are read. The view
	// still needs those settings, but an open gate already read this snapshot.
	if out.Settings.Revision == "" {
		out.Settings, e = settingsTx(ctx, tx)
		if e != nil {
			return out, e
		}
	}
	c := known.current
	if !known.currentKnown {
		value, err := currentCastTx(ctx, tx, user)
		if err == nil {
			c = &value
		} else if !errors.Is(err, ErrNotFound) {
			return out, err
		}
	}
	if c != nil {
		v := c.view(row.revision)
		if out.Readonly && !c.state.Terminal() {
			v.Readonly = true
			v.RecoveryAction = "closed"
		}
		out.Cast = &v
	}
	return out, nil
}
func (s *Service) Profile(ctx context.Context, user int64) (_ ProfileView, resultErr error) {
	ctx = context.WithValue(ctx, lakeOperationContextKey{}, "lake_profile")
	now, e := s.clock()
	if e != nil {
		return ProfileView{}, e
	}
	tx, finish, e := observability.BeginMeasuredTx(ctx, s.database, nil, "lake_profile")
	if e != nil {
		return ProfileView{}, e
	}
	defer func() { finish(resultErr) }()
	if e = s.authorize(ctx, tx, user, false); e != nil {
		return ProfileView{}, e
	}
	settings, gateErr := s.qualifiedTx(ctx, tx, user, now.Unix())
	if gateErr != nil && !closedError(gateErr) {
		return ProfileView{}, gateErr
	}
	if e = s.expireUserTx(ctx, tx, user, now, gateErr != nil); e != nil {
		return ProfileView{}, e
	}
	out, e := s.profileViewFromTx(ctx, tx, user, now, profileProjection{settings: &settings, readonly: gateErr != nil})
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
	return mutate(ctx, s, user, false, key, "POST", baseRoute+"/actions", input, func(tx *sql.Tx, now time.Time, _ []byte) (ActionResult, error) {
		settings, e := s.qualifiedTx(ctx, tx, user, now.Unix())
		if e != nil {
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
		// ApplyAction/saveProfile change only this row; the verified absence of
		// a current cast and the admission/config snapshot remain valid.
		v, e := s.profileViewFromTx(ctx, tx, user, now, profileProjection{row: &row, settings: &settings, currentKnown: true})
		return ActionResult{v, action.CoinDelta}, e
	})
}

func closedError(e error) bool {
	return errors.Is(e, ErrClosed) || errors.Is(e, maintenance.ErrMaintenanceOn) || errors.Is(e, authz.ErrForbidden) || errors.Is(e, resources.ErrForbidden) || errors.Is(e, resources.ErrMaintenance)
}
