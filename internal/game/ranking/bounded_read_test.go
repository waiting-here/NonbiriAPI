package ranking

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func seedRankPage(t *testing.T, f *fixture, count int) {
	t.Helper()
	f.tx(func(tx *sql.Tx) error {
		_, err := tx.Exec(`WITH RECURSIVE n(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<?)
INSERT INTO users(id,username,is_admin,game_profile_public,charity_profile_public,is_banned,banned_until,
donation_credit_mag,donation_credit_achieved_at,donation_credit_achieved_seq,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,created_at,updated_at)
SELECT i+1000,'rank-'||i,CASE WHEN i=3 THEN 1 ELSE 0 END,1,1,CASE WHEN i%19=0 THEN 1 ELSE 0 END,NULL,
CAST(printf('%016d',i%17+1) AS BLOB),?+i%7,CAST(printf('%016d',i) AS BLOB),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),?,? FROM n`, count, f.epoch, f.epoch, f.epoch)
		if err != nil {
			return err
		}
		_, err = tx.Exec(`INSERT INTO game_rank_totals(user_id,board,window,amount_sign,amount_mag,achieved_at,achieved_phase,achieved_seq)
SELECT id,'game_charity','7d',1,donation_credit_mag,donation_credit_achieved_at,id%2,donation_credit_achieved_seq FROM users WHERE id>1000`)
		return err
	})
}

// This independent reference retains the full window used before page selection.
func fullRankReference(ctx context.Context, tx *sql.Tx, user int64, board string, page pagination.Request, now int64) ([]Row, *Row, error) {
	eligible := `SELECT t.user_id,t.amount_mag,t.achieved_at,t.achieved_phase,t.achieved_seq FROM game_rank_totals t JOIN users u ON u.id=t.user_id WHERE t.board='game_charity' AND t.window='7d' AND t.amount_sign=1 AND u.is_admin=0`
	preference := `COALESCE(p.game_profile_public,u.game_profile_public)`
	where := `r.rank<=20 OR r.user_id=?`
	args := []any{now, user}
	if board == "charity" {
		eligible = `SELECT id AS user_id,donation_credit_mag AS amount_mag,donation_credit_achieved_at AS achieved_at,0 AS achieved_phase,donation_credit_achieved_seq AS achieved_seq FROM users WHERE is_admin=0 AND donation_credit_mag>zeroblob(16)`
		preference = `u.charity_profile_public`
		var count int64
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM (`+eligible+`)`).Scan(&count); err != nil {
			return nil, nil, err
		}
		_, offset, err := page.Window(count)
		if err != nil {
			return nil, nil, err
		}
		where = `r.rank>? AND r.rank<=?`
		args = []any{now, offset, offset + 20}
	}
	rows, err := tx.QueryContext(ctx, `WITH eligible AS (`+eligible+`), ranked AS (SELECT *,ROW_NUMBER() OVER(ORDER BY amount_mag DESC,achieved_at,achieved_phase,achieved_seq) AS rank FROM eligible)
 SELECT r.user_id,r.amount_mag,r.rank,CASE WHEN u.is_banned=1 AND (u.banned_until IS NULL OR u.banned_until>?) THEN 0 ELSE `+preference+` END,u.username,u.guild_nick,COALESCE(u.discord_id,''),u.avatar,u.guild_avatar_url
 FROM ranked r JOIN users u ON u.id=r.user_id LEFT JOIN game_user_preferences p ON p.user_id=r.user_id WHERE `+where+` ORDER BY r.rank`, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	out := make([]Row, 0, 20)
	var me *Row
	for rows.Next() {
		var owner, rank int64
		var magnitude []byte
		var public bool
		var username, nick, discord, avatar, guild string
		if err := rows.Scan(&owner, &magnitude, &rank, &public, &username, &nick, &discord, &avatar, &guild); err != nil {
			return nil, nil, err
		}
		amount, err := db.DecodeU128(magnitude)
		if err != nil {
			return nil, nil, err
		}
		row := Row{Rank: strconv.FormatInt(rank, 10), Amount: credits(amount.Big()), IsMe: owner == user, Identity: identity(public, username, nick, discord, avatar, guild)}
		if board != "charity" && rank > 20 {
			me = &row
		} else {
			out = append(out, row)
		}
	}
	return out, me, rows.Err()
}

func TestBoundedRankMatchesFullWindowAcrossTiesAndOwnPositions(t *testing.T) {
	f := newFixture(t)
	seedRankPage(t, f, 73)
	for _, board := range []string{"game_charity", "charity"} {
		for _, user := range []int64{1001, 1003, 1016, 1073, 99999} {
			for _, pageNumber := range []int64{1, 2, pagination.MaxPage} {
				f.tx(func(tx *sql.Tx) error {
					page := pagination.Request{Page: pageNumber, Size: 20}
					want, wantMe, err := fullRankReference(t.Context(), tx, user, board, page, f.epoch+100)
					if err != nil {
						return err
					}
					got, err := ReadTx(t.Context(), tx, user, board, "7d", page, f.epoch+100)
					if err != nil {
						return err
					}
					if !reflect.DeepEqual(got.Rows, want) || !reflect.DeepEqual(got.Me, wantMe) {
						t.Fatalf("board=%s user=%d page=%d got=%+v me=%+v want=%+v me=%+v", board, user, pageNumber, got.Rows, got.Me, want, wantMe)
					}
					return nil
				})
			}
		}
	}
}

func TestBoundedRankPairedPerformanceSample(t *testing.T) {
	if os.Getenv("NONBIRI_PERFORMANCE_SAMPLE") != "1" {
		t.Skip("opt-in bounded performance sample")
	}
	for _, count := range []int{1000, 20000} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			f := newFixture(t)
			seedRankPage(t, f, count)
			for _, board := range []string{"game_charity", "charity"} {
				for _, pageNumber := range []int64{1, pagination.MaxPage} {
					if board != "charity" && pageNumber != 1 {
						continue
					}
					f.tx(func(tx *sql.Tx) error {
						page := pagination.Request{Page: pageNumber, Size: 20}
						user := int64(1001)
						var elapsed [2]time.Duration
						for i := range 6 {
							for _, mode := range []int{i % 2, 1 - i%2} {
								start := time.Now()
								if mode == 0 {
									if _, _, err := fullRankReference(t.Context(), tx, user, board, page, f.epoch+100); err != nil {
										return err
									}
								} else {
									if _, err := ReadTx(t.Context(), tx, user, board, "7d", page, f.epoch+100); err != nil {
										return err
									}
								}
								if i > 0 {
									elapsed[mode] += time.Since(start)
								}
							}
						}
						t.Logf("rows=%d board=%s page=%d old_avg=%s bounded_avg=%s", count, board, pageNumber, elapsed[0]/5, elapsed[1]/5)
						return nil
					})
				}
			}
		})
	}
}
