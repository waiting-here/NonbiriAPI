package ledger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

type accountRole struct {
	id   int64
	kind AccountKind
	code string
}

func userRole(id int64) accountRole {
	return accountRole{id: id, kind: AccountUser}
}

func poolRole(id int64) accountRole {
	return accountRole{id: id, kind: AccountPool}
}

func externalRole(id int64) accountRole {
	return accountRole{id: id, kind: AccountExternal, code: "external"}
}

func platformRole(id int64) accountRole {
	return accountRole{id: id, kind: AccountPlatform, code: "platform"}
}

func reserveRole(id int64, code string) accountRole {
	return accountRole{id: id, kind: AccountPlatform, code: code}
}

// CreateUserAccount creates or returns the one wallet owned by userID. It is
// designed for the outer registration transaction: the users row must
// already exist in that same transaction, and a later registration hook can
// create CallerKey generation zero before the outer commit.
func CreateUserAccount(ctx context.Context, tx *sql.Tx, userID, at int64) (Account, error) {
	if tx == nil || ctx == nil || userID <= 0 || !validUnix(at) {
		return Account{}, ErrInvalidPlan
	}
	if account, err := accountForUser(ctx, tx, userID); err == nil {
		return account, nil
	} else if !errors.Is(err, ErrNotFound) {
		return Account{}, err
	}
	zero := db.EncodeU128(db.U128{})
	result, err := tx.ExecContext(ctx, `
INSERT INTO credit_accounts(kind,user_id,code,balance_sign,balance_mag,created_at,updated_at)
VALUES('user',?,NULL,0,?,?,?)`, userID, zero, at, at)
	if err != nil {
		return Account{}, classifySQLError("create user account", err)
	}
	id, err := result.LastInsertId()
	if err != nil || id <= 0 {
		return Account{}, fmt.Errorf("%w: create user account id", ErrInvariant)
	}
	return readAccount(ctx, tx, id)
}

// CreatePoolAccount creates the zero account paired with a shared pool. The
// caller creates the shared_pools row in the same outer transaction.
func CreatePoolAccount(ctx context.Context, tx *sql.Tx, poolID string, at int64) (Account, error) {
	if !db.ValidateOpaqueID(poolID, "pol_") {
		return Account{}, ErrInvalidPlan
	}
	return createCodedAccount(ctx, tx, AccountPool, "pool:"+poolID, at)
}

// CreateRPSQueueAccount creates the isolated zero reserve account for one
// queue row. It cannot create any other platform account code.
func CreateRPSQueueAccount(ctx context.Context, tx *sql.Tx, queueID string, at int64) (Account, error) {
	if !db.ValidateOpaqueID(queueID, "rpsq_") {
		return Account{}, ErrInvalidPlan
	}
	return createCodedAccount(ctx, tx, AccountPlatform, "rps-queue:"+queueID, at)
}

// CreateRPSSessionAccount creates the isolated zero reserve account for one
// started RPS session.
func CreateRPSSessionAccount(ctx context.Context, tx *sql.Tx, sessionID string, at int64) (Account, error) {
	if !db.ValidateOpaqueID(sessionID, "rps_") {
		return Account{}, ErrInvalidPlan
	}
	return createCodedAccount(ctx, tx, AccountPlatform, "rps-session:"+sessionID, at)
}

func createCodedAccount(ctx context.Context, tx *sql.Tx, kind AccountKind, code string, at int64) (Account, error) {
	if tx == nil || ctx == nil || !validUnix(at) {
		return Account{}, ErrInvalidPlan
	}
	if account, err := accountByCode(ctx, tx, code); err == nil {
		if account.Kind != kind {
			return Account{}, ErrInvariant
		}
		return account, nil
	} else if !errors.Is(err, ErrNotFound) {
		return Account{}, err
	}
	zero := db.EncodeU128(db.U128{})
	result, err := tx.ExecContext(ctx, `
INSERT INTO credit_accounts(kind,user_id,code,balance_sign,balance_mag,created_at,updated_at)
VALUES(?,NULL,?,0,?,?,?)`, string(kind), code, zero, at, at)
	if err != nil {
		return Account{}, classifySQLError("create coded account", err)
	}
	id, err := result.LastInsertId()
	if err != nil || id <= 0 {
		return Account{}, fmt.Errorf("%w: create coded account id", ErrInvariant)
	}
	return readAccount(ctx, tx, id)
}

