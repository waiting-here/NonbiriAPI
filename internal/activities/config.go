package activities

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
)

const (
	configActivitiesEnabled = "activities_enabled"
	configWelfareEnabled    = "activity_welfare_enabled"
	configWelfareThreshold  = "activity_welfare_threshold_milli"
	configWelfareCap        = "activity_welfare_cap_milli"
	configThursdayEnabled   = "activity_thursday_enabled"
	configSiteTimezone      = "site_timezone_offset_minutes"
	configTimezoneLocked    = "site_timezone_offset_locked"
)

type activityConfig struct {
	revision         int64
	masterEnabled    bool
	welfareEnabled   bool
	welfareThreshold int64
	welfareCap       int64
	thursdayEnabled  bool
	timezoneSet      bool
	timezoneMinutes  int
}

func readActivityConfigTx(ctx context.Context, tx *sql.Tx) (activityConfig, error) {
	if ctx == nil || tx == nil {
		return activityConfig{}, ErrUnavailable
	}
	var config activityConfig
	if err := tx.QueryRowContext(ctx, `SELECT revision FROM config_revisions WHERE domain='activities'`).Scan(&config.revision); err != nil {
		return activityConfig{}, classifyDatabaseError("read activities configuration revision", err)
	}
	if config.revision < 1 {
		return activityConfig{}, ErrInvariant
	}
	keys := []string{
		configActivitiesEnabled, configWelfareEnabled, configWelfareThreshold,
		configWelfareCap, configThursdayEnabled, configSiteTimezone,
	}
	values := make(map[string]string, len(keys))
	for _, key := range keys {
		var value string
		if err := tx.QueryRowContext(ctx, `SELECT value FROM site_config WHERE key=?`, key).Scan(&value); errors.Is(err, sql.ErrNoRows) && key == configSiteTimezone {
			value = ""
		} else if err != nil {
			return activityConfig{}, classifyDatabaseError("read activity configuration", err)
		}
		values[key] = value
	}
	var ok bool
	if config.masterEnabled, ok = parseConfigBool(values[configActivitiesEnabled]); !ok {
		return activityConfig{}, ErrInvariant
	}
	if config.welfareEnabled, ok = parseConfigBool(values[configWelfareEnabled]); !ok {
		return activityConfig{}, ErrInvariant
	}
	if config.thursdayEnabled, ok = parseConfigBool(values[configThursdayEnabled]); !ok {
		return activityConfig{}, ErrInvariant
	}
	if config.welfareThreshold, ok = parseStoredConfigMilli(values[configWelfareThreshold]); !ok {
		return activityConfig{}, ErrInvariant
	}
	if config.welfareCap, ok = parseStoredConfigMilli(values[configWelfareCap]); !ok {
		return activityConfig{}, ErrInvariant
	}
	if values[configSiteTimezone] != "" {
		offset, err := strconv.Atoi(values[configSiteTimezone])
		if err != nil || strconv.Itoa(offset) != values[configSiteTimezone] || !db.ValidSiteTimezoneOffset(offset) {
			return activityConfig{}, ErrInvariant
		}
		config.timezoneSet = true
		config.timezoneMinutes = offset
	}
	if !validActivityConfig(config) {
		return activityConfig{}, ErrInvariant
	}
	return config, nil
}

func parseConfigBool(value string) (bool, bool) {
	switch value {
	case "0":
		return false, true
	case "1":
		return true, true
	default:
		return false, false
	}
}

func parseStoredConfigMilli(value string) (int64, bool) {
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 || parsed > db.MaxMoneyMilli || strconv.FormatInt(parsed, 10) != value {
		return 0, false
	}
	return parsed, true
}

func validActivityConfig(config activityConfig) bool {
	if config.revision < 1 || config.welfareThreshold < 0 || config.welfareThreshold > db.MaxMoneyMilli ||
		config.welfareCap < 0 || config.welfareCap > db.MaxMoneyMilli ||
		config.timezoneSet && !db.ValidSiteTimezoneOffset(config.timezoneMinutes) {
		return false
	}
	if !config.masterEnabled {
		return true
	}
	return config.timezoneSet
}

