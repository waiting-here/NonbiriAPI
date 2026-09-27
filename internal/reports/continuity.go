package reports

import (
	"context"
	"database/sql"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/continuity"
)

func (keys *reportKeys) identityRateDigest(key continuity.Key) ([32]byte, error) {
	if keys == nil {
		return [32]byte{}, ErrUnavailable
	}
	keys.mu.RLock()
	defer keys.mu.RUnlock()
	if keys.closed {
		return [32]byte{}, ErrClosed
	}
	return keyedDigest(&keys.rate, false, framed([]byte("account")), framed([]byte("stable-identity-v1")), framed(key[:])), nil
}

// accountRateTx moves only verifiable, unexpired legacy account windows.
// The source rows are removed in the same TX, making repeated access exact.
// Identity buckets retain the existing short expiry after account deletion.
func (repository *Repository) accountRateTx(ctx context.Context, tx *sql.Tx, userID, now int64) ([32]byte, error) {
	key, err := continuity.BoundKeyTx(ctx, tx, userID)
	if err != nil {
		return [32]byte{}, err
	}
	stable, err := repository.keys.identityRateDigest(key)
	if err != nil {
		return [32]byte{}, err
	}
	legacy, err := repository.keys.rateDigest("account", []byte(strconv.FormatInt(userID, 10)))
	if err != nil {
		return [32]byte{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO report_rate_buckets(scope,scope_hash,window_start,count,updated_at,expires_at)
SELECT 'account',?,window_start,count,updated_at,expires_at FROM report_rate_buckets WHERE scope='account' AND scope_hash=? AND expires_at>?
ON CONFLICT(scope,scope_hash,window_start) DO UPDATE SET count=report_rate_buckets.count+excluded.count,updated_at=MAX(report_rate_buckets.updated_at,excluded.updated_at)`, stable[:], legacy[:], now)
	if err != nil {
		return [32]byte{}, err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM report_rate_buckets WHERE scope='account' AND scope_hash=?`, legacy[:])
	return stable, err
}
