package limitedactivities

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/strictjson"
)

type Config struct {
	Database *sql.DB
	Users    UserAuthorizer
	Admins   AdminAuthorizer
	Gate     AdmissionGate
	Keys     KeyDeriver
	Registry *Registry
	Activity ActivityRecorder
	Now      func() time.Time
}
type Service struct {
	database *sql.DB
	users    UserAuthorizer
	admins   AdminAuthorizer
	gate     AdmissionGate
	keys     KeyDeriver
	registry *Registry
	activity ActivityRecorder
	now      func() time.Time
}

func New(c Config) (*Service, error) {
	if c.Database == nil || nilInterface(c.Users) || nilInterface(c.Admins) || nilInterface(c.Gate) || nilInterface(c.Keys) || c.Registry == nil {
		return nil, ErrInvalid
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	return &Service{c.Database, c.Users, c.Admins, c.Gate, c.Keys, c.Registry, c.Activity, c.Now}, nil
}

func (s *Service) decisionNow() (int64, error) {
	if s == nil || s.now == nil {
		return 0, ErrInvalid
	}
	n := s.now().Unix()
	if n < 0 || n > maxUnix-idempotency.ReplayWindowSeconds {
		return 0, ErrInvalid
	}
	return n, nil
}

func (s *Service) authorizeUserTx(ctx context.Context, tx *sql.Tx, user int64, now int64) error {
	if s == nil || tx == nil || ctx == nil || user <= 0 {
		return authz.ErrUnauthorized
	}
	if err := s.users.AuthorizeUserMutation(ctx, tx, user); err != nil {
		return err
	}
	var admin, banned int
	var until sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT is_admin,is_banned,banned_until FROM users WHERE id=?`, user).Scan(&admin, &banned, &until)
	if errors.Is(err, sql.ErrNoRows) {
		return authz.ErrUnauthorized
	}
	if err != nil {
		return err
	}
	if admin != 0 || banned != 0 && (!until.Valid || until.Int64 > now) {
		return authz.ErrForbidden
	}
	return nil
}

type configRow struct {
	visible, paused bool
	start, end      *int64
	revision        int64
	module          json.RawMessage
}

func readConfig(ctx context.Context, tx *sql.Tx, key string) (configRow, error) {
	var c configRow
	var start, end sql.NullInt64
	var raw string
	err := tx.QueryRowContext(ctx, `SELECT visible,starts_at,ends_at,paused,module_config,revision FROM limited_activity_configs WHERE activity_key=?`, key).Scan(&c.visible, &start, &end, &c.paused, &raw, &c.revision)
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrNotFound
	}
	if err != nil {
		return c, err
	}
	if start.Valid {
		n := start.Int64
		c.start = &n
	}
	if end.Valid {
		n := end.Int64
		c.end = &n
	}
	c.module = json.RawMessage(raw)
	return c, nil
}

func (s *Service) detailTx(ctx context.Context, tx *sql.Tx, key string, now int64) (Detail, error) {
	d, err := s.registry.lookup(key)
	if err != nil {
		return Detail{}, err
	}
	c, err := readConfig(ctx, tx, key)
	if err != nil {
		return Detail{}, err
	}
	module, err := d.configuration.PublicTx(ctx, tx, key, c.module)
	if err != nil {
		return Detail{}, err
	}
	ready, err := d.ready(ctx, tx)
	if err != nil {
		return Detail{}, err
	}
	status := "open"
	switch {
	case c.start == nil || c.end == nil:
		status = "unconfigured"
	case c.paused:
		status = "paused"
	case !ready:
		status = "unavailable"
	case now < *c.start:
		status = "scheduled"
	case now >= *c.end:
		status = "ended"
	}
	return Detail{key, d.name, c.visible, c.start, c.end, c.paused, strconv.FormatInt(c.revision, 10), status, module}, nil
}

// CheckAdmissionTx is only for new economic intentions. Continuing accepted
// work must not call it: natural closing time does not cancel that work.
func (s *Service) CheckAdmissionTx(ctx context.Context, tx *sql.Tx, user int64, key string, now int64) (Detail, error) {
	if now < 0 || now > maxUnix {
		return Detail{}, ErrInvalid
	}
	if err := s.authorizeUserTx(ctx, tx, user, now); err != nil {
		return Detail{}, err
	}
	if err := s.gate.AuthorizeUserActivity(ctx, tx, user); err != nil {
		return Detail{}, err
	}
	d, err := s.detailTx(ctx, tx, key, now)
	if err != nil {
		return Detail{}, err
	}
	if d.Status != "open" {
		return Detail{}, ErrClosed
	}
	return d, nil
}

func (s *Service) List(ctx context.Context, user int64) ([]Detail, error) {
	now, err := s.decisionNow()
	if err != nil {
		return nil, err
	}
	tx, err := s.database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = s.authorizeUserTx(ctx, tx, user, now); err != nil {
		return nil, err
	}
	items := []Detail{}
	for _, key := range s.registry.keys {
		d, e := s.detailTx(ctx, tx, key, now)
		if e != nil {
			return nil, e
		}
		if d.Visible {
			items = append(items, d)
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return items, nil
}

func (s *Service) Detail(ctx context.Context, user int64, key string) (Detail, error) {
	now, err := s.decisionNow()
	if err != nil {
		return Detail{}, err
	}
	tx, err := s.database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Detail{}, err
	}
	defer tx.Rollback()
	if err = s.authorizeUserTx(ctx, tx, user, now); err != nil {
		return Detail{}, err
	}
	d, err := s.detailTx(ctx, tx, key, now)
	if err != nil {
		return Detail{}, err
	}
	if err = tx.Commit(); err != nil {
		return Detail{}, err
	}
	return d, nil
}

func (s *Service) Wallet(ctx context.Context, user int64) (Wallet, error) {
	now, err := s.decisionNow()
	if err != nil {
		return Wallet{}, err
	}
	tx, err := s.database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Wallet{}, err
	}
	defer tx.Rollback()
	if err = s.authorizeUserTx(ctx, tx, user, now); err != nil {
		return Wallet{}, err
	}
	w, err := walletTx(ctx, tx, user)
	if err != nil {
		return Wallet{}, err
	}
	if err = tx.Commit(); err != nil {
		return Wallet{}, err
	}
	return w, nil
}

func walletTx(ctx context.Context, tx *sql.Tx, user int64) (Wallet, error) {
	w := Wallet{"0", "0", "0"}
	for asset, target := range map[ledger.Asset]*string{ledger.General: &w.General, ledger.SketchPaper: &w.Paper, ledger.SketchBrush: &w.Brush} {
		a, err := ledger.UserAssetAccount(ctx, tx, user, asset)
		if errors.Is(err, ledger.ErrNotFound) && asset.IsActivity() {
			continue
		}
		if err != nil {
			return Wallet{}, err
		}
		if asset.IsActivity() {
			q, r := new(big.Int).QuoRem(a.Balance.Big(), big.NewInt(1000), new(big.Int))
			if r.Sign() != 0 || q.Sign() < 0 {
				return Wallet{}, ErrInvariant
			}
			*target = q.String()
		} else {
			*target = points(a.Balance.Big())
		}
	}
	return w, nil
}

func parsePrice(text string) (int64, error) {
	if len(text) < 1 || len(text) > 20 {
		return 0, ErrInvalid
	}
	parts := strings.Split(text, ".")
	if len(parts) > 2 || parts[0] == "" || len(parts[0]) > 1 && parts[0][0] == '0' {
		return 0, ErrInvalid
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
		if len(fraction) < 1 || len(fraction) > 3 {
			return 0, ErrInvalid
		}
	}
	for _, part := range parts {
		for _, ch := range part {
			if ch < '0' || ch > '9' {
				return 0, ErrInvalid
			}
		}
	}
	n, err := strconv.ParseInt(parts[0]+fraction+strings.Repeat("0", 3-len(fraction)), 10, 64)
	if err != nil || n < 1 || n > db.MaxMoneyMilli {
		return 0, ErrInvalid
	}
	return n, nil
}

func points(milli *big.Int) string {
	negative := milli.Sign() < 0
	n := new(big.Int).Abs(milli)
	q, r := new(big.Int).QuoRem(n, big.NewInt(1000), new(big.Int))
	result := q.String()
	if r.Sign() != 0 {
		fraction := strconv.FormatInt(r.Int64()+1000, 10)[1:]
		result += "." + strings.TrimRight(fraction, "0")
	}
	if negative {
		result = "-" + result
	}
	return result
}

func validateSettings(s ExchangeSettings) (ExchangeSettings, int64, int64, error) {
	p, e := parsePrice(s.PaperPrice)
	if e != nil {
		return s, 0, 0, e
	}
	b, e := parsePrice(s.BrushPrice)
	if e != nil {
		return s, 0, 0, e
	}
	cap, e := db.ParseU128Decimal(s.BrushCap)
	if e != nil || cap.Decimal() != s.BrushCap {
		return s, 0, 0, ErrInvalid
	}
	s.PaperPrice, s.BrushPrice = points(big.NewInt(p)), points(big.NewInt(b))
	return s, p, b, nil
}

func readSupply(ctx context.Context, tx *sql.Tx, key string, settings ExchangeSettings) (ExchangeSupply, error) {
	var totalRaw, capRaw []byte
	if err := tx.QueryRowContext(ctx, `SELECT total_exchanged_mag,cap_mag FROM activity_exchange_state WHERE activity_key=? AND asset_type='sketch_brush'`, key).Scan(&totalRaw, &capRaw); err != nil {
		return ExchangeSupply{}, err
	}
	total, err := db.DecodeU128(totalRaw)
	if err != nil {
		return ExchangeSupply{}, ErrInvariant
	}
	cap, err := db.DecodeU128(capRaw)
	if err != nil || cap.Decimal() != settings.BrushCap {
		return ExchangeSupply{}, ErrInvariant
	}
	remaining := new(big.Int).Sub(cap.Big(), total.Big())
	if remaining.Sign() < 0 {
		remaining.SetInt64(0)
	}
	return ExchangeSupply{settings.PaperPrice, settings.BrushPrice, cap.Decimal(), total.Decimal(), remaining.String()}, nil
}

func strictJSON(raw []byte, out any) error {
	if err := strictjson.ValidateObject(raw); err != nil {
		return ErrInvalid
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return ErrInvalid
	}
	var tail any
	if err := decoder.Decode(&tail); !errors.Is(err, io.EOF) {
		return ErrInvalid
	}
	return nil
}
