package charityrouting

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/waiting-here/NonbiriAPI/internal/charityaccess"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
)

// RequestPolicy is a candidate-free ingress snapshot. The caller binds its
// model identity to admission and retains the same exclusions for retries.
type RequestPolicy struct {
	ModelID               int64
	FullName              string
	ExcludedRequestFields []string
}

func decodeExcludedFields(encoded string) ([]string, error) {
	var fields []string
	if json.Unmarshal([]byte(encoded), &fields) != nil || fields == nil {
		return nil, ErrInvariant
	}
	result, err := openai.NormalizeExcludedRequestFields(fields)
	if err != nil {
		return nil, ErrInvariant
	}
	return result, nil
}

func (s *Service) RequestPolicy(ctx context.Context, userID int64, fullName string, decisionNow int64) (RequestPolicy, error) {
	var result RequestPolicy
	if s == nil || s.db == nil || ctx == nil || userID <= 0 || fullName == "" || decisionNow < 0 || decisionNow > maxUnixSecond {
		return result, ErrInvalidRequest
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	var admin, banned int
	var bannedUntil, suspendedUntil sql.NullInt64
	var gate string
	err = tx.QueryRowContext(ctx, `SELECT u.is_admin,u.is_banned,u.banned_until,u.charity_suspended_until,c.value
FROM users u JOIN site_config c ON c.key='charity_enabled' WHERE u.id=?`, userID).
		Scan(&admin, &banned, &bannedUntil, &suspendedUntil, &gate)
	if errors.Is(err, sql.ErrNoRows) || err == nil && (admin != 0 || banned != 0 && (!bannedUntil.Valid || bannedUntil.Int64 > decisionNow)) {
		return result, ErrUnauthorized
	}
	if err != nil {
		return result, err
	}
	if gate != "0" && gate != "1" {
		return result, ErrInvariant
	}
	if gate == "0" {
		return result, ErrFeatureDisabled
	}
	if suspendedUntil.Valid && suspendedUntil.Int64 > decisionNow {
		return result, ErrCharitySuspended
	}
	var encoded string
	err = tx.QueryRowContext(ctx, `SELECT id,full_name,excluded_request_fields FROM charity_models WHERE full_name=? AND enabled=1`, fullName).
		Scan(&result.ModelID, &result.FullName, &encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return result, ErrNotFound
	}
	if err != nil {
		return result, err
	}
	if err = charityaccess.Require(ctx, tx, userID, result.ModelID); errors.Is(err, charityaccess.ErrForbidden) {
		return RequestPolicy{}, ErrForbidden
	} else if errors.Is(err, charityaccess.ErrUnavailable) {
		return RequestPolicy{}, ErrNotFound
	} else if err != nil {
		return RequestPolicy{}, err
	}
	result.ExcludedRequestFields, err = decodeExcludedFields(encoded)
	if err != nil {
		return RequestPolicy{}, err
	}
	return result, tx.Commit()
}
