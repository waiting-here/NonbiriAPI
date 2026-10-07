package rules

import (
	"encoding/json"
	"math"
	"sort"
)

type Loadout struct {
	Rod     string `json:"rod"`
	Tackle1 string `json:"tackle1,omitempty"`
	Tackle2 string `json:"tackle2,omitempty"`
	Tackle3 string `json:"tackle3,omitempty"`
	Bait    string `json:"bait,omitempty"`
}
type Catch struct {
	ID      uint64 `json:"id"`
	Kind    string `json:"kind"`
	Length  int    `json:"length"`
	Quality int    `json:"quality"`
	Perfect bool   `json:"perfect"`
	Locked  bool   `json:"locked"`
}
type Record struct {
	Caught       Amount `json:"caught"`
	MaxLength    int    `json:"maxLength"`
	BestQuality  int    `json:"bestQuality"`
	PerfectCount Amount `json:"perfectCount"`
}
type Contract struct {
	ID       string `json:"id"`
	Slot     string `json:"slot"`
	Day      uint64 `json:"day"`
	Type     string `json:"type"`
	Target   int    `json:"target"`
	Kind     string `json:"kind,omitempty"`
	Location string `json:"location,omitempty"`
	Progress int    `json:"progress"`
	Status   string `json:"status"`
}
type Profile struct {
	Coins              Amount            `json:"coins"`
	XP                 Amount            `json:"xp"`
	First              string            `json:"first,omitempty"`
	Second             string            `json:"second,omitempty"`
	Third              string            `json:"third,omitempty"`
	Fourth             string            `json:"fourth,omitempty"`
	Location           string            `json:"location"`
	Day                uint64            `json:"day"`
	ClockMinutes       float64           `json:"clockMinutes"`
	OwnedGear          []string          `json:"ownedGear"`
	Equipped           Loadout           `json:"equipped"`
	BaitStock          map[string]int    `json:"baitStock"`
	SelectedBait       string            `json:"selectedBait,omitempty"`
	Basket             []Catch           `json:"basket"`
	Records            map[string]Record `json:"records"`
	NextCatchID        uint64            `json:"nextCatchId"`
	SavedLoadouts      [3]*Loadout       `json:"savedLoadouts"`
	DebrisStock        map[string]int    `json:"debrisStock"`
	TrashRecovered     Amount            `json:"trashRecovered"`
	TreasureOpened     Amount            `json:"treasureOpened"`
	Contracts          []Contract        `json:"contracts"`
	ContractsDay       uint64            `json:"contractsDay"`
	CompletedContracts Amount            `json:"completedContracts"`
	Streak             Amount            `json:"streak"`
}

