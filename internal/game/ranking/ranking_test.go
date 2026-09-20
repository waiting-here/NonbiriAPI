package ranking

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"math/rand/v2"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbfixture"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

type fixture struct {
	t      *testing.T
	db     *sql.DB
	epoch  int64
	serial int
}

type allowRankUser struct{}

func (allowRankUser) AuthorizeUserMutation(context.Context, *sql.Tx, int64) error { return nil }

func TestReadRechecksExpiryWhenCatchUpCrossesASecond(t *testing.T) {
	f := newFixture(t)
	user := f.user(false)
	f.add(user, f.epoch, 100, 0, "fishing")
	calls := int64(0)
	s, err := New(f.db, allowRankUser{}, func() time.Time { calls++; return time.Unix(f.epoch+week-2+calls, 0) })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Read(context.Background(), user, "game_charity", "7d", pagination.Default()); !errors.Is(err, ErrCatchingUp) {
		t.Fatal("stale window returned", err)
	}
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rank.db")
	dbfixture.Materialize(t, path)
	database, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { database.Close() })
	if _, err := database.Exec(`UPDATE maintenance_state SET enabled=0 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, db: database}
	if err := database.QueryRow(`SELECT started_at FROM game_statistics_epoch`).Scan(&f.epoch); err != nil {
		t.Fatal(err)
	}
	return f
}
func (f *fixture) user(public bool, admin ...bool) int64 {
	f.t.Helper()
	f.serial++
	z := make([]byte, 16)
	isAdmin := len(admin) > 0 && admin[0]
	r, err := f.db.Exec(`INSERT INTO users(discord_id,username,is_admin,game_profile_public,charity_profile_public,donation_credit_mag,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, fmt.Sprintf("%d", f.serial), fmt.Sprintf("player-%d", f.serial), isAdmin, public, public, z, z, z, z, z, z, z, z, f.epoch, f.epoch)
	if err != nil {
		f.t.Fatal(err)
	}
	id, err := r.LastInsertId()
	if err != nil {
		f.t.Fatal(err)
	}
	return id
}
func (f *fixture) tx(fn func(*sql.Tx) error) {
	f.t.Helper()
	tx, err := f.db.Begin()
	if err != nil {
		f.t.Fatal(err)
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		f.t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		f.t.Fatal(err)
	}
}
func (f *fixture) add(user, at, loss, profit int64, game string) {
	f.t.Helper()
	f.serial++
	f.tx(func(tx *sql.Tx) error {
		return RecordTx(context.Background(), tx, Contribution{user, game, fmt.Sprintf("source_%d", f.serial), at, big.NewInt(loss), big.NewInt(profit)})
	})
}
func (f *fixture) advance(now int64) {
	f.t.Helper()
	for range 100 {
		ready := false
		f.tx(func(tx *sql.Tx) error {
			var err error
			ready, err = AdvanceTx(context.Background(), tx, now)
			return err
		})
		if ready {
			return
		}
	}
	f.t.Fatal("catch-up did not finish")
}
func (f *fixture) total(user int64, board, window string) (string, int64, int) {
	f.t.Helper()
	var sign, phase int
	var mag []byte
	var at int64
	err := f.db.QueryRow(`SELECT amount_sign,amount_mag,achieved_at,achieved_phase FROM game_rank_totals WHERE user_id=? AND board=? AND window=?`, user, board, window).Scan(&sign, &mag, &at, &phase)
	if err == sql.ErrNoRows {
		return "0", 0, 0
	}
	if err != nil {
		f.t.Fatal(err)
	}
	value, err := db.NewSM128(sign, mag)
	if err != nil {
		f.t.Fatal(err)
	}
	return value.Decimal(), at, phase
}
func (f *fixture) read(user int64, board, window string, page int64, now int64) Board {
	f.t.Helper()
	var b Board
	f.tx(func(tx *sql.Tx) error {
		var err error
		b, err = ReadTx(context.Background(), tx, user, board, window, pagination.Request{Page: page, Size: 20}, now)
		return err
	})
	return b
}

