package checkin

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"math"
	"math/big"
	"math/bits"
	"strconv"
	"strings"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

const (
	checkinModeKey     = "checkin_mode"
	awardMinimumKey    = "checkin_award_min_milli"
	awardMaximumKey    = "checkin_award_max_milli"
	balanceCapKey      = "credits_cap_milli"
	timezoneLockKey    = "site_timezone_offset_locked"
	levelThreshold2Key = "level_threshold_2_milli"
	levelThreshold3Key = "level_threshold_3_milli"
	levelThreshold4Key = "level_threshold_4_milli"
)

type checkinConfig struct {
	mode                           string
	awardMin, awardMax, balanceCap int64
}

type siteDay struct {
	activityDay int64
	siteDate    string
}

func readSiteDayAndConfig(ctx context.Context, tx *sql.Tx, now int64) (siteDay, checkinConfig, error) {
	rawOffset, err := readRequiredConfig(ctx, tx, db.SiteTimezoneKey)
	if err != nil {
		return siteDay{}, checkinConfig{}, err
	}
	trimmed := strings.TrimSpace(rawOffset)
	offset, parseErr := strconv.Atoi(trimmed)
	if parseErr != nil || strconv.FormatInt(int64(offset), 10) != trimmed || !db.ValidSiteTimezoneOffset(offset) {
		return siteDay{}, checkinConfig{}, ErrFeatureDisabled
	}
	mode, err := readRequiredConfig(ctx, tx, checkinModeKey)
	if err != nil {
		return siteDay{}, checkinConfig{}, err
	}
	if mode != db.CheckinModeEnabled && mode != db.CheckinModeLevelGated {
		return siteDay{}, checkinConfig{}, ErrFeatureDisabled
	}
	minimum, err := readCheckinAmount(ctx, tx, awardMinimumKey)
	if err != nil {
		return siteDay{}, checkinConfig{}, err
	}
	maximum, err := readCheckinAmount(ctx, tx, awardMaximumKey)
	if err != nil {
		return siteDay{}, checkinConfig{}, err
	}
	capValue, err := readCheckinAmount(ctx, tx, balanceCapKey)
	if err != nil {
		return siteDay{}, checkinConfig{}, err
	}
	if minimum > maximum {
		return siteDay{}, checkinConfig{}, ErrFeatureDisabled
	}
	activityDay := db.SiteDayKey(now, int64(offset))
	localMidnight := activityDay + int64(offset)*60
	date := time.Unix(localMidnight, 0).UTC().Format("2006-01-02")
	if len(date) != len("2006-01-02") {
		return siteDay{}, checkinConfig{}, ErrInvariant
	}
	return siteDay{activityDay: activityDay, siteDate: date}, checkinConfig{
		mode: mode, awardMin: minimum, awardMax: maximum, balanceCap: capValue,
	}, nil
}

func readRequiredConfig(ctx context.Context, tx *sql.Tx, key string) (string, error) {
	var value sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT value FROM site_config WHERE key=?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) || err == nil && !value.Valid {
		return "", ErrFeatureDisabled
	}
	if err != nil {
		return "", classifyDatabase("read check-in configuration", err)
	}
	return value.String, nil
}

