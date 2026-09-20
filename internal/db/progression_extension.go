package db

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/game"
)

// This is the complete, published source structure, including gateway policy.
const preProgressionManifestHash = "f9b40aca86364622c27d73c31f59d53965adaf1c814882662d965e721f30a1f6"

var progressionChangedTables = []string{"credit_operations", "idempotency_records", "game_onboarding_completions", "game_onboarding_holds", "game_blackjack_sessions"}

func progressionTableSQL(table, previous string) (string, error) {
	var changes [][2]string
	switch table {
	case "credit_operations":
		if strings.Count(previous, "'game_onboarding_reward'") != 2 {
			return "", errors.New("unrecognized loan operation source")
		}
		return strings.ReplaceAll(previous, "'game_onboarding_reward'", "'game_onboarding_reward','activity_loan'"), nil
	case "idempotency_records":
		changes = append(changes, [2]string{"'game_blackjack','donation'", "'game_blackjack','activity_loan','donation'"})
	case "game_blackjack_sessions":
		// Previous sessions remain valid. Their retained decision timestamps may
		// exceed the shorter live interval, so the historical upper bound stays.
		changes = append(changes, [2]string{"started_at%60=0", "started_at%30=0"})
	case "game_onboarding_holds":
		return progressionOnboardingHolds, nil
	case "game_onboarding_completions":
		changes = append(changes, [2]string{previousOnboardingTasks, progressionOnboardingTasks}, [2]string{
			" END)",
			`   WHEN game_key='bidding' THEN CASE task_key WHEN 'complete_tier_1' THEN 1000000 WHEN 'complete_tier_2' THEN 2000000 WHEN 'complete_tier_3' THEN 5000000 WHEN 'first_win' THEN 2000000 END
   WHEN game_key='likes' THEN CASE task_key WHEN 'quick_complete' THEN 1000000 WHEN 'quick_win' THEN 2000000 WHEN 'standard_complete' THEN 5000000 WHEN 'standard_win' THEN 10000000 END
   WHEN game_key='blackjack' THEN CASE task_key WHEN 'complete' THEN 1000000 WHEN 'first_win' THEN 2000000 WHEN 'first_bust' THEN 3000000 WHEN 'first_21' THEN 4000000 WHEN 'first_natural_21' THEN 5000000 END
 END)`,
		})
	default:
		return "", errors.New("unknown progression schema target")
	}
	for _, change := range changes {
		if strings.Count(previous, change[0]) != 1 {
			return "", fmt.Errorf("unrecognized progression source constraint: %s", table)
		}
		previous = strings.Replace(previous, change[0], change[1], 1)
	}
	return previous, nil
}

func progressionBootstrapSchema(previous string) string {
	for _, table := range progressionChangedTables {
		start := "CREATE TABLE " + table + " ("
		_, tail, ok := strings.Cut(previous, start)
		if !ok {
			panic("missing canonical progression table")
		}
		body, _, ok := strings.Cut(tail, ";")
		if !ok {
			panic("incomplete canonical progression table")
		}
		old := start + body
		target, err := progressionTableSQL(table, old)
		if err != nil || strings.Count(previous, old) != 1 {
			panic("invalid canonical progression table: " + table)
		}
		previous = strings.Replace(previous, old, target, 1)
	}
	return previous + progressionAdditiveSchema()
}

func progressionAdditiveSchema() string {
	return progressionColumnsSchema + progressionTablesSchema + abuseStorageSchema + progressionOnboardingIndexes + progressionOnboardingGuards + rejectionStorageSchema()
}

func ProgressionStoragePresent(ctx context.Context, q queryer) (bool, error) {
	var count int
	err := q.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_schema WHERE type='table' AND name IN ('game_statistics_epoch','game_rank_counters','game_rank_events','game_rank_totals','activity_loans','abuse_windows','abuse_window_events','abuse_cases','abuse_actions','abuse_evidence')`).Scan(&count)
	if err != nil {
		return false, err
	}
	if count != 0 && count != 10 {
		return false, errors.New("partial progression storage")
	}
	return count == 10, nil
}

func seedProgressionState(ctx context.Context, tx *sql.Tx, at int64) error {
	present, err := ProgressionStoragePresent(ctx, tx)
	if err != nil || !present {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO game_statistics_epoch(id,started_at,rules_version) VALUES(1,?,1);
INSERT INTO game_rank_counters(id,next_event_seq) VALUES(1,X'00000000000000000000000000000001')`, at)
	return err
}