// ReadAccount reads one authoritative account inside tx.
func ReadAccount(ctx context.Context, tx *sql.Tx, accountID int64) (Account, error) {
	if tx == nil || ctx == nil || accountID <= 0 {
		return Account{}, ErrNotFound
	}
	return readAccount(ctx, tx, accountID)
}

// UserAccount reads the unique wallet for userID.
func UserAccount(ctx context.Context, tx *sql.Tx, userID int64) (Account, error) {
	if tx == nil || ctx == nil || userID <= 0 {
		return Account{}, ErrNotFound
	}
	return accountForUser(ctx, tx, userID)
}

// CodedAccount reads one closed non-user account code. Dynamic pool/RPS codes
// remain constructible only by their typed account constructors.
func CodedAccount(ctx context.Context, tx *sql.Tx, code string) (Account, error) {
	switch code {
	case "platform", "external", "forward_reserve", "charity_reserve", "game_fishing_reserve":
	default:
		return Account{}, ErrNotFound
	}
	if tx == nil || ctx == nil {
		return Account{}, ErrNotFound
	}
	return accountByCode(ctx, tx, code)
}

func accountForUser(ctx context.Context, tx *sql.Tx, userID int64) (Account, error) {
	var id int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM credit_accounts WHERE kind='user' AND user_id=?`, userID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, ErrNotFound
	}
	if err != nil {
		return Account{}, classifySQLError("read user account", err)
	}
	return readAccount(ctx, tx, id)
}

func accountByCode(ctx context.Context, tx *sql.Tx, code string) (Account, error) {
	var id int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM credit_accounts WHERE code=?`, code).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, ErrNotFound
	}
	if err != nil {
		return Account{}, classifySQLError("read coded account", err)
	}
	return readAccount(ctx, tx, id)
}

func readAccount(ctx context.Context, tx *sql.Tx, accountID int64) (Account, error) {
	var (
		kind      string
		user      sql.NullInt64
		code      sql.NullString
		sign      int
		mag       []byte
		createdAt int64
		updatedAt int64
	)
	err := tx.QueryRowContext(ctx, `
SELECT kind,user_id,code,balance_sign,balance_mag,created_at,updated_at
FROM credit_accounts WHERE id=?`, accountID).Scan(&kind, &user, &code, &sign, &mag, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, ErrNotFound
	}
	if err != nil {
		return Account{}, classifySQLError("read account", err)
	}
	accountKind, ok := parseAccountKind(kind)
	if !ok || !validUnix(createdAt) || !validUnix(updatedAt) {
		return Account{}, ErrInvariant
	}
	balance, err := amountFromParts(sign, mag)
	if err != nil || (accountKind == AccountPool || accountKind == AccountPlatform) && balance.Sign() < 0 {
		return Account{}, ErrInvariant
	}
	account := Account{ID: accountID, Kind: accountKind, Balance: balance, CreatedAt: createdAt, UpdatedAt: updatedAt}
	if user.Valid {
		account.UserID = user.Int64
	}
	if code.Valid {
		account.Code = code.String
	}
	if accountKind == AccountUser {
		if account.UserID <= 0 || account.Code != "" {
			return Account{}, ErrInvariant
		}
	} else if account.UserID != 0 || account.Code == "" {
		return Account{}, ErrInvariant
	}
	return account, nil
}

func readAccounts(ctx context.Context, tx *sql.Tx, roles []accountRole) (map[int64]Account, error) {
	ids := make([]int64, 0, len(roles))
	seen := make(map[int64]struct{}, len(roles))
	for _, role := range roles {
		if role.id <= 0 {
			return nil, ErrInvalidPlan
		}
		if _, exists := seen[role.id]; !exists {
			seen[role.id] = struct{}{}
			ids = append(ids, role.id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	accounts := make(map[int64]Account, len(ids))
	for _, id := range ids {
		account, err := readAccount(ctx, tx, id)
		if err != nil {
			return nil, err
		}
		accounts[id] = account
	}
	for _, role := range roles {
		account := accounts[role.id]
		if account.Kind != role.kind || role.code != "" && account.Code != role.code {
			return nil, ErrInvalidPlan
		}
	}
	return accounts, nil
}

func parseAccountKind(value string) (AccountKind, bool) {
	switch AccountKind(value) {
	case AccountUser, AccountPool, AccountPlatform, AccountExternal:
		return AccountKind(value), true
	default:
		return "", false
	}
}
