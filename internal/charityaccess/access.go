// Package charityaccess defines public model access fields and their current
// transaction-local admission checks, shared by routing and dispatch.
package charityaccess

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	ErrInvalid     = errors.New("charity access: invalid fields")
	ErrUnavailable = errors.New("charity access: model or caller unavailable")
	ErrForbidden   = errors.New("charity access: caller level is not allowed")
	ErrInvariant   = errors.New("charity access: invalid stored access state")
)

func Mask(levels []int) (int, error) {
	if len(levels) > 6 {
		return 0, ErrInvalid
	}
	mask := 0
	for _, level := range levels {
		if level < 1 || level > 6 {
			return 0, ErrInvalid
		}
		bit := 1 << (level - 1)
		if mask&bit != 0 {
			return 0, ErrInvalid
		}
		mask |= bit
	}
	return mask, nil
}

func Levels(mask int) ([]int, error) {
	if mask < 0 || mask > 63 {
		return nil, ErrInvariant
	}
	levels := make([]int, 0, 6)
	for level := 1; level <= 6; level++ {
		if Allows(mask, level) {
			levels = append(levels, level)
		}
	}
	return levels, nil
}

func Allows(mask, level int) bool {
	return mask >= 0 && mask <= 63 && level >= 1 && level <= 6 && mask&(1<<(level-1)) != 0
}

func NormalizeDescription(value string) (string, error) {
	if !utf8.ValidString(value) {
		return "", ErrInvalid
	}
	value = strings.ReplaceAll(value, "\r\n", "\n")
	if len(value) > 4096 || utf8.RuneCountInString(value) > 1024 {
		return "", ErrInvalid
	}
	for _, r := range value {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return "", ErrInvalid
		}
	}
	return value, nil
}

func CurrentLevel(ctx context.Context, tx *sql.Tx, userID int64) (int, error) {
	if ctx == nil || tx == nil || userID <= 0 {
		return 0, ErrInvalid
	}
	var level, automatic, admin int
	err := tx.QueryRowContext(ctx, `SELECT COALESCE(level,auto_level),auto_level,is_admin FROM users WHERE id=?`, userID).Scan(&level, &automatic, &admin)
	if errors.Is(err, sql.ErrNoRows) || err == nil && admin != 0 {
		return 0, ErrUnavailable
	}
	if err != nil {
		return 0, fmt.Errorf("charity access: read current level: %w", err)
	}
	if level < 1 || level > 6 || automatic < 1 || automatic > 4 {
		return 0, ErrInvariant
	}
	return level, nil
}

func Require(ctx context.Context, tx *sql.Tx, userID, modelID int64) error {
	if ctx == nil || tx == nil || modelID <= 0 {
		return ErrInvalid
	}
	level, err := CurrentLevel(ctx, tx, userID)
	if err != nil {
		return err
	}
	var enabled, mask int
	err = tx.QueryRowContext(ctx, `SELECT m.enabled,a.allowed_level_mask FROM charity_models m
JOIN charity_model_access a ON a.model_id=m.id WHERE m.id=?`, modelID).Scan(&enabled, &mask)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrUnavailable
	}
	if err != nil {
		return fmt.Errorf("charity access: read model access: %w", err)
	}
	if enabled != 1 {
		return ErrUnavailable
	}
	if mask < 0 || mask > 63 {
		return ErrInvariant
	}
	if !Allows(mask, level) {
		return ErrForbidden
	}
	return nil
}