func readCheckinAmount(ctx context.Context, tx *sql.Tx, key string) (int64, error) {
	raw, err := readRequiredConfig(ctx, tx, key)
	if err != nil {
		return 0, err
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 || value > db.MaxMoneyMilli || strconv.FormatInt(value, 10) != raw {
		return 0, ErrFeatureDisabled
	}
	return value, nil
}

func effectiveLevelTx(ctx context.Context, tx *sql.Tx, userID, now int64, persist bool) (int, error) {
	var (
		isAdmin     int
		manual      sql.NullInt64
		autoLevel   int
		donationRaw []byte
		revisionRaw []byte
	)
	err := tx.QueryRowContext(ctx, `SELECT is_admin,level,auto_level,donation_credit_mag,revision
		FROM users WHERE id=?`, userID).Scan(&isAdmin, &manual, &autoLevel, &donationRaw, &revisionRaw)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, classifyDatabase("read effective level", err)
	}
	if isAdmin != 0 {
		return 0, ErrForbidden
	}
	if manual.Valid {
		if manual.Int64 < 1 || manual.Int64 > 5 {
			return 0, ErrInvariant
		}
		return int(manual.Int64), nil
	}
	if autoLevel < 1 || autoLevel > 4 {
		return 0, ErrInvariant
	}
	donation, err := db.DecodeU128(donationRaw)
	if err != nil {
		return 0, ErrInvariant
	}
	target := 1
	for index, key := range []string{levelThreshold2Key, levelThreshold3Key, levelThreshold4Key} {
		threshold, err := readCheckinAmount(ctx, tx, key)
		if err != nil {
			return 0, err
		}
		if threshold > 0 && donation.Big().Cmp(big.NewInt(threshold)) >= 0 {
			target = index + 2
		}
	}
	if target <= autoLevel {
		return autoLevel, nil
	}
	if !persist {
		return target, nil
	}
	revision, err := db.DecodeU128(revisionRaw)
	if err != nil {
		return 0, ErrInvariant
	}
	nextRevision, err := incrementU128(revision)
	if err != nil {
		return 0, ErrResourceLimit
	}
	result, err := tx.ExecContext(ctx, `UPDATE users SET auto_level=?,revision=?,updated_at=?
		WHERE id=? AND is_admin=0 AND revision=?`, target, db.EncodeU128(nextRevision), now, userID, revisionRaw)
	if err != nil {
		return 0, classifyDatabase("persist automatic level", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return 0, classifyDatabase("read automatic level update", err)
	}
	if changed != 1 {
		return 0, ErrInvariant
	}
	return target, nil
}

func drawAward(random io.Reader, minimum, maximum int64) (int64, error) {
	if random == nil || minimum < 0 || minimum > maximum || maximum > db.MaxMoneyMilli {
		return 0, ErrInvariant
	}
	if minimum == maximum {
		return minimum, nil
	}
	span := uint64(maximum - minimum)
	bitWidth := bits.Len64(span)
	byteWidth := (bitWidth + 7) / 8
	mask := uint64(1)<<bitWidth - 1
	buffer := make([]byte, byteWidth)
	for {
		if _, err := io.ReadFull(random, buffer); err != nil {
			return 0, err
		}
		var value uint64
		for _, item := range buffer {
			value = value<<8 | uint64(item)
		}
		value &= mask
		if value <= span {
			return minimum + int64(value), nil
		}
	}
}

func temporalTableEmpty(ctx context.Context, tx *sql.Tx, table string) (bool, error) {
	switch table {
	case "checkins", "user_activity_daily", "site_activity_daily":
	default:
		return false, ErrInvariant
	}
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM `+table+` LIMIT 1)`).Scan(&exists); err != nil {
		return false, classifyDatabase("probe timezone freeze table", err)
	}
	return exists == 0, nil
}

func freezeTimezone(ctx context.Context, tx *sql.Tx, now int64) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO site_config(key,value,updated_at) VALUES(?,'1',?)
		ON CONFLICT(key) DO UPDATE SET value='1',updated_at=excluded.updated_at`, timezoneLockKey, now); err != nil {
		return classifyDatabase("freeze site timezone", err)
	}
	return nil
}

