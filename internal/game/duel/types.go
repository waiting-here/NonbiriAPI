package duel

import (
	"encoding/json"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/game"
)

const QueueSeconds int64 = 120
const RetentionSeconds int64 = 30 * 24 * 60 * 60
const QueueCapacity = 4096
const maxDecisionTime int64 = 253399708679

type Rates struct {
	Platform int `json:"platform"`
	Welfare  int `json:"welfare"`
	Thursday int `json:"thursday"`
}
type Terms struct {
	Game         string `json:"game"`
	Mode         string `json:"mode"`
	Ticket       string `json:"ticket"`
	Rake         Rates  `json:"rake_bp"`
	RulesVersion int    `json:"rules_version"`
	ContentHash  string `json:"content_hash"`
}
type Resolution struct {
	Round     int             `json:"round"`
	StartedAt int64           `json:"started_at"`
	EndsAt    int64           `json:"ends_at"`
	Summary   json.RawMessage `json:"summary"`
}
type RoundStart struct {
	Round     int             `json:"round"`
	StartedAt int64           `json:"started_at"`
	Events    json.RawMessage `json:"events"`
}
type Queue struct {
	ID           string          `json:"id"`
	Revision     string          `json:"revision"`
	Mode         string          `json:"mode"`
	Deadline     int64           `json:"deadline"`
	Ticket       string          `json:"ticket"`
	Payment      game.Payment    `json:"payment"`
	TermsHash    string          `json:"terms_hash"`
	RulesVersion int             `json:"rules_version"`
	Loadout      json.RawMessage `json:"loadout,omitempty"`
}
type State struct {
	Profiles     *[2]Profile     `json:"profiles,omitempty"`
	ID           string          `json:"id"`
	Game         string          `json:"game"`
	Mode         string          `json:"mode"`
	RulesVersion int             `json:"rules_version"`
	ContentHash  string          `json:"content_hash"`
	Revision     string          `json:"revision"`
	PhaseSeq     string          `json:"phase_seq"`
	Phase        string          `json:"phase"`
	Round        int             `json:"round"`
	Deadline     *int64          `json:"deadline"`
	ServerNow    int64           `json:"server_now"`
	You          int             `json:"you"`
	Locked       [2]bool         `json:"locked"`
	Ticket       string          `json:"ticket"`
	Rake         Rates           `json:"rake_bp"`
	OwnPayment   game.Payment    `json:"own_payment"`
	View         json.RawMessage `json:"view"`
	Resolution   *Resolution     `json:"resolution"`
	RoundStart   *RoundStart     `json:"round_start"`
}
type ResultSummary struct {
	Profiles     *[2]Profile     `json:"profiles,omitempty"`
	ID           string          `json:"id"`
	Game         string          `json:"game"`
	Mode         string          `json:"mode"`
	TerminalAt   int64           `json:"terminal_at"`
	Outcome      string          `json:"outcome"`
	Reason       string          `json:"reason"`
	Scores       [2]int64        `json:"scores"`
	OwnPayment   game.Payment    `json:"own_payment"`
	OwnRefund    game.Payment    `json:"own_refund"`
	PrizeGeneral string          `json:"prize_general"`
	Rake         RakeAmounts     `json:"rake"`
	Resolution   *Resolution     `json:"resolution"`
	You          int             `json:"you"`
	View         json.RawMessage `json:"view"`
}
type RakeAmounts struct {
	Platform string `json:"platform"`
	Welfare  string `json:"welfare"`
	Thursday string `json:"thursday"`
}
type Home struct {
	ServerNow    int64          `json:"server_now"`
	Queue        *Queue         `json:"queue"`
	Current      *State         `json:"current"`
	LatestResult *ResultSummary `json:"latest_result"`
}
type QueueReceipt struct {
	QueueID  string `json:"queue_id"`
	Revision string `json:"revision"`
	Deadline int64  `json:"deadline"`
}
type ActionReceipt struct {
	SessionID string `json:"session_id"`
	Revision  string `json:"revision"`
	PhaseSeq  string `json:"phase_seq"`
	Locked    bool   `json:"locked"`
}
type MutationResult struct {
	Status   int
	Body     json.RawMessage
	Replayed bool
}
type Identity struct {
	UserID         int64
	SessionBinding string
}
type EnqueueInput struct {
	Identity
	IdempotencyKey    string
	Mode              string
	ExpectedTermsHash string
	DeviceToken       string
	CanonicalSourceIP [16]byte
	Loadout           json.RawMessage
}
type CancelInput struct {
	Identity
	IdempotencyKey   string
	QueueID          string
	ExpectedRevision string
}
type ActionInput struct {
	Identity
	IdempotencyKey string
	SessionID      string
	PhaseSeq       string
	Action         json.RawMessage
}

type storedPayload struct {
	Rules            json.RawMessage    `json:"rules"`
	Resolution       *Resolution        `json:"resolution"`
	RoundStartEvents json.RawMessage    `json:"round_start_events"`
	RoundStartedAt   *int64             `json:"round_started_at"`
	RoundTimeouts    [2]bool            `json:"round_timeouts"`
	TerminalActions  [2]json.RawMessage `json:"terminal_actions"`
}
type roundRecord struct {
	Round       int             `json:"round"`
	Before      json.RawMessage `json:"before"`
	After       json.RawMessage `json:"after"`
	Facts       json.RawMessage `json:"facts"`
	StartEvents json.RawMessage `json:"start_events"`
	Timeouts    [2]bool         `json:"timeouts"`
}
type queueRecord struct {
	ID, Mode                    string
	User                        int64
	Revision                    db.U128
	Created, Deadline           int64
	Terms                       Terms
	TermsHash                   string
	Ticket, GamePaid            int64
	Operation                   string
	GeneralAccount, GameAccount int64
	Device, IP                  [32]byte
	Loadout                     json.RawMessage
}
type seatRecord struct {
	User                  *int64
	GeneralPaid, GamePaid int64
	Loadout, Action       json.RawMessage
	Locked                bool
	TimeoutCount          int
}
type sessionRecord struct {
	ID, Mode, State, Phase             string
	Terms                              Terms
	TermsHash                          string
	Ticket                             int64
	Revision, PhaseSeq                 db.U128
	Round                              int
	Started                            int64
	Deadline                           *int64
	GeneralAccount, GameAccount        int64
	Payload                            storedPayload
	Initial                            json.RawMessage
	TerminalAt, DeleteAt               *int64
	Outcome, Reason                    string
	Winner                             *int
	Scores                             [2]int64
	Prize, Platform, Welfare, Thursday int64
	Operation                          string
	Seats                              [2]seatRecord
}
