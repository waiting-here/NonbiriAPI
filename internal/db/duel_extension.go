package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
)

const preRCOneManifestHash = "49638d0096f60b55246851f30a08de53b51847ca33b3a6586345c3029c694dcd"

// Build the fresh DDL from the same closed table changes used by upgrades.
// The resulting complete source and manifest are pinned independently.
func duelBootstrapSchema(previous string) string {
	for _, table := range []string{"credit_accounts", "credit_operations", "idempotency_records"} {
		start := "CREATE TABLE " + table + " ("
		_, tail, ok := strings.Cut(previous, start)
		if !ok {
			panic("missing canonical duel table")
		}
		body, _, ok := strings.Cut(tail, ";")
		if !ok {
			panic("incomplete canonical duel table")
		}
		old := start + body
		target, err := extendDuelTableSQL(table, old)
		if err != nil || strings.Count(previous, old) != 1 {
			panic("invalid canonical duel table")
		}
		previous = strings.Replace(previous, old, target, 1)
	}
	return previous + duelTablesSchema
}

// DuelStoragePresent supports validation of both sides of the exact migration.
// A partial extension is never accepted as an older, empty deployment.
func DuelStoragePresent(ctx context.Context, q queryer) (bool, error) {
	var count int
	err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema WHERE type='table' AND name IN ('game_duel_catalogs','game_duel_queue','game_duel_sessions','game_duel_seats','game_duel_user_slots','game_duel_rounds','game_duel_anonymous','game_duel_anonymous_rounds')`).Scan(&count)
	if err != nil {
		return false, err
	}
	if count != 0 && count != 8 {
		return false, errors.New("partial duel storage")
	}
	return count == 8, nil
}

func duelConfigDefaults() map[string]string {
	values := map[string]string{"game_bidding_enabled": "0", "game_likes_enabled": "0"}
	for _, game := range []string{"bidding", "likes"} {
		modes := []string{"tier1", "tier2", "tier3"}
		tickets := []string{"5000000", "10000000", "50000000"}
		if game == "likes" {
			modes = []string{"quick", "standard"}
			tickets = []string{"5000000", "25000000"}
		}
		for i, mode := range modes {
			prefix := "game_" + game + "_" + mode
			values[prefix+"_enabled"] = "0"
			values[prefix+"_ticket_milli"] = tickets[i]
			for _, destination := range []string{"platform", "welfare", "thursday"} {
				values[prefix+"_rake_"+destination+"_bp"] = "100"
			}
		}
	}
	return values
}

func extendDuelTableSQL(table, previous string) (string, error) {
	target := previous
	replace := func(old, next string) error {
		if strings.Count(target, old) != 1 {
			return errors.New("unrecognized duel source constraint")
		}
		target = strings.Replace(target, old, next, 1)
		return nil
	}
	var changes [][2]string
	switch table {
	case "credit_accounts":
		old := "OR (length(code)=37 AND substr(code,1,10)='rps-queue:'"
		next := "OR (length(code)=38 AND substr(code,1,11)='duel-queue:' AND substr(code,12,5) IN ('bidq_','likq_') AND substr(code,17) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(code,-1,1) IN ('A','Q','g','w')) OR (length(code)=39 AND substr(code,1,13)='duel-session:' AND substr(code,14,4) IN ('bid_','lik_') AND substr(code,18) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(code,-1,1) IN ('A','Q','g','w')) " + old
		changes = append(changes, [2]string{old, next})
	case "credit_operations":
		changes = append(changes,
			[2]string{",'rps_round_cut','rps_terminal'))", ",'rps_round_cut','rps_terminal','duel_queue_reserve','duel_queue_release','duel_session_start','duel_terminal'))"},
			[2]string{",'rps_queue','rps_session'))", ",'rps_queue','rps_session','duel_queue','duel_session'))"},
			[2]string{"OR (source_type='rps_session' AND", "OR (source_type='duel_queue' AND length(source_id)=27 AND substr(source_id,1,5) IN ('bidq_','likq_') AND substr(source_id,6) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w')) OR (source_type='duel_session' AND length(source_id)=26 AND substr(source_id,1,4) IN ('bid_','lik_') AND substr(source_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(source_id,-1,1) IN ('A','Q','g','w')) OR (source_type='rps_session' AND"},
			[2]string{"OR (kind='rps_round_cut' AND", "OR (kind IN ('duel_queue_reserve','duel_queue_release') AND source_type='duel_queue' AND hex(source_seq)='00000000000000000000000000000000') OR (kind IN ('duel_session_start','duel_terminal') AND source_type='duel_session' AND hex(source_seq)='00000000000000000000000000000000') OR (kind='rps_round_cut' AND"})
	case "idempotency_records":
		changes = append(changes, [2]string{",'game_rps','donation'", ",'game_rps','game_bidding','game_likes','donation'"})
	default:
		return "", errors.New("unknown duel schema target")
	}
	for _, change := range changes {
		if err := replace(change[0], change[1]); err != nil {
			return "", err
		}
	}
	return target, nil
}

// ApplyDuelExtension borrows the caller's migration transaction and accepts
// only the complete previous manifest. Callers roll back on any error and
// validate the complete final manifest before committing.
func ApplyDuelExtension(ctx context.Context, tx *sql.Tx) (resultErr error) {
	manifest, err := readGenerationManifest(ctx, tx)
	if err != nil {
		return err
	}
	if generationManifestDigest(manifest) != preRCOneManifestHash {
		return errors.New("unrecognized duel source manifest")
	}
	type change struct{ table, previous, target string }
	changes := []change{}
	for _, table := range []string{"credit_accounts", "credit_operations", "idempotency_records"} {
		var previous string
		if err := tx.QueryRowContext(ctx, `SELECT sql FROM sqlite_schema WHERE type='table' AND name=?`, table).Scan(&previous); err != nil {
			return err
		}
		target, err := extendDuelTableSQL(table, previous)
		if err != nil {
			return err
		}
		changes = append(changes, change{table, previous, target})
	}
	var version int64
	if err := tx.QueryRowContext(ctx, `PRAGMA schema_version`).Scan(&version); err != nil {
		return err
	}
	if version < 0 || version >= 2147483647 {
		return errors.New("duel schema version cannot advance")
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
	for _, change := range changes {
		result, err := tx.ExecContext(ctx, `UPDATE sqlite_schema SET sql=? WHERE type='table' AND name=? AND sql=?`, change.target, change.table, change.previous)
		if err != nil {
			return err
		}
		if count, err := result.RowsAffected(); err != nil || count != 1 {
			return errors.New("duel constraint update failed")
		}
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA schema_version=%d; PRAGMA writable_schema=RESET;`, version+1)); err != nil {
		return err
	}
	writable = false
	if _, err := tx.ExecContext(ctx, duelTablesSchema); err != nil {
		return err
	}
	defaults := duelConfigDefaults()
	keys := make([]string, 0, len(defaults))
	for key := range defaults {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if _, err := tx.ExecContext(ctx, `INSERT INTO site_config(key,value,updated_at) VALUES(?,?,0)`, key, defaults[key]); err != nil {
			return err
		}
	}
	return nil
}