func TestLogicalExpiryOrderingAndNetZero(t *testing.T) {
	f := newFixture(t)
	u := f.user(false)
	e := f.epoch
	f.add(u, e, 100, 0, "fishing")
	f.add(u, e+1, 100, 0, "likes")
	f.add(u, e+2, -100, 0, "likes")
	if v, at, _ := f.total(u, "game_charity", "7d"); v != "100" || at != e+2 {
		t.Fatal(v, at)
	}
	f.add(u, e+3, 50, 0, "fishing")
	f.add(u, e+3, -50, 0, "likes")
	f.advance(e + week + 2)
	if v, at, phase := f.total(u, "game_charity", "7d"); v != "0" || at != e+week+2 || phase != 0 {
		t.Fatal(v, at, phase)
	}
	f.advance(e + week + 3)
	if v, at, _ := f.total(u, "game_charity", "7d"); v != "0" || at != e+week+2 {
		t.Fatal("net zero refreshed achievement", v, at)
	}
	// A settlement at the expiry second observes all phase-zero changes first.
	f.add(u, e+week+3, 100, 0, "linklink")
	if v, at, phase := f.total(u, "game_charity", "7d"); v != "100" || at != e+week+3 || phase != 1 {
		t.Fatal(v, at, phase)
	}
}

func TestLargeSingleExpiryGroupResumesWithoutIntermediateTotal(t *testing.T) {
	f := newFixture(t)
	u := f.user(false)
	e := f.epoch
	for i := range advanceRows + 80 {
		loss := int64(7)
		if i%2 == 1 {
			loss = -7
		}
		f.add(u, e, loss, 0, "likes")
	}
	f.add(u, e+1, 20, 0, "fishing")
	service, err := New(f.db, allowRankUser{}, func() time.Time { return time.Unix(e+week, 0) })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Read(context.Background(), u, "game_charity", "7d", pagination.Default()); !errors.Is(err, ErrCatchingUp) {
		t.Fatal("partial ranking exposed", err)
	}
	ready := true
	f.tx(func(tx *sql.Tx) error {
		var err error
		ready, err = ReadyTx(context.Background(), tx, e+week)
		return err
	})
	if ready {
		t.Fatal("unbounded expiry batch")
	}
	if v, at, _ := f.total(u, "game_charity", "7d"); v != "20" || at != e+1 {
		t.Fatal("partial group changed total", v, at)
	}
	f.advance(e + week)
	if v, at, _ := f.total(u, "game_charity", "7d"); v != "20" || at != e+1 {
		t.Fatal("zero group changed achievement", v, at)
	}
	var n int
	if err := f.db.QueryRow(`SELECT count(*) FROM game_rank_events WHERE settled_at=?`, e).Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
}

func TestPersonalExportUsesCurrentWindowsAndBoundedOwnerProjection(t *testing.T) {
	f := newFixture(t)
	u, v := f.user(false), f.user(false)
	e := f.epoch
	f.add(u, e, 150, 100, "blackjack")
	f.add(u, e+1, 20, 0, "likes")
	f.add(v, e+1, 999, 0, "fishing")
	f.tx(func(tx *sql.Tx) error {
		_, err := ExportTx(context.Background(), tx, u, e+1, 1)
		if !errors.Is(err, ErrExportTooLarge) {
			t.Fatal(err)
		}
		out, err := ExportTx(context.Background(), tx, u, e+1, 10000)
		if err != nil {
			return err
		}
		if len(out.Events) != 2 || out.Events[0].Game != "blackjack" || *out.Events[0].Loss != "0.15" || *out.Events[0].PositiveProfit != "0.1" {
			t.Fatal(out)
		}
		body, _ := json.Marshal(out)
		for _, key := range []string{"user_id", "source_id", "seq", "player"} {
			if strings.Contains(string(body), key) {
				t.Fatal(string(body))
			}
		}
		return nil
	})
	f.tx(func(tx *sql.Tx) error {
		_, err := ExportTx(context.Background(), tx, u, e+week, 10000)
		if !errors.Is(err, ErrCatchingUp) {
			t.Fatal(err)
		}
		return nil
	})
	f.advance(e + week + 1)
	f.tx(func(tx *sql.Tx) error {
		out, err := ExportTx(context.Background(), tx, u, e+week+1, 10000)
		if err != nil {
			return err
		}
		if len(out.Events) != 1 || out.Events[0].Loss != nil || out.Events[0].PositiveProfit == nil {
			t.Fatal(out)
		}
		return nil
	})
}