func recordCheckinActivity(ctx context.Context, tx *sql.Tx, userID, day, now int64) error {
	var priorActive int
	priorExists := true
	err := tx.QueryRowContext(ctx, `SELECT product_active FROM user_activity_daily
		WHERE day=? AND user_id=?`, day, userID).Scan(&priorActive)
	if errors.Is(err, sql.ErrNoRows) {
		priorExists = false
		priorActive = 0
	} else if err != nil {
		return classifyDatabase("read prior check-in activity", err)
	}
	if priorActive != 0 && priorActive != 1 {
		return ErrInvariant
	}
	if priorExists {
		result, err := tx.ExecContext(ctx, `UPDATE user_activity_daily
			SET product_active=1,checkins=checkins+1,updated_at=?
			WHERE day=? AND user_id=? AND checkins<?`, now, day, userID, int64(math.MaxInt64))
		if err != nil {
			return classifyDatabase("update user check-in activity", err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return classifyDatabase("read user activity update", err)
		}
		if changed != 1 {
			return ErrResourceLimit
		}
	} else {
		result, err := tx.ExecContext(ctx, `INSERT INTO user_activity_daily(
			day,user_id,product_active,checkins,updated_at
		) SELECT ?,?,1,1,? WHERE EXISTS(SELECT 1 FROM users WHERE id=?)`, day, userID, now, userID)
		if err != nil {
			return classifyDatabase("insert user check-in activity", err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return classifyDatabase("read user activity insert", err)
		}
		if changed != 1 {
			return ErrNotFound
		}
	}
	distinctIncrement := int64(1)
	if priorActive == 1 {
		distinctIncrement = 0
	}
	return incrementSiteCheckinActivity(ctx, tx, day, now, distinctIncrement)
}

func incrementSiteCheckinActivity(ctx context.Context, tx *sql.Tx, day, now, distinctIncrement int64) error {
	var checkinsRaw, distinctRaw []byte
	var productActive int
	err := tx.QueryRowContext(ctx, `SELECT product_active,checkins,distinct_product_users
		FROM site_activity_daily WHERE day=?`, day).Scan(&productActive, &checkinsRaw, &distinctRaw)
	if errors.Is(err, sql.ErrNoRows) {
		if distinctIncrement != 1 {
			return ErrInvariant
		}
		zero := db.EncodeU128(db.U128{})
		one := db.U128{}
		one[15] = 1
		oneRaw := db.EncodeU128(one)
		if _, err := tx.ExecContext(ctx, `INSERT INTO site_activity_daily(
			day,product_active,api_requests,uncached_input_tokens,cache_write_input_tokens,
			cache_read_input_tokens,output_tokens,checkins,console_writes,game_active,
			game_rounds,distinct_product_users,updated_at
		) VALUES(?,1,?,?,?,?,?,?,?,0,?,?,?)`, day, zero, zero, zero, zero, zero, oneRaw, zero, zero, oneRaw, now); err != nil {
			return classifyDatabase("insert site check-in activity", err)
		}
		return nil
	}
	if err != nil {
		return classifyDatabase("read site check-in activity", err)
	}
	if productActive != 0 && productActive != 1 {
		return ErrInvariant
	}
	checkins, err := db.DecodeU128(checkinsRaw)
	if err != nil {
		return ErrInvariant
	}
	distinct, err := db.DecodeU128(distinctRaw)
	if err != nil {
		return ErrInvariant
	}
	nextCheckins, err := incrementU128(checkins)
	if err != nil {
		return ErrResourceLimit
	}
	nextDistinct := distinct
	if distinctIncrement == 1 {
		nextDistinct, err = incrementU128(distinct)
		if err != nil {
			return ErrResourceLimit
		}
	} else if distinctIncrement != 0 {
		return ErrInvariant
	}
	result, err := tx.ExecContext(ctx, `UPDATE site_activity_daily
		SET product_active=1,checkins=?,distinct_product_users=?,updated_at=?
		WHERE day=? AND checkins=? AND distinct_product_users=?`,
		db.EncodeU128(nextCheckins), db.EncodeU128(nextDistinct), now, day, checkinsRaw, distinctRaw)
	if err != nil {
		return classifyDatabase("update site check-in activity", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return classifyDatabase("read site activity update", err)
	}
	if changed != 1 {
		return ErrInvariant
	}
	return nil
}

func incrementU128(value db.U128) (db.U128, error) {
	next := new(big.Int).Add(value.Big(), big.NewInt(1))
	return db.U128FromBig(next)
}
