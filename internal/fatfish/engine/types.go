// Package engine implements the versioned, deterministic Fat Fish rules.
// Coordinates are signed multiples of 1/64 logical pixel. A Level contains
// gameplay only; descriptions, display text, and account state live elsewhere.
package engine

import "context"

const (
	LegacyEngineVersion = 1
	EngineVersion       = 2
	ScoringVersion      = 1
	FieldWidth          = 480 * 64
	FieldHeight         = 560 * 64
	DragBuffer          = 128 * 64
	FishRadius          = 8 * 64
	TicksPerSecond      = 60
	Substeps            = 2
	MaxInputs           = 72000
	MaxInputBytes       = 4 << 20
	MaxLevelBytes       = 256 << 10
)

type Point struct {
	X int64 `json:"x"`
	Y int64 `json:"y"`
}

type Polygon struct {
	Outer []Point   `json:"outer"`
	Holes [][]Point `json:"holes"`
}

type FishSpec struct {
	ID      int   `json:"id"`
	X       int64 `json:"x"`
	Y       int64 `json:"y"`
	Heading int   `json:"heading"`
}

type Shape struct {
	ID      int     `json:"id"`
	Polygon Polygon `json:"polygon"`
}

// Tool polygons use coordinates relative to (X,Y). Static polygons are in
// field coordinates. The editor can place tools in the 128-pixel outer buffer.
type Tool struct {
	ID          int     `json:"id"`
	ResourceKey string  `json:"resource_key"`
	Polygon     Polygon `json:"polygon"`
	Placed      bool    `json:"placed"`
	X           int64   `json:"x"`
	Y           int64   `json:"y"`
}

type Hazard struct {
	ID      int     `json:"id"`
	Polygon Polygon `json:"polygon"`
}

type Bowl struct {
	ID       int     `json:"id"`
	Polygon  Polygon `json:"polygon"`
	Required int     `json:"required"`
	Capacity int     `json:"capacity"`
}

type Switch struct {
	ID      int     `json:"id"`
	Polygon Polygon `json:"polygon"`
	Mode    string  `json:"mode"` // latch or hold
}

type Gate struct {
	ID            int     `json:"id"`
	Polygon       Polygon `json:"polygon"`
	InitiallyOpen bool    `json:"initially_open"`
	Mode          string  `json:"mode"` // any or all
	SwitchIDs     []int   `json:"switch_ids"`
}

type Direction struct {
	ID      int     `json:"id"`
	Polygon Polygon `json:"polygon"`
	Mode    string  `json:"mode"` // entry or oneway
	Heading int     `json:"heading"`
}

type Level struct {
	Format               string      `json:"format"`
	FormatVersion        int         `json:"format_version"`
	EngineVersion        int         `json:"engine_version"`
	ScoringVersion       int         `json:"scoring_version"`
	DurationSeconds      int         `json:"duration_seconds"`
	SpeedPixelsPerSecond int         `json:"speed_pixels_per_second"`
	Thresholds           [3]int      `json:"thresholds"`
	Fish                 []FishSpec  `json:"fish"`
	Tools                []Tool      `json:"tools"`
	Solids               []Shape     `json:"solids"`
	Hazards              []Hazard    `json:"hazards"`
	Bowls                []Bowl      `json:"bowls"`
	Switches             []Switch    `json:"switches"`
	Gates                []Gate      `json:"gates"`
	Directions           []Direction `json:"directions"`
}

// InputTuple is encoded as [tick,seq,op,tool_id,x?,y?]. The optional
// coordinates are present only for place, including zero coordinates.
type InputTuple struct {
	Tick   int
	Seq    int
	Op     string
	ToolID int
	X      int64
	Y      int64
}

type FishState struct {
	ID             int          `json:"id"`
	X              int64        `json:"x"`
	Y              int64        `json:"y"`
	Heading        int          `json:"heading"`
	Status         string       `json:"status"` // walking, fed, lost
	BowlID         int          `json:"bowl_id"`
	TurnDir        int          `json:"turn_dir"`
	TurnDistance   int64        `json:"turn_distance"`
	FlowID         int          `json:"flow_id"`
	SpeedRemainder int64        `json:"speed_remainder"`
	XRema          int64        `json:"x_remainder"`
	YRema          int64        `json:"y_remainder"`
	RNG            [4]uint32    `json:"rng"`
	Motion         *MotionState `json:"motion,omitempty"`
}

// MotionState is serialized only by version 2. Version 1 keeps its original
// fish state fields and digest bytes.
type MotionState struct {
	TurnRemainder    int64 `json:"turn_remainder"`
	AmbiguousTurnDir int   `json:"ambiguous_turn_dir"`
}

type ToolState struct {
	ID     int   `json:"id"`
	Placed bool  `json:"placed"`
	X      int64 `json:"x"`
	Y      int64 `json:"y"`
}

type SwitchState struct {
	ID        int  `json:"id"`
	Active    bool `json:"active"`
	Triggered bool `json:"triggered"`
	Occupied  bool `json:"occupied"`
}

type GateState struct {
	ID      int  `json:"id"`
	Open    bool `json:"open"`
	Pending bool `json:"pending"`
}

type BowlState struct {
	ID    int `json:"id"`
	Count int `json:"count"`
}

type EngineState struct {
	Tick          int           `json:"tick"`
	SolidRevision int           `json:"solid_revision"`
	Fish          []FishState   `json:"fish"`
	Tools         []ToolState   `json:"tools"`
	Switches      []SwitchState `json:"switches"`
	Gates         []GateState   `json:"gates"`
	Bowls         []BowlState   `json:"bowls"`
	Terminal      bool          `json:"terminal"`
	Reason        string        `json:"reason"`
}

type ReplayResult struct {
	EngineVersion  int         `json:"engine_version"`
	ScoringVersion int         `json:"scoring_version"`
	ContentHash    string      `json:"content_hash"`
	FinalStateHash string      `json:"final_state_hash"`
	TerminalTick   int         `json:"terminal_tick"`
	Reason         string      `json:"reason"`
	Fed            int         `json:"fed"`
	Total          int         `json:"total"`
	BowlCounts     []BowlState `json:"bowl_counts"`
	Passed         bool        `json:"passed"`
	Stars          int         `json:"stars"`
	ScoreUnits     int64       `json:"score_units"`
}

type ReplayOptions struct {
	Context context.Context
	// OnTick receives immutable copies for golden vectors or local rendering.
	OnTick func(EngineState) error
}
