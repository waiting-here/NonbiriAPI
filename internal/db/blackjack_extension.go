package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
)

const preBlackjackManifestHash = "2f0c6660a89ab28478c502f24e75a1bb1bbb9309bff904ca3ded24e29dab3fd9"

func blackjackConfigDefaults() map[string]string {
	return map[string]string{"game_blackjack_enabled": "0", "game_blackjack_min_stake_milli": "1000000", "game_blackjack_max_stake_milli": "50000000", "game_blackjack_stake_step_milli": "1000000", "game_blackjack_default_stake_milli": "5000000", "game_blackjack_rake_platform_bp": "100", "game_blackjack_rake_welfare_bp": "100", "game_blackjack_rake_thursday_bp": "100"}
}

func BlackjackStoragePresent(ctx context.Context, q queryer) (bool, error) {
	var count int
	err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema WHERE type='table' AND name IN ('game_blackjack_clock','game_blackjack_sessions','game_blackjack_entries','game_blackjack_payments','game_blackjack_events','game_blackjack_anonymous')`).Scan(&count)
	if err != nil {
		return false, err
	}
	if count != 0 && count != 6 {
		return false, errors.New("partial blackjack storage")
	}
	return count == 6, nil
}

func extendBlackjackTableSQL(table, previous string) (string, error) {
	var changes [][2]string
	switch table {
	case "credit_accounts":
		old := "OR (length(code)=38 AND substr(code,1,11)='duel-queue:'"
		changes = append(changes, [2]string{old, "OR (length(code)=44 AND substr(code,1,18)='blackjack-payment:' AND substr(code,19,4)='bjp_' AND substr(code,23) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(code,-1,1) IN ('A','Q','g','w')) " + old})
	case "credit_operations":
		changes = append(changes,
			[2]string{",'duel_session_start','duel_terminal'))", ",'duel_session_start','duel_terminal','blackjack_reserve','blackjack_settle','blackjack_release'))"},
			[2]string{",'duel_queue','duel_session'))", ",'duel_queue','duel_session','blackjack_payment'))"},
			[2]string{"OR (source_type='duel_queue' AND", "OR (source_type='blackjack_payment' AND length(source_id)=26 AND substr(source_id,1,4)='bjp_' AND substr(source_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w')) OR (source_type='duel_queue' AND"},
			[2]string{"OR (kind IN ('duel_queue_reserve'", "OR (kind IN ('blackjack_reserve','blackjack_settle','blackjack_release') AND source_type='blackjack_payment' AND hex(source_seq)='00000000000000000000000000000000') OR (kind IN ('duel_queue_reserve'"})
	case "idempotency_records":
		changes = append(changes, [2]string{",'game_bidding','game_likes','donation'", ",'game_bidding','game_likes','game_blackjack','donation'"})
	default:
		return "", errors.New("unknown blackjack schema target")
	}
	for _, change := range changes {
		if strings.Count(previous, change[0]) != 1 {
			return "", errors.New("unrecognized blackjack source constraint")
		}
		previous = strings.Replace(previous, change[0], change[1], 1)
	}
	return previous, nil
}

func blackjackBootstrapSchema(previous string) string {
	for _, table := range []string{"credit_accounts", "credit_operations", "idempotency_records"} {
		start := "CREATE TABLE " + table + " ("
		_, tail, ok := strings.Cut(previous, start)
		if !ok {
			panic("missing canonical blackjack table")
		}
		body, _, ok := strings.Cut(tail, ";")
		if !ok {
			panic("incomplete canonical blackjack table")
		}
		old := start + body
		target, err := extendBlackjackTableSQL(table, old)
		if err != nil || strings.Count(previous, old) != 1 {
			panic("invalid canonical blackjack table")
		}
		previous = strings.Replace(previous, old, target, 1)
	}
	return previous + blackjackTablesSchema
}

func applyBlackjackExtension(ctx context.Context, tx *sql.Tx) (resultErr error) {
	manifest, err := readGenerationManifest(ctx, tx)
	if err != nil {
		return err
	}
	if generationManifestDigest(manifest) != preBlackjackManifestHash {
		return errors.New("unrecognized blackjack source manifest")
	}
	type change struct{ table, previous, target string }
	var changes []change
	for _, table := range []string{"credit_accounts", "credit_operations", "idempotency_records"} {
		c := change{table: table}
		if err := tx.QueryRowContext(ctx, `SELECT sql FROM sqlite_schema WHERE type='table' AND name=?`, table).Scan(&c.previous); err != nil {
			return err
		}
		if c.target, err = extendBlackjackTableSQL(table, c.previous); err != nil {
			return err
		}
		changes = append(changes, c)
	}
	var version int64
	if err := tx.QueryRowContext(ctx, `PRAGMA schema_version`).Scan(&version); err != nil {
		return err
	}
	if version < 0 || version >= 2147483647 {
		return errors.New("blackjack schema version cannot advance")
	}
	writable := false
	defer func() {
		if writable {
			_, err := tx.ExecContext(context.WithoutCancel(ctx), `PRAGMA writable_schema=RESET`)
			if resultErr == nil {
				resultErr = err
			}
		}
	}()
	if _, err := tx.ExecContext(ctx, `PRAGMA writable_schema=ON`); err != nil {
		return err
	}
	writable = true
	for _, c := range changes {
		r, err := tx.ExecContext(ctx, `UPDATE sqlite_schema SET sql=? WHERE type='table' AND name=? AND sql=?`, c.target, c.table, c.previous)
		if err != nil {
			return err
		}
		if count, err := r.RowsAffected(); err != nil || count != 1 {
			return errors.New("blackjack constraint update failed")
		}
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA schema_version=%d; PRAGMA writable_schema=RESET`, version+1)); err != nil {
		return err
	}
	writable = false
	if _, err := tx.ExecContext(ctx, blackjackTablesSchema); err != nil {
		return err
	}
	defaults := blackjackConfigDefaults()
	keys := make([]string, 0, len(defaults))
	for key := range defaults {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		if _, err := tx.ExecContext(ctx, `INSERT INTO site_config(key,value,updated_at) VALUES(?,?,0)`, key, defaults[key]); err != nil {
			return err
		}
	}
	return nil
}