func applyProgressionExtension(ctx context.Context, tx *sql.Tx) error {
	manifest, err := readGenerationManifest(ctx, tx)
	if err != nil {
		return err
	}
	if generationManifestDigest(manifest) != preProgressionManifestHash {
		return errors.New("unrecognized progression source manifest")
	}
	type change struct{ table, before, after string }
	var changes []change
	for _, table := range progressionChangedTables {
		c := change{table: table}
		if err := tx.QueryRowContext(ctx, `SELECT sql FROM sqlite_schema WHERE type='table' AND name=?`, table).Scan(&c.before); err != nil {
			return err
		}
		if c.after, err = progressionTableSQL(table, c.before); err != nil {
			return err
		}
		changes = append(changes, c)
	}
	// ALTER allocates appended nullable columns without copying or changing any
	// existing holds. The replacement SQL retains exactly that physical order.
	if _, err := tx.ExecContext(ctx, `ALTER TABLE game_onboarding_holds ADD COLUMN duel_queue_id TEXT REFERENCES game_duel_queue(id) ON DELETE RESTRICT;
ALTER TABLE game_onboarding_holds ADD COLUMN duel_session_id TEXT;
ALTER TABLE game_onboarding_holds ADD COLUMN blackjack_entry_id TEXT REFERENCES game_blackjack_entries(id) ON DELETE RESTRICT;`); err != nil {
		return err
	}
	for i := range changes {
		if changes[i].table == "game_onboarding_holds" {
			if err := tx.QueryRowContext(ctx, `SELECT sql FROM sqlite_schema WHERE type='table' AND name='game_onboarding_holds'`).Scan(&changes[i].before); err != nil {
				return err
			}
		}
	}
	var version int64
	if err := tx.QueryRowContext(ctx, `PRAGMA schema_version`).Scan(&version); err != nil {
		return err
	}
	if version < 0 || version >= 2147483647 {
		return errors.New("progression schema version cannot advance")
	}
	if _, err := tx.ExecContext(ctx, `PRAGMA writable_schema=ON`); err != nil {
		return err
	}
	for _, c := range changes {
		result, err := tx.ExecContext(ctx, `UPDATE sqlite_schema SET sql=? WHERE type='table' AND name=? AND sql=?`, c.after, c.table, c.before)
		if err != nil {
			return err
		}
		if count, err := result.RowsAffected(); err != nil || count != 1 {
			return errors.New("progression schema update count mismatch")
		}
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA schema_version=%d; PRAGMA writable_schema=RESET;`, version+1)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, progressionAdditiveSchema()); err != nil {
		return err
	}
	if err := backfillDonationAchievement(ctx, tx); err != nil {
		return err
	}
	defaults := progressionConfigDefaults()
	quick, err := initialQuickStakes(ctx, tx)
	if err != nil {
		return err
	}
	defaults["game_blackjack_quick_stakes"] = quick
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
	return seedProgressionState(ctx, tx, time.Now().Unix())
}

func backfillDonationAchievement(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `SELECT u.id,u.donation_credit_mag,o.ledger_seq,o.created_at,o.donation_credit_after
FROM users u LEFT JOIN credit_operations o ON o.ledger_seq=(SELECT max(c.ledger_seq) FROM credit_operations c WHERE c.donation_credit_user_id=u.id AND c.donation_credit_delta_sign<>0)
ORDER BY u.id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var current, after []byte
		var sequence, at sql.NullInt64
		if err := rows.Scan(&id, &current, &sequence, &at, &after); err != nil {
			return err
		}
		amount, err := DecodeU128(current)
		if err != nil {
			return errors.New("invalid donation total encoding")
		}
		if !sequence.Valid {
			if amount.Big().Sign() != 0 {
				return errors.New("donation total has no ledger achievement")
			}
			continue
		}
		if !at.Valid || sequence.Int64 <= 0 || !bytes.Equal(current, after) {
			return errors.New("donation total differs from ledger achievement")
		}
		seq, err := U128FromBig(big.NewInt(sequence.Int64))
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE users SET donation_credit_achieved_at=?,donation_credit_achieved_seq=? WHERE id=?`, at.Int64, EncodeU128(seq), id); err != nil {
			return err
		}
	}
	return rows.Err()
}

func initialQuickStakes(ctx context.Context, q queryer) (string, error) {
	limits := make(map[string]int64, 4)
	for _, field := range []string{"min_stake", "max_stake", "stake_step", "default_stake"} {
		var raw string
		if err := q.QueryRowContext(ctx, `SELECT value FROM site_config WHERE key=?`, "game_blackjack_"+field+"_milli").Scan(&raw); err != nil {
			return "", err
		}
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value < 1 {
			return "", errors.New("invalid source blackjack limits")
		}
		limits[field] = value
	}
	accepts := func(v int64) bool {
		return v >= limits["min_stake"] && v <= limits["max_stake"] && (v-limits["min_stake"])%limits["stake_step"] == 0
	}
	if !accepts(limits["default_stake"]) {
		return "", errors.New("invalid source blackjack default stake")
	}
	values := make([]string, 0, 4)
	for _, v := range []int64{1000000, 5000000, 10000000, 50000000} {
		if accepts(v) {
			values = append(values, game.FormatAmount(v))
		}
	}
	if len(values) == 0 {
		values = append(values, game.FormatAmount(limits["default_stake"]))
	}
	encoded, err := json.Marshal(values)
	return string(encoded), err
}