func TestWindowsProfitRetentionAndRandomLossConservation(t *testing.T) {
	f := newFixture(t)
	u := f.user(false)
	e := f.epoch
	f.add(u, e, 80, 100, "blackjack")
	f.add(u, e+week, 10, 200, "bidding")
	if v, _, _ := f.total(u, "blackjack", "7d"); v != "0" {
		t.Fatal(v)
	}
	if v, _, _ := f.total(u, "blackjack", "30d"); v != "100" {
		t.Fatal(v)
	}
	var loss sql.NullInt64
	if err := f.db.QueryRow(`SELECT loss_sign FROM game_rank_events WHERE game_key='blackjack'`).Scan(&loss); err != nil || loss.Valid {
		t.Fatal("charity payload retained", err, loss)
	}
	f.advance(e + month)
	if v, _, _ := f.total(u, "blackjack", "30d"); v != "0" {
		t.Fatal(v)
	}
	if v, at, _ := f.total(u, "blackjack", "history"); v != "100" || at != e {
		t.Fatal(v, at)
	}
	rng := rand.New(rand.NewPCG(7, 11))
	type event struct{ at, loss int64 }
	var events []event
	for i := range 80 {
		at := e + month + int64(i)*86400
		loss := int64(rng.IntN(2001) - 1000)
		events = append(events, event{at, loss})
		f.advance(at)
		f.add(u, at, loss, 0, "fishing")
		var want int64
		for _, v := range events {
			if v.at > at-week {
				want += v.loss
			}
		}
		if got, _, _ := f.total(u, "game_charity", "7d"); got != fmt.Sprint(want) {
			t.Fatal(i, got, want)
		}
	}
}

func TestRankingIdentityTopTwentyAndCharityPaging(t *testing.T) {
	f := newFixture(t)
	e := f.epoch
	var users []int64
	for i := range 24 {
		u := f.user(true, i == 2)
		users = append(users, u)
		f.add(u, e+int64(i), int64(100-i), 0, "fishing")
		amount, _ := db.U128FromBig(big.NewInt(int64(100 - i)))
		seq, _ := db.U128FromBig(big.NewInt(int64(i + 1)))
		if _, err := f.db.Exec(`UPDATE users SET donation_credit_mag=?,donation_credit_achieved_at=?,donation_credit_achieved_seq=? WHERE id=?`, db.EncodeU128(amount), e+int64(i), db.EncodeU128(seq), u); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.db.Exec(`UPDATE users SET is_banned=1,banned_until=NULL WHERE id=?`, users[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE users SET is_banned=1,banned_until=? WHERE id=?`, e+100, users[1]); err != nil {
		t.Fatal(err)
	}
	b := f.read(users[23], "game_charity", "7d", 1, e+50)
	if len(b.Rows) != 20 || b.Me == nil || b.Me.Rank != "23" || !b.Me.IsMe {
		t.Fatal(b)
	}
	if b.Rows[0].Identity.Kind != "anonymous" || b.Rows[1].Identity.Kind != "anonymous" {
		t.Fatal("banned identity leaked")
	}
	raw, _ := json.Marshal(b.Rows[0])
	if strings.Contains(string(raw), "player") || strings.Contains(string(raw), "avatar") || strings.Contains(string(raw), "user_id") {
		t.Fatal(string(raw))
	}
	b = f.read(users[23], "charity", "history", 2, e+100)
	if b.Pagination.TotalItems != "23" || len(b.Rows) != 3 || !b.Rows[2].IsMe || b.Me != nil {
		t.Fatal(b)
	}
	b = f.read(users[1], "charity", "history", 1, e+100)
	if b.Rows[1].Identity.Kind != "public" {
		t.Fatal("expired ban still anonymous")
	}
	if _, err := f.db.Exec(`UPDATE users SET charity_profile_public=0 WHERE id=?`, users[1]); err != nil {
		t.Fatal(err)
	}
	b = f.read(users[1], "charity", "history", 1, e+100)
	if b.Rows[1].Identity.Kind != "anonymous" {
		t.Fatal(b)
	}
	b = f.read(users[1], "game_charity", "7d", 1, e+100)
	if b.Rows[1].Identity.Kind != "public" {
		t.Fatal("preferences coupled")
	}
}

func TestWideAmountsDuplicateAndRollback(t *testing.T) {
	f := newFixture(t)
	u := f.user(false)
	wide := new(big.Int).Lsh(big.NewInt(1), 100)
	c := Contribution{u, "bidding", "wide_source", f.epoch, wide, wide}
	f.tx(func(tx *sql.Tx) error { return RecordTx(context.Background(), tx, c) })
	tx, err := f.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := RecordTx(context.Background(), tx, c); err == nil {
		t.Fatal("duplicate accepted")
	}
	tx.Rollback()
	if got, _, _ := f.total(u, "bidding", "history"); got != wide.String() {
		t.Fatal(got)
	}
	tx, err = f.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	c.Source = "rollback"
	if err := RecordTx(context.Background(), tx, c); err != nil {
		t.Fatal(err)
	}
	tx.Rollback()
	if got, _, _ := f.total(u, "bidding", "history"); got != wide.String() {
		t.Fatal(got)
	}
	f.tx(func(tx *sql.Tx) error { return DeleteTx(context.Background(), tx, u) })
	var count int
	if err := f.db.QueryRow(`SELECT count(*) FROM game_rank_events WHERE user_id=?`, u).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
}
