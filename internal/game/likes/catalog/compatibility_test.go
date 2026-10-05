package catalog

import "testing"

func TestBalanceV3KeepsPublishedIdentity(t *testing.T) {
	for mode, expected := range map[string]string{
		"quick":    "8ac284bb30358464fa7dd6f811e56ac30d46efb47daeb72bd2bc056855cee4f0",
		"standard": "32bb27079e1d075cbc2921519c3830b1b06d70e5687bd75b31263cbedee3f10c",
	} {
		c, hash, err := LoadBalanceV3(mode)
		if err != nil || hash != expected || c.SchemaVersion != 16 {
			t.Fatal(mode, hash, err)
		}
		current, currentHash, err := Load(mode)
		if err != nil || currentHash == hash || current.SchemaVersion != 17 {
			t.Fatal(mode, currentHash, err)
		}
	}
}

func TestSupportedLegacyIdentitiesAndExplicitCategories(t *testing.T) {
	for mode, expected := range map[string]string{
		"quick":    "65512e407ece9c31486cc808100780607b524cc6a95d1e2a9b6ef0424c6f7333",
		"standard": "55473a4623bf7974a64210f1afbcc2f0411d7557888c9a20fc8df88ad23d0dd7",
	} {
		old, hash, err := LoadLegacy(mode)
		if err != nil || hash != expected || old.SchemaVersion != 15 {
			t.Fatal("legacy identity changed", mode, hash, err)
		}
		c, hash, err := Load(mode)
		if err != nil || hash == expected {
			t.Fatal("new behavior shares legacy hash", err)
		}
		for _, role := range c.Roles {
			if role.Passive == nil {
				t.Fatal("missing character passive")
			}
		}
		c.Buffs[0].Category = "unknown"
		if c.Validate() == nil {
			t.Fatal("unknown category accepted")
		}
		c, _, _ = Load(mode)
		for i := range c.Buffs {
			if c.Buffs[i].Kind == "SOTA_FANATICISM" {
				c.Buffs[i].Category = "state"
			}
		}
		if c.Validate() == nil {
			t.Fatal("SOTA excluded from resistance")
		}
		c, _, _ = Load(mode)
		c.Skills[0].Effects.Base.BuffID = "unknown"
		if c.Validate() == nil {
			t.Fatal("unknown effect reference accepted")
		}
		c, _, _ = Load(mode)
		for i := range c.Skills {
			if c.Skills[i].ID == "PUB62" {
				c.Skills[i].Effects.Base.P = 5
			}
		}
		if c.Validate() == nil {
			t.Fatal("unbounded attempt count accepted")
		}
	}
}

func TestPreviousCatalogIdentityAndPlanningTime(t *testing.T) {
	for mode, expected := range map[string]string{
		"quick":    "177cb83e30206c8343cd91984963228970980fe6b36a4fb6844253f7d76ececc",
		"standard": "27fe126841daa7328c487036241469a8854866c9bf9602743eda2f925a84cced",
	} {
		old, hash, err := LoadHistorical(mode)
		if err != nil || hash != expected || old.SchemaVersion != 16 || old.Parameters["TURN_SECONDS"] != 20 {
			t.Fatal("previous catalog identity changed", mode, hash, err)
		}
		current, currentHash, err := Load(mode)
		if err != nil || currentHash == hash || current.Parameters["TURN_SECONDS"] != 30 {
			t.Fatal("current catalog does not have distinct 30-second identity", mode, currentHash, err)
		}
	}
}
