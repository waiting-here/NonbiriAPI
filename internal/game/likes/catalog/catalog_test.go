package catalog

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestFixedCatalogsAreCompleteAndIndependent(t *testing.T) {
	for _, mode := range []string{"quick", "standard"} {
		t.Run(mode, func(t *testing.T) {
			config, hash, err := Load(mode)
			if err != nil {
				t.Fatal(err)
			}
			again, otherHash, err := Load(mode)
			if err != nil || hash != otherHash || len(hash) != 64 || !reflect.DeepEqual(config, again) {
				t.Fatal("unstable catalog")
			}
			config.Parameters["TARGET_LIKES"]++
			config.Skills[0].ResourceCosts["R_IMAGE"] = 999
			if config.Parameters["TARGET_LIKES"] == again.Parameters["TARGET_LIKES"] || again.Skills[0].ResourceCosts["R_IMAGE"] == 999 {
				t.Fatal("catalog shares mutable data")
			}
		})
	}
	if _, _, err := Load("../quick"); err == nil {
		t.Fatal("unknown mode accepted")
	}
}

func TestPublicSnapshotExcludesUnusedPricesAndCaps(t *testing.T) {
	for _, mode := range []string{"quick", "standard"} {
		snapshot, err := Public(mode)
		if err != nil {
			t.Fatal(err)
		}
		body, err := json.Marshal(snapshot)
		if err != nil || len(body) > 1<<19 || strings.Contains(string(body), "POINT_TICKET") || strings.Contains(string(body), "FOLLOWUP_CAP") {
			t.Fatal("invalid public content budget or settings")
		}
		for _, buff := range snapshot.Config.Buffs {
			if buff.Kind == "OVERLOAD" && !strings.Contains(buff.Target, "报价大于零") {
				t.Fatal("public overload explanation contradicts rule")
			}
		}
		original, hash, err := Load(mode)
		if err != nil || snapshot.ContentHash != hash || original.Parameters["POINT_TICKET"] == 0 || original.Parameters["FOLLOWUP_CAP"] != 3 {
			t.Fatal("public projection modified rule source")
		}
	}
}

func TestEveryPairOfCurrentActionsTerminatesFlashWithinBound(t *testing.T) {
	for _, mode := range []string{"quick", "standard"} {
		c, _, err := Load(mode)
		if err != nil {
			t.Fatal(err)
		}
		buffs := map[string]Buff{}
		for _, buff := range c.Buffs {
			buffs[buff.ID] = buff
		}
		progress := []int64{}
		for _, skill := range c.Skills {
			variants := []Effect{skill.Effects.Base}
			if skill.Copyable {
				variants = append(variants, skill.Effects.I, skill.Effects.II)
			}
			for _, effect := range variants {
				n := effect.Combo
				if effect.Kind == "COMBO" {
					n += effect.P
				}
				if effect.N > 0 && buffs[effect.ExtraBuffID].Kind == "COMBO" {
					n++
				}
				if effect.Kind == "CACHE_CONVERT" {
					if n != 0 {
						t.Fatal("conversion and direct progress overlap")
					}
					n = 3
				}
				progress = append(progress, n)
			}
		}
		if len(progress) != 130 {
			t.Fatal("variant coverage changed")
		}
		for _, first := range progress {
			for _, second := range progress {
				for old := int64(0); old < 3; old++ {
					budget, count := old+first+second, 0
					if budget > 10 {
						t.Fatal("progress proof invalid")
					}
					for budget >= 3 {
						budget -= 2 // success returns one; failure consumes more
						count++
					}
					if count > 5 {
						t.Fatal("flash bound invalid")
					}
				}
			}
		}
	}
}

func TestRejectsBrokenFlashTerminationAssumptions(t *testing.T) {
	for _, change := range []func(*Config){
		func(c *Config) {
			for i := range c.Buffs {
				if c.Buffs[i].Kind == "COMBO" {
					c.Buffs[i].P = 1
				}
			}
		},
		func(c *Config) {
			for i := range c.Skills {
				if c.Skills[i].ID == "GEM01" {
					c.Skills[i].Effects.Base.Combo = 1
				}
			}
		},
		func(c *Config) {
			for i := range c.Skills {
				if c.Skills[i].Effects.Base.Kind == "CACHE_CONVERT" {
					c.Skills[i].Effects.Base.Combo = 1
				}
			}
		},
		func(c *Config) {
			for i := range c.Skills {
				if c.Skills[i].Effects.Base.Kind == "COMBO" {
					c.Skills[i].Effects.Base.P = 5
				}
			}
		},
	} {
		config, _, err := Load("quick")
		if err != nil {
			t.Fatal(err)
		}
		change(&config)
		if config.Validate() == nil {
			t.Fatal("unsafe content accepted")
		}
	}
}