func projectActivitiesConfig(config activityConfig) ActivitiesConfig {
	return ActivitiesConfig{
		Revision: strconv.FormatInt(config.revision, 10), MasterEnabled: config.masterEnabled,
		Welfare: WelfareConfig{
			Enabled: config.welfareEnabled, Threshold: formatMilliPointsInt64(config.welfareThreshold),
			Cap: formatMilliPointsInt64(config.welfareCap),
		},
		Thursday: ThursdayConfig{Enabled: config.thursdayEnabled},
	}
}

func (r *Repository) GetActivitiesConfig(ctx context.Context) (ActivitiesConfig, error) {
	if r == nil || ctx == nil {
		return ActivitiesConfig{}, ErrUnavailable
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return ActivitiesConfig{}, classifyDatabaseError("begin configuration read", err)
	}
	defer tx.Rollback()
	config, err := readActivityConfigTx(ctx, tx)
	if err != nil {
		return ActivitiesConfig{}, err
	}
	if err := tx.Commit(); err != nil {
		return ActivitiesConfig{}, classifyDatabaseError("commit configuration read", err)
	}
	return projectActivitiesConfig(config), nil
}

func (r *Repository) PatchActivitiesConfig(ctx context.Context, adminID int64, mutation ControlMutation, patch ActivitiesConfigPatch) (MutationResult[ActivitiesConfig], PublishFacts, error) {
	if patch.ExpectedRevision < 1 || patch.MasterEnabled == nil && patch.Welfare == nil && patch.Thursday == nil ||
		patch.Welfare != nil && patch.Welfare.Enabled == nil && patch.Welfare.Threshold == nil && patch.Welfare.Cap == nil ||
		patch.Thursday != nil && patch.Thursday.Enabled == nil {
		return MutationResult[ActivitiesConfig]{}, PublishFacts{}, ErrInvalidRequest
	}
	tx, err := r.beginAdminMutation(ctx, adminID)
	if err != nil {
		return MutationResult[ActivitiesConfig]{}, PublishFacts{}, err
	}
	committed := false
	defer finishTx(tx, &committed)
	now, err := r.decisionNow()
	if err != nil {
		return MutationResult[ActivitiesConfig]{}, PublishFacts{}, err
	}
	decision, err := beginControlMutation(ctx, tx, "admin", adminID, mutation, now)
	if err != nil {
		return MutationResult[ActivitiesConfig]{}, PublishFacts{}, err
	}
	if decision.Kind == idempotency.Replay {
		replayed, replayErr := replayMutation[ActivitiesConfig](decision)
		if replayErr != nil {
			return MutationResult[ActivitiesConfig]{}, PublishFacts{}, replayErr
		}
		if err := commitTx(tx, &committed); err != nil {
			return MutationResult[ActivitiesConfig]{}, PublishFacts{}, err
		}
		return replayed, PublishFacts{}, nil
	}
	current, err := readActivityConfigTx(ctx, tx)
	if err != nil {
		return MutationResult[ActivitiesConfig]{}, PublishFacts{}, err
	}
	if current.revision != patch.ExpectedRevision {
		return MutationResult[ActivitiesConfig]{}, PublishFacts{}, ErrConflict
	}
	next := current
	if patch.MasterEnabled != nil {
		next.masterEnabled = *patch.MasterEnabled
	}
	if patch.Welfare != nil {
		if patch.Welfare.Enabled != nil {
			next.welfareEnabled = *patch.Welfare.Enabled
		}
		if patch.Welfare.Threshold != nil {
			value, ok := parsePointsMilli(*patch.Welfare.Threshold)
			if !ok {
				return MutationResult[ActivitiesConfig]{}, PublishFacts{}, ErrInvalidRequest
			}
			next.welfareThreshold = value
		}
		if patch.Welfare.Cap != nil {
			value, ok := parsePointsMilli(*patch.Welfare.Cap)
			if !ok {
				return MutationResult[ActivitiesConfig]{}, PublishFacts{}, ErrInvalidRequest
			}
			next.welfareCap = value
		}
	}
	if patch.Thursday != nil && patch.Thursday.Enabled != nil {
		next.thursdayEnabled = *patch.Thursday.Enabled
	}
	if !validActivityConfig(next) {
		return MutationResult[ActivitiesConfig]{}, PublishFacts{}, ErrInvalidRequest
	}
	thursdayWasEffective := current.masterEnabled && current.thursdayEnabled
	thursdayWillBeEffective := next.masterEnabled && next.thursdayEnabled
	if !thursdayWasEffective && thursdayWillBeEffective {
		var ready int
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(
			SELECT 1 FROM thursday_periods WHERE state IN ('configured','open','settling'))`).Scan(&ready); err != nil {
			return MutationResult[ActivitiesConfig]{}, PublishFacts{}, classifyDatabaseError("validate Thursday configuration", err)
		}
		if ready != 1 {
			return MutationResult[ActivitiesConfig]{}, PublishFacts{}, ErrInvalidRequest
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE config_revisions SET revision=revision+1,updated_at=? WHERE domain='activities' AND revision=? AND revision<9223372036854775807`, now, current.revision)
	if err != nil {
		return MutationResult[ActivitiesConfig]{}, PublishFacts{}, classifyDatabaseError("advance activities configuration revision", err)
	}
	changed, err := result.RowsAffected()
	if err != nil || changed != 1 {
		return MutationResult[ActivitiesConfig]{}, PublishFacts{}, ErrConflict
	}
	updates := []struct{ key, value string }{
		{configActivitiesEnabled, boolConfig(next.masterEnabled)},
		{configWelfareEnabled, boolConfig(next.welfareEnabled)},
		{configWelfareThreshold, strconv.FormatInt(next.welfareThreshold, 10)},
		{configWelfareCap, strconv.FormatInt(next.welfareCap, 10)},
		{configThursdayEnabled, boolConfig(next.thursdayEnabled)},
	}
	for _, update := range updates {
		if _, err := tx.ExecContext(ctx, `UPDATE site_config SET value=?,updated_at=? WHERE key=?`, update.value, now, update.key); err != nil {
			return MutationResult[ActivitiesConfig]{}, PublishFacts{}, classifyDatabaseError("update activities configuration", err)
		}
	}
	next.revision++
	response, err := finishJSONMutation(ctx, tx, decision, http.StatusOK, projectActivitiesConfig(next))
	if err != nil {
		return MutationResult[ActivitiesConfig]{}, PublishFacts{}, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return MutationResult[ActivitiesConfig]{}, PublishFacts{}, err
	}
	return response, PublishFacts{Global: true}, nil
}

