package db

import (
	"database/sql"
	"testing"
)

func TestAppearanceUpgradeRetainsPityAndAddsUnpublishedPresets(t *testing.T) {
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	defer database.Close()
	hostileMustExec(t, database, "PRAGMA foreign_keys=ON;"+gachaStorageSchema())
	assertRetainedManifest(t, database, gachaManifestHash)
	user := hostileInsertUser(t, database, "appearance-upgrade", 0, 0)
	hostileMustExec(t, database, "INSERT INTO gacha_pity(user_id,group_id,sr,ssr,sr_limit,ssr_limit,sr_featured,ssr_featured) VALUES(?,'permanent',5,41,10,60,1,1)", user)
	hostileMustExec(t, database, "UPDATE gacha_settings SET shop=json_set(shop,'$.ssr_card',4,'$.limited_cap',12)")
	for range 2 {
		if err = extendKnownGenerationTwoSchema(t.Context(), database); err != nil {
			t.Fatal(err)
		}
		assertRetainedManifest(t, database, PinnedGenerationTwoManifestHash)
		var sr, ssr, srl, ssrl, srf, ssrf, misses int
		if err = database.QueryRow("SELECT sr,ssr,sr_limit,ssr_limit,sr_featured,ssr_featured,ssr_misses FROM gacha_pity WHERE user_id=?", user).Scan(&sr, &ssr, &srl, &ssrl, &srf, &ssrf, &misses); err != nil {
			t.Fatal(err)
		}
		if sr != 5 || ssr != 41 || srl != 10 || ssrl != 60 || srf != 1 || ssrf != 1 || misses != 1 {
			t.Fatal("pity lost")
		}
		var spPrice, oldPrice, cap, selected, templates int
		err = database.QueryRow("SELECT json_extract(shop,'$.sp_card'),json_extract(shop,'$.ssr_card'),json_extract(shop,'$.limited_cap'),(SELECT count(*) FROM user_appearance),(SELECT count(*) FROM gacha_presets WHERE id IN ('celestial','wandering') AND json_extract(config,'$.published')=0 AND json_extract(config,'$.rules.ssr_featured')=1500 AND json_extract(config,'$.rules.ssr_guarantee_misses')=2) FROM gacha_settings").Scan(&spPrice, &oldPrice, &cap, &selected, &templates)
		if err != nil || spPrice != 9 || oldPrice != 4 || cap != 12 || selected != 0 || templates != 2 {
			t.Fatal(spPrice, oldPrice, cap, selected, templates, err)
		}
	}
}