func InitialProfile() Profile {
	p := Profile{Coins: "0", XP: "0", Location: "lake", Day: 1, ClockMinutes: 360, OwnedGear: []string{"bambooPole"}, Equipped: Loadout{Rod: "bambooPole"}, BaitStock: map[string]int{}, Basket: []Catch{}, Records: map[string]Record{}, NextCatchID: 1, DebrisStock: map[string]int{}, TrashRecovered: "0", TreasureOpened: "0", Contracts: []Contract{}, CompletedContracts: "0", Streak: "0"}
	for k := range catalog.Baits {
		p.BaitStock[k] = 0
	}
	for k := range catalog.Debris {
		p.DebrisStock[k] = 0
	}
	return p
}
func CloneProfile(p Profile) Profile {
	b, e := json.Marshal(p)
	if e != nil {
		panic(e)
	}
	var q Profile
	if e = json.Unmarshal(b, &q); e != nil {
		panic(e)
	}
	return q
}
func (p Profile) Caught() Amount {
	n := Amount("0")
	for _, r := range p.Records {
		var e error
		n, e = n.Add(r.Caught)
		if e != nil {
			panic(e)
		}
	}
	return n
}
func (p Profile) Copies(id string) int {
	n := 0
	for _, s := range p.OwnedGear {
		if s == id {
			n++
		}
	}
	return n
}
func (p Profile) Effects(l Loadout) Effects {
	e := Effects{Acceleration: 1, ProgressGain: 1, ProgressLoss: 1, RareWeight: 1, LegendWeight: 1, BottomBounce: float64(2.0 / 3.0)}
	rod := catalog.Gear[l.Rod]
	if l.Rod == "trainingRod" {
		e.ProgressLoss = float64(2.0 / 3.0)
	}
	trap := 0
	for _, id := range []string{l.Tackle1, l.Tackle2, l.Tackle3}[:rod.TackleSlots] {
		switch id {
		case "corkBobber":
			e.BarHeight = float64(e.BarHeight + float64(24.0/568.0))
		case "leadBobber":
			e.BottomBounce = float64(e.BottomBounce * 0.1)
		case "trapBobber":
			trap++
		case "barbedHook":
			e.BarbedCount++
		case "qualityBobber":
			e.QualityBonus++
		case "curiosityLure":
			e.LegendWeight = 2
		case "spinner":
			e.SpinnerSeconds += 5
		case "dressedSpinner":
			e.SpinnerSeconds += 10
		case "sonarBobber":
			e.Sonar = true
		}
	}
	if trap == 1 {
		e.ProgressLoss = float64(e.ProgressLoss * float64(2.0/3.0))
	}
	if trap >= 2 {
		e.ProgressLoss = float64(e.ProgressLoss * 0.5)
	}
	return e
}
func (p Profile) BarHeight(e Effects, rod, bait string) float64 {
	skill := 0.0
	if p.First == "steady" {
		skill = float64(8.0 / 568.0)
	}
	if p.Fourth == "master" {
		skill = float64(skill + float64(8.0/568.0))
	}
	base := float64(float64(float64(96.0/568.0)+float64(float64(LevelFromXP(p.XP))*float64(4.0/568.0))) + skill)
	minimum := 0.0
	if rod == "trainingRod" {
		minimum = float64(136.0 / 568.0)
	}
	return clamp(float64(float64(math.Max(minimum, base)+e.BarHeight)+catalog.Baits[bait].Effects.BarHeight), 0.08, 0.5)
}
func (p *Profile) AdvanceClock(minutes float64) error {
	if !finite(minutes) || minutes < 0 {
		return ErrInvalid
	}
	total := float64(p.ClockMinutes + minutes)
	days := uint64(math.Floor(total / 1440))
	if days > MaxSafeInteger-p.Day {
		return ErrOverflow
	}
	p.Day += days
	p.ClockMinutes = math.Mod(total, 1440)
	if days > 0 {
		board := make([]Contract, 0, len(p.Contracts))
		for _, q := range p.Contracts {
			if q.Status == "active" || q.Day == p.Day {
				board = append(board, q)
			}
		}
		p.Contracts = board
	}
	return nil
}
func finite(n float64) bool { return !math.IsNaN(n) && !math.IsInf(n, 0) }
func CatchValue(c Catch) int {
	f, ok := Fish(c.Kind)
	if !ok || c.Quality < 0 || c.Quality > 3 {
		return 0
	}
	return int(math.Floor(float64(float64(f.BasePrice) * []float64{1, 1.25, 1.5, 2}[c.Quality])))
}
func (p *Profile) AwardBait(id string, count int) (added, converted int, err error) {
	b, ok := catalog.Baits[id]
	if !ok || count < 0 {
		return 0, 0, ErrInvalid
	}
	added = min(count, MaxBait-p.BaitStock[id])
	p.BaitStock[id] += added
	converted = (count - added) * b.Cost
	err = addSmall(&p.Coins, converted)
	return
}
func (p *Profile) advanceContracts(event string, f FishType, perfect bool) {
	for i := range p.Contracts {
		q := &p.Contracts[i]
		if q.Status == "active" && ((q.Type == "catch" && event == "fish" && f.Location == q.Location) || (q.Type == "perfect" && event == "fish" && perfect) || (q.Type == "cleanup" && event == "cleanup")) {
			q.Progress = min(q.Target, q.Progress+1)
		}
	}
}
func (p *Profile) StoreCatch(f FishType, length, quality int, perfect bool) error {
	if p.NextCatchID >= MaxSafeInteger {
		return ErrOverflow
	}
	r, ok := p.Records[f.Kind]
	if !ok {
		r = Record{Caught: "0", PerfectCount: "0"}
	}
	if e := increment(&r.Caught); e != nil {
		return e
	}
	r.MaxLength = max(r.MaxLength, length)
	r.BestQuality = max(r.BestQuality, quality)
	if perfect {
		if e := increment(&r.PerfectCount); e != nil {
			return e
		}
	}
	p.Records[f.Kind] = r
	c := Catch{ID: p.NextCatchID, Kind: f.Kind, Length: length, Quality: quality, Perfect: perfect}
	p.NextCatchID++
	if len(p.Basket) >= MaxBasket {
		if e := addSmall(&p.Coins, CatchValue(c)); e != nil {
			return e
		}
	} else {
		p.Basket = append(p.Basket, c)
	}
	p.advanceContracts("fish", f, perfect)
	return nil
}
func (p Profile) ContractProgress(q Contract) int {
	if q.Type != "delivery" {
		return q.Progress
	}
	n := 0
	for _, c := range p.Basket {
		if c.Kind == q.Kind && !c.Locked {
			n++
		}
	}
	return min(n, q.Target)
}
func (p Profile) SkillOptions(tier int) []string {
	switch tier {
	case 5:
		return []string{"steady", "tracker"}
	case 10:
		if p.First == "steady" {
			return []string{"calm", "control"}
		}
		if p.First == "tracker" {
			return []string{"legendHunter", "brawler"}
		}
	case 15:
		return []string{"patience", "reader"}
	case 20:
		return []string{"master", "deepSeeker"}
	}
	return nil
}
func (p Profile) PendingSkillTier() int {
	level := LevelFromXP(p.XP)
	if level >= 5 && p.First == "" {
		return 5
	}
	if level >= 10 && p.First != "" && p.Second == "" {
		return 10
	}
	if level >= 15 && p.Second != "" && p.Third == "" {
		return 15
	}
	if level >= 20 && p.Third != "" && p.Fourth == "" {
		return 20
	}
	return 0
}
func sortDelivery(a []Catch) {
	sort.Slice(a, func(i, j int) bool {
		v, w := CatchValue(a[i]), CatchValue(a[j])
		if v == w {
			return a[i].ID < a[j].ID
		}
		return v < w
	})
}