func boolConfig(value bool) string {
	if value {
		return "1"
	}
	return "0"
}

func siteDay(now int64, timezoneMinutes int) (string, error) {
	if now < 0 || now > maxUnixSecond || timezoneMinutes < -720 || timezoneMinutes > 840 {
		return "", ErrInvariant
	}
	return time.Unix(now, 0).UTC().Add(time.Duration(timezoneMinutes) * time.Minute).Format("2006-01-02"), nil
}

func freezeSiteTimezoneTx(ctx context.Context, tx *sql.Tx, now int64) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO site_config(key,value,updated_at) VALUES(?,'1',?)
		ON CONFLICT(key) DO UPDATE SET value='1',updated_at=excluded.updated_at`, configTimezoneLocked, now); err != nil {
		return classifyDatabaseError("freeze site timezone", err)
	}
	return nil
}

func checkedNextRevision(current int64) (int64, error) {
	if current < 1 || current == int64(^uint64(0)>>1) {
		return 0, ErrInvariant
	}
	return current + 1, nil
}

func mustRowsAffected(result sql.Result, want int64, conflict bool) error {
	count, err := result.RowsAffected()
	if err != nil {
		return classifyDatabaseError("read rows affected", err)
	}
	if count != want {
		if conflict {
			return ErrConflict
		}
		return fmt.Errorf("%w: unexpected row count", ErrInvariant)
	}
	return nil
}

func isNoRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }
