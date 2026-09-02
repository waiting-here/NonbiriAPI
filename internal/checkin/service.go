package checkin

import (
	"context"
	crand "crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"math/big"
	"reflect"
	"strings"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

const maxUnixSecond int64 = 253402300799

type ServiceConfig struct {
	Store       *db.Store
	FinalAuth   FinalTxAuthorizer
	Maintenance MaintenanceAuthorizer
	Now         func() time.Time
	Random      io.Reader
	GenerateID  func(string) (string, error)
}

type Service struct {
	database    *sql.DB
	finalAuth   FinalTxAuthorizer
	maintenance MaintenanceAuthorizer
	now         func() time.Time
	random      io.Reader
	generateID  func(string) (string, error)
}

func NewService(config ServiceConfig) (*Service, error) {
	if config.Store == nil || config.Store.DB() == nil || nilInterface(config.FinalAuth) || nilInterface(config.Maintenance) {
		return nil, errors.New("checkin: store, final authorization, and maintenance are required")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.Random == nil {
		config.Random = crand.Reader
	}
	if config.GenerateID == nil {
		config.GenerateID = db.GenerateOpaqueID
	}
	return &Service{
		database: config.Store.DB(), finalAuth: config.FinalAuth, maintenance: config.Maintenance,
		now: config.Now, random: config.Random, generateID: config.GenerateID,
	}, nil
}

func nilInterface(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

func (service *Service) decisionNow() (int64, error) {
	if service == nil || service.now == nil {
		return 0, ErrUnavailable
	}
	now := service.now()
	if now.IsZero() || now.Unix() < 0 || now.Unix() > maxUnixSecond {
		return 0, ErrUnavailable
	}
	return now.Unix(), nil
}

// Status returns the current check-in projection without writing lazy level
// promotion or any other database fact.
func (service *Service) Status(ctx context.Context, userID int64) (Status, error) {
	if service == nil || service.database == nil || ctx == nil || userID <= 0 {
		return Status{}, ErrInvalidRequest
	}
	now, err := service.decisionNow()
	if err != nil {
		return Status{}, err
	}
	tx, err := service.database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Status{}, classifyDatabase("begin status", err)
	}
	defer tx.Rollback()
	if err := service.authorize(ctx, tx, userID, now); err != nil {
		return Status{}, err
	}
	day, config, err := readSiteDayAndConfig(ctx, tx, now)
	if errors.Is(err, ErrFeatureDisabled) {
		return Status{Enabled: false}, nil
	}
	if err != nil {
		return Status{}, err
	}
	if config.mode == db.CheckinModeLevelGated {
		level, err := effectiveLevelTx(ctx, tx, userID, now, false)
		if err != nil {
			return Status{}, err
		}
		if level < 3 {
			return Status{Enabled: false}, nil
		}
	}
	wallet, err := ledger.UserAccount(ctx, tx, userID)
	if err != nil {
		return Status{}, mapLedgerError("read status account", err)
	}
	var checked int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM checkins WHERE user_id=? AND site_day=?
	)`, userID, day.siteDate).Scan(&checked); err != nil {
		return Status{}, classifyDatabase("read status row", err)
	}
	if checked != 0 && checked != 1 {
		return Status{}, ErrInvariant
	}
	return Status{
		Enabled: true, CheckedInToday: checked == 1, Balance: formatMilliPoints(wallet.Balance.Big()),
		AwardMinimum: formatMilliPoints(big.NewInt(config.awardMin)),
		AwardMaximum: formatMilliPoints(big.NewInt(config.awardMax)),
		BalanceCap:   formatMilliPoints(big.NewInt(config.balanceCap)),
	}, nil
}

// Checkin performs one business-unique daily check-in. Every authority,
// configuration, ledger, activity, and timezone-freeze decision is part of
// this one transaction.
func (service *Service) Checkin(ctx context.Context, userID int64) (Result, error) {
	if service == nil || service.database == nil || ctx == nil || userID <= 0 {
		return Result{}, ErrInvalidRequest
	}
	now, err := service.decisionNow()
	if err != nil {
		return Result{}, err
	}
	tx, err := service.database.BeginTx(ctx, nil)
	if err != nil {
		return Result{}, classifyDatabase("begin check-in", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := service.authorize(ctx, tx, userID, now); err != nil {
		return Result{}, err
	}
	day, config, err := readSiteDayAndConfig(ctx, tx, now)
	if err != nil {
		return Result{}, err
	}
	level, err := effectiveLevelTx(ctx, tx, userID, now, true)
	if err != nil {
		return Result{}, err
	}
	if config.mode == db.CheckinModeLevelGated && level < 3 {
		return Result{}, ErrFeatureDisabled
	}
	wallet, err := ledger.UserAccount(ctx, tx, userID)
	if err != nil {
		return Result{}, mapLedgerError("read check-in account", err)
	}
	if config.balanceCap > 0 && level < 3 && wallet.Balance.Big().Cmp(big.NewInt(config.balanceCap)) >= 0 {
		return Result{}, ErrBalanceCap
	}
	external, err := ledger.CodedAccount(ctx, tx, "external")
	if err != nil {
		return Result{}, mapLedgerError("read external account", err)
	}
	award, err := drawAward(service.random, config.awardMin, config.awardMax)
	if err != nil {
		return Result{}, fmt.Errorf("%w: draw award: %v", ErrUnavailable, err)
	}
	operationID, err := service.generateID("op_")
	if err != nil || !db.ValidateOpaqueID(operationID, "op_") {
		return Result{}, ErrUnavailable
	}
	firstCheckin, err := temporalTableEmpty(ctx, tx, "checkins")
	if err != nil {
		return Result{}, err
	}
	firstUserActivity, err := temporalTableEmpty(ctx, tx, "user_activity_daily")
	if err != nil {
		return Result{}, err
	}
	firstSiteActivity, err := temporalTableEmpty(ctx, tx, "site_activity_daily")
	if err != nil {
		return Result{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO checkins(
		user_id,site_day,award_milli,operation_id,created_at
	) VALUES(?,?,?,?,?)`, userID, day.siteDate, award, operationID, now); err != nil {
		var exists int
		probeErr := tx.QueryRowContext(ctx, `SELECT EXISTS(
			SELECT 1 FROM checkins WHERE user_id=? AND site_day=?
		)`, userID, day.siteDate).Scan(&exists)
		if probeErr == nil && exists == 1 {
			return Result{}, ErrAlreadyCheckedIn
		}
		return Result{}, classifyDatabase("insert check-in", err)
	}
	if err := ledger.CheckImmediateCapacity(ctx, tx, db.U128{}); err != nil {
		return Result{}, mapLedgerError("check capacity", err)
	}
	plan, err := ledger.NewCheckinAward(ledger.Meta{
		OperationID: operationID, ActorUserID: userID, CreatedAt: now,
	}, wallet.ID, external.ID, ledger.AmountFromMilli(award))
	if err != nil {
		return Result{}, mapLedgerError("build check-in award", err)
	}
	if _, err := ledger.Apply(ctx, tx, plan); err != nil {
		return Result{}, mapLedgerError("apply check-in award", err)
	}
	if err := recordCheckinActivity(ctx, tx, userID, day.activityDay, now); err != nil {
		return Result{}, err
	}
	if firstCheckin || firstUserActivity || firstSiteActivity {
		if err := freezeTimezone(ctx, tx, now); err != nil {
			return Result{}, err
		}
	}
	walletAfter, err := ledger.UserAccount(ctx, tx, userID)
	if err != nil {
		return Result{}, mapLedgerError("read awarded account", err)
	}
	if err := tx.Commit(); err != nil {
		return Result{}, classifyDatabase("commit check-in", err)
	}
	committed = true
	return Result{Award: formatMilliPoints(big.NewInt(award)), Balance: formatMilliPoints(walletAfter.Balance.Big())}, nil
}

