package rules

import (
	_ "embed"
	"encoding/json"
	"math"
)

const TickSeconds = float64(1.0 / 60.0)
const MaxBasket = 80
const MaxBait = 999
const MaxDebris = 9999
const MaxSafeInteger = uint64(9007199254740991)
const SourceSHA256 = "c1962f3f7278493b3af025488bd866fde8aae45fc69ed3a3ee92cf0acde83d0c"

type FishType struct {
	Name        string   `json:"name"`
	Kind        string   `json:"kind"`
	Location    string   `json:"location"`
	Rarity      string   `json:"rarity"`
	Weight      float64  `json:"weight"`
	Behavior    string   `json:"behavior"`
	Style       string   `json:"style"`
	Length      [2]int   `json:"length"`
	Difficulty  float64  `json:"difficulty"`
	BasePrice   int      `json:"basePrice"`
	Periods     []string `json:"periods,omitempty"`
	Weathers    []string `json:"weathers,omitempty"`
	Description string   `json:"description,omitempty"`
}
type Effects struct {
	BarHeight      float64 `json:"barHeight"`
	Acceleration   float64 `json:"acceleration"`
	ProgressGain   float64 `json:"progressGain"`
	ProgressLoss   float64 `json:"progressLoss"`
	RareWeight     float64 `json:"rareWeight"`
	LegendWeight   float64 `json:"legendWeight"`
	BottomBounce   float64 `json:"bottomBounce"`
	BarbedCount    int     `json:"barbedCount"`
	QualityBonus   int     `json:"qualityBonus"`
	SpinnerSeconds float64 `json:"spinnerSeconds"`
	Sonar          bool    `json:"sonar"`
	BiteDelay      float64 `json:"biteDelay"`
	EpicWeight     float64 `json:"epicWeight"`
	FishSpeed      float64 `json:"fishSpeed"`
}
type GearType struct {
	LegendaryRequired int     `json:"legendaryRequired,omitempty"`
	Name              string  `json:"name"`
	Slot              string  `json:"slot"`
	Cost              int     `json:"cost"`
	TackleSlots       int     `json:"tackleSlots"`
	BaitAllowed       bool    `json:"baitAllowed"`
	Description       string  `json:"description"`
	Unlock            string  `json:"unlock,omitempty"`
	Effects           Effects `json:"effects,omitempty"`
}
type BaitType struct {
	Name        string  `json:"name"`
	Cost        int     `json:"cost"`
	Description string  `json:"description"`
	Effects     Effects `json:"effects"`
}
type DebrisType struct {
	Name  string `json:"name"`
	Icon  string `json:"icon"`
	Value int    `json:"value"`
}
type TextType struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Scene       string `json:"scene,omitempty"`
}
type RarityType struct {
	Label string `json:"label"`
	Rank  int    `json:"rank"`
}
type ContractTemplate struct {
	Type   string `json:"type"`
	Target int    `json:"target"`
}
type Catalog struct {
	RarityWeights    map[string]float64          `json:"RARITY_WEIGHTS"`
	Config           map[string]float64          `json:"CONFIG"`
	Rarities         map[string]RarityType       `json:"RARITIES"`
	Locations        map[string]TextType         `json:"LOCATIONS"`
	Fish             []FishType                  `json:"FISH_TYPES"`
	Periods          map[string]string           `json:"PERIODS"`
	Weathers         map[string]string           `json:"WEATHERS"`
	WeatherCycle     []string                    `json:"WEATHER_CYCLE"`
	FishDescriptions map[string]string           `json:"FISH_DESCRIPTIONS"`
	Skills           map[string]TextType         `json:"SKILLS"`
	Gear             map[string]GearType         `json:"GEAR"`
	Baits            map[string]BaitType         `json:"BAITS"`
	Debris           map[string]DebrisType       `json:"DEBRIS"`
	ContractSlots    map[string]ContractTemplate `json:"CONTRACT_SLOTS"`
	QualityNames     []string                    `json:"QUALITY_NAMES"`
}

//go:embed catalog.json
var catalogJSON []byte
var catalog = loadCatalog()

func loadCatalog() Catalog {
	var c Catalog
	if err := json.Unmarshal(catalogJSON, &c); err != nil {
		panic(err)
	}
	return c
}

// CatalogJSON returns an independent copy of the bounded compiled catalog.
func CatalogJSON() []byte { return append([]byte(nil), catalogJSON...) }
func Fish(kind string) (FishType, bool) {
	for _, f := range catalog.Fish {
		if f.Kind == kind {
			return f, true
		}
	}
	return FishType{}, false
}
func Gear(id string) (GearType, bool) { g, ok := catalog.Gear[id]; return g, ok }
func Bait(id string) (BaitType, bool) { b, ok := catalog.Baits[id]; return b, ok }
func clamp(n, a, b float64) float64   { return math.Max(a, math.Min(b, n)) }
func round(n float64) float64         { return math.Floor(float64(n + 0.5)) }
func defaultOne(n float64) float64 {
	if n == 0 {
		return 1
	}
	return n
}
func PeriodAt(minutes float64) string {
	if minutes < 300 || minutes >= 1260 {
		return "night"
	}
	if minutes < 540 {
		return "dawn"
	}
	if minutes < 1020 {
		return "day"
	}
	return "dusk"
}
func WeatherForDay(day uint64) string {
	return catalog.WeatherCycle[(day-1)%uint64(len(catalog.WeatherCycle))]
}
func available(f FishType, p, w string) bool {
	return (len(f.Periods) == 0 || contains(f.Periods, p)) && (len(f.Weathers) == 0 || contains(f.Weathers, w))
}
func contains(a []string, x string) bool {
	for _, v := range a {
		if v == x {
			return true
		}
	}
	return false
}

var xpThresholds = [...]uint64{0, 100, 380, 770, 1300, 2150, 3300, 4800, 6900, 10000, 15000}

func XPForLevel(level int) uint64 {
	if level%2 == 0 {
		return xpThresholds[level/2]
	}
	return (xpThresholds[level/2] + xpThresholds[level/2+1]) / 2
}
func LevelFromXP(xp Amount) int {
	level := 0
	for level < 20 && xp.Cmp(NewAmount(XPForLevel(level+1))) >= 0 {
		level++
	}
	return level
}
