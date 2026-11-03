package rules

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
)

func populatedCloneProfile(size int) Profile {
	p := InitialProfile()
	p.SavedLoadouts[0] = &Loadout{Rod: "bambooPole"}
	p.CollectionClaims = map[string]int64{"lake": 1800000000}
	for i := 0; i < size; i++ {
		p.Basket = append(p.Basket, Catch{ID: uint64(i + 1), Kind: "gold", Length: 20})
	}
	if size != 0 {
		p.Records["gold"] = Record{Caught: Amount(fmt.Sprint(size)), MaxLength: 20, PerfectCount: "0"}
	}
	p.NextCatchID = uint64(size + 1)
	return p
}

func jsonCloneProfile(p Profile) Profile {
	raw, err := json.Marshal(p)
	if err != nil {
		panic(err)
	}
	var out Profile
	if err := json.Unmarshal(raw, &out); err != nil {
		panic(err)
	}
	return out
}

func TestProfileCloneIsolation(t *testing.T) {
	p := populatedCloneProfile(2)
	p.Contracts = []Contract{{ID: "1-catch", Progress: 1}}
	p.SavedLoadouts[1] = p.SavedLoadouts[0]
	before := jsonCloneProfile(p)
	copy := CloneProfile(p)
	if !reflect.DeepEqual(copy, before) {
		t.Fatal("clone changed profile values")
	}
	copy.OwnedGear[0] = "changed"
	copy.Basket[0].Locked = true
	copy.BaitStock["basic"]++
	copy.Records["gold"] = Record{}
	copy.CollectionClaims["lake"]++
	copy.DebrisStock["boot"]++
	copy.Contracts[0].Progress++
	copy.SavedLoadouts[0].Rod = "changed"
	if copy.SavedLoadouts[1].Rod != "bambooPole" || !reflect.DeepEqual(p, before) {
		t.Fatal("mutating the clone changed another saved loadout or the source")
	}
	for _, claims := range []map[string]int64{nil, {}} {
		p.CollectionClaims = claims
		if !reflect.DeepEqual(CloneProfile(p), jsonCloneProfile(p)) {
			t.Fatal("empty optional receipts changed representation")
		}
	}
}

// Compare the same bounded profile values without including fixture setup.
func BenchmarkProfileClone(b *testing.B) {
	for _, size := range []int{0, MaxBasket} {
		p := populatedCloneProfile(size)
		for _, implementation := range []struct {
			name string
			copy func(Profile) Profile
		}{{"json", jsonCloneProfile}, {"typed", CloneProfile}} {
			b.Run(fmt.Sprintf("basket_%d/%s", size, implementation.name), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					copy := implementation.copy(p)
					if copy.NextCatchID != p.NextCatchID {
						b.Fatal("clone changed the catch sequence")
					}
				}
			})
		}
	}
}