func (service *Service) authorize(ctx context.Context, tx *sql.Tx, userID, now int64) error {
	if err := service.finalAuth.AuthorizeUserMutation(ctx, tx, userID); err != nil {
		switch {
		case errors.Is(err, resources.ErrUnauthorized):
			return ErrUnauthorized
		case errors.Is(err, resources.ErrForbidden):
			return ErrForbidden
		case errors.Is(err, resources.ErrNotFound), errors.Is(err, sql.ErrNoRows):
			return ErrNotFound
		default:
			return classifyDatabase("final user authorization", err)
		}
	}
	if err := service.maintenance.AuthorizeChatAcceptance(ctx, tx, userID, now); err != nil {
		switch {
		case errors.Is(err, maintenance.ErrMaintenanceOn):
			return ErrMaintenance
		case errors.Is(err, maintenance.ErrInvalidMutation), errors.Is(err, maintenance.ErrInvariant):
			return ErrInvariant
		default:
			return classifyDatabase("final maintenance authorization", err)
		}
	}
	return nil
}

func mapLedgerError(action string, err error) error {
	switch {
	case errors.Is(err, ledger.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, ledger.ErrCapacityExhausted):
		return ErrResourceLimit
	case errors.Is(err, ledger.ErrRetryable):
		return ErrUnavailable
	case errors.Is(err, ledger.ErrInvalidAmount), errors.Is(err, ledger.ErrInvalidPlan),
		errors.Is(err, ledger.ErrConflict), errors.Is(err, ledger.ErrInvariant):
		return fmt.Errorf("%w: %s: %v", ErrInvariant, action, err)
	default:
		return classifyDatabase(action, err)
	}
}

func classifyDatabase(action string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%w: %s: %v", ErrUnavailable, action, err)
	}
	text := strings.ToLower(err.Error())
	if strings.Contains(text, "database is locked") || strings.Contains(text, "database is busy") || strings.Contains(text, "sqlite_busy") {
		return fmt.Errorf("%w: %s: %v", ErrUnavailable, action, err)
	}
	return fmt.Errorf("%w: %s: %v", ErrInvariant, action, err)
}

func formatMilliPoints(value *big.Int) string {
	if value == nil || value.Sign() == 0 {
		return "0"
	}
	negative := value.Sign() < 0
	abs := new(big.Int).Abs(new(big.Int).Set(value))
	whole, fraction := new(big.Int), new(big.Int)
	whole.QuoRem(abs, big.NewInt(1000), fraction)
	out := whole.String()
	if fraction.Sign() != 0 {
		digits := fmt.Sprintf("%03d", fraction.Int64())
		digits = strings.TrimRight(digits, "0")
		out += "." + digits
	}
	if negative {
		return "-" + out
	}
	return out
}
