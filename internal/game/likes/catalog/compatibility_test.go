package catalog

import "testing"

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
