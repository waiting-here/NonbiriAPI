package gacha

import (
	"database/sql"
	"slices"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func TestMomentOptionsUseOwnedArtworkAndActualPoolIntervals(t *testing.T) {
	f := newFixture(t)
	options := func(user int64) []string {
		t.Helper()
		tx, err := f.db.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		value, err := f.s.MomentOptionsTx(t.Context(), tx, user)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	if got := options(f.user); !slices.Equal(got, []string{"cover:permanent"}) {
		t.Fatal(got)
	}
	for _, id := range []string{"a-chatgpt", "a-claude", "a-glm", "a-deepseek"} {
		f.exec(t, "INSERT INTO gacha_owned(user_id,card_id,obtained_at,source) VALUES(?,?,?,'reward')", f.user, id, f.at.Load())
	}
	if got := options(f.user); !slices.Contains(got, "card:a-chatgpt") || slices.Contains(got, "illustration:a") {
		t.Fatal(got)
	}
	f.exec(t, "INSERT INTO gacha_owned(user_id,card_id,obtained_at,source) VALUES(?,?,?,'shop')", f.user, "a-gemini", f.at.Load())
	f.exec(t, "INSERT INTO gacha_owned(user_id,card_id,obtained_at,source) VALUES(?,?,?,'shop')", f.user, "l-chatgpt", f.at.Load())
	if got := options(f.user); !slices.Contains(got, "illustration:a") || !slices.Contains(got, "card:l-chatgpt") || !slices.Contains(got, "illustration:l") || slices.Contains(got, "cover:l") {
		t.Fatal(got)
	}
	if got := options(f.other); !slices.Equal(got, []string{"cover:permanent"}) {
		t.Fatal(got)
	}

	id, err := db.GenerateOpaqueID("gp_")
	if err != nil {
		t.Fatal(err)
	}
	start, end := f.at.Load()+100, f.at.Load()+200
	pool := Pool{ID: id, Name: "Scheduled", Kind: "limited", Cover: "l", Rules: spRules(), Featured: []string{"l-chatgpt"}, StartsAt: &start, EndsAt: &end}
	save := func(key string) {
		t.Helper()
		out := f.save(t, key, AdminInput{Kind: "pool", Pool: &pool, ExpectedRevision: pool.Revision})
		pool = out.Pools[1]
	}
	checkCover := func(want bool) {
		t.Helper()
		got := options(f.other)
		if slices.Contains(got, "cover:l") != want {
			t.Fatal(got, want)
		}
	}
	save("moment-draft")
	checkCover(false)
	pool.Published = true
	save("moment-scheduled")
	checkCover(false)
	f.at.Add(10)
	pool.Paused = true
	save("moment-paused")
	f.at.Add(100)
	checkCover(false)
	pool.Paused = false
	save("moment-open")
	checkCover(true)
	f.at.Add(1)
	pool.Cover = "m"
	save("moment-changed")
	if got := options(f.other); !slices.Contains(got, "cover:l") || !slices.Contains(got, "cover:m") {
		t.Fatal(got)
	}
	f.at.Add(1)
	pool.Paused = true
	save("moment-closed")
	f.at.Add(1000)
	checkCover(true)
	var profiles int
	if err := f.db.QueryRow("SELECT count(*) FROM gacha_profiles").Scan(&profiles); err != nil || profiles != 0 {
		t.Fatal("options entered the activity", profiles, err)
	}
}
