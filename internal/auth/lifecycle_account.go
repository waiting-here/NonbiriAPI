package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

const lifecycleExportLimit = 10_000

var (
	ErrLifecycleInvalid  = errors.New("auth: invalid lifecycle request")
	ErrLifecycleNotFound = errors.New("auth: lifecycle user not found")
)

// LifecycleIdentity is the closed, non-secret account projection made
// available to the account lifecycle coordinator. It intentionally does not
// contain the Discord identity, ban reason, revisions, or session material.
type LifecycleIdentity struct {
	ID                        string
	Username                  string
	Avatar                    *string
	AvatarURL                 *string
	GuildNick                 *string
	GuildAvatarURL            *string
	Lang                      string
	IsBanned                  bool
	BannedUntil               *int64
	CharitySuspendedUntil     *int64
	EndpointLimit             *string
	EffectiveEndpointLimit    string
	RPMLimit                  *string
	EffectiveRPMLimit         string
	ConcurrencyLimit          *string
	EffectiveConcurrencyLimit string
	Balance                   string
	DonationCredit            string
	EffectiveLevel            int
	LevelDisplayName          string
	GameProfilePublic         bool
	CreatedAt                 int64
	UpdatedAt                 int64
}

// LifecycleUsage is the closed usage projection exported with an account.
type LifecycleUsage struct {
	TotalRequests              string
	TotalUncachedInputTokens   string
	TotalCacheWriteInputTokens string
	TotalCacheReadInputTokens  string
	TotalOutputTokens          string
	TotalPromptTokens          string
	TotalCompletionTokens      string
	TotalUnknownUsageRequests  string
}

// LifecycleDeletionFinalizer owns the sole process-local side effect of the
// auth deletion slice. The coordinator calls exactly one method after its
// shared database transaction has either committed or rolled back.
type LifecycleDeletionFinalizer interface {
	Commit() bool
	Abort() bool
}

// ExportLifecycleIdentity reads the account through the coordinator-owned
// transaction and frozen decision time. No second transaction is started.
func (r *Runtime) ExportLifecycleIdentity(
	ctx context.Context,
	tx *sql.Tx,
	userID, decisionNow int64,
	limit int,
) (LifecycleIdentity, LifecycleUsage, error) {
	if r == nil || r.db == nil || ctx == nil || tx == nil || userID <= 0 ||
		decisionNow < 0 || decisionNow > maxUnixSecond || limit < 1 || limit > lifecycleExportLimit {
		return LifecycleIdentity{}, LifecycleUsage{}, ErrLifecycleInvalid
	}
	envelope, err := r.userEnvelopeTx(ctx, tx, userID, decisionNow)
	if errors.Is(err, sql.ErrNoRows) {
		return LifecycleIdentity{}, LifecycleUsage{}, ErrLifecycleNotFound
	}
	if err != nil {
		return LifecycleIdentity{}, LifecycleUsage{}, fmt.Errorf("auth: export lifecycle identity: %w", err)
	}
	user := envelope.User
	identity := LifecycleIdentity{
		ID: user.ID, Username: user.Username, Avatar: user.Avatar, AvatarURL: user.AvatarURL,
		GuildNick: user.GuildNick, GuildAvatarURL: user.GuildAvatarURL, Lang: user.Lang,
		IsBanned: user.IsBanned, BannedUntil: user.BannedUntil,
		CharitySuspendedUntil: user.CharitySuspendedUntil,
		EndpointLimit:         user.EndpointLimit, EffectiveEndpointLimit: user.EffectiveEndpointLimit,
		RPMLimit: user.RPMLimit, EffectiveRPMLimit: user.EffectiveRPMLimit,
		ConcurrencyLimit:          user.ConcurrencyLimit,
		EffectiveConcurrencyLimit: user.EffectiveConcurrencyLimit,
		Balance:                   user.Balance, DonationCredit: user.DonationCredit,
		EffectiveLevel: user.EffectiveLevel, LevelDisplayName: user.LevelDisplayName,
		GameProfilePublic: user.GameProfilePublic, CreatedAt: user.CreatedAt, UpdatedAt: user.UpdatedAt,
	}
	usage := LifecycleUsage{
		TotalRequests:              user.Usage.TotalRequests,
		TotalUncachedInputTokens:   user.Usage.TotalUncachedInputTokens,
		TotalCacheWriteInputTokens: user.Usage.TotalCacheWriteInputTokens,
		TotalCacheReadInputTokens:  user.Usage.TotalCacheReadInputTokens,
		TotalOutputTokens:          user.Usage.TotalOutputTokens,
		TotalPromptTokens:          user.Usage.TotalPromptTokens,
		TotalCompletionTokens:      user.Usage.TotalCompletionTokens,
		TotalUnknownUsageRequests:  user.Usage.TotalUnknownUsageRequests,
	}
	return identity, usage, nil
}

type lifecycleDeletionFinalizer struct {
	runtime *Runtime
	userID  int64
	done    bool
}

// PrepareLifecycleAccountDeletion revokes every browser session in the
// caller-owned transaction. Elevated capabilities are bound to those session
// rows and therefore become unusable in the same authoritative commit.
func (r *Runtime) PrepareLifecycleAccountDeletion(
	ctx context.Context,
	tx *sql.Tx,
	userID, decisionNow int64,
) (LifecycleDeletionFinalizer, error) {
	if r == nil || r.db == nil || ctx == nil || tx == nil || userID <= 0 ||
		decisionNow < 0 || decisionNow > maxUnixSecond {
		return nil, ErrLifecycleInvalid
	}
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=? AND is_admin=0)`, userID).Scan(&exists); err != nil {
		return nil, fmt.Errorf("auth: read lifecycle deletion user: %w", err)
	}
	if exists != 1 {
		return nil, ErrLifecycleNotFound
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id=?`, userID); err != nil {
		return nil, fmt.Errorf("auth: revoke lifecycle sessions: %w", err)
	}
	return &lifecycleDeletionFinalizer{runtime: r, userID: userID}, nil
}

func (finalizer *lifecycleDeletionFinalizer) Commit() bool {
	if finalizer == nil || finalizer.runtime == nil || finalizer.done {
		return false
	}
	finalizer.done = true
	finalizer.runtime.notifyUserSessionInvalidated(finalizer.userID)
	return true
}

func (finalizer *lifecycleDeletionFinalizer) Abort() bool {
	if finalizer == nil || finalizer.done {
		return false
	}
	finalizer.done = true
	return true
}
