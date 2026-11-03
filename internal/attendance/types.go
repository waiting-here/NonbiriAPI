package attendance

import (
	"database/sql"
	"errors"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/limitedactivities"
	"github.com/waiting-here/NonbiriAPI/internal/rewardasset"
)

var (
	ErrInvalid     = errors.New("attendance: invalid request")
	ErrConflict    = errors.New("attendance: request or configuration changed")
	ErrNotFound    = errors.New("attendance: period not found")
	ErrClosed      = errors.New("attendance: period is not accepting sign-ins")
	ErrExportLimit = errors.New("attendance: export limit exceeded")
)

const MaxDays = 3660
const MaxTiers = 366
const MaxRewardItems = 1024

type Config struct {
	Database     *sql.DB
	ReadDatabase *sql.DB
	Users        limitedactivities.UserAuthorizer
	Admins       limitedactivities.AdminAuthorizer
	Gate         limitedactivities.AdmissionGate
	Keys         limitedactivities.KeyDeriver
	Activity     limitedactivities.ActivityRecorder
	Now          func() time.Time
}
type TierInput struct {
	Days    int                `json:"days"`
	Rewards []rewardasset.Item `json:"rewards"`
}
type Input struct {
	Title       string      `json:"title"`
	Description string      `json:"description"`
	StartDate   string      `json:"start_date"`
	EndDate     string      `json:"end_date"`
	Mode        string      `json:"mode"`
	Published   bool        `json:"published"`
	Tiers       []TierInput `json:"tiers"`
}
type Edit struct {
	Input
	ExpectedRevision int64 `json:"expected_revision"`
}
type Tier struct {
	TierInput
	Awarded   bool                 `json:"awarded"`
	Results   []rewardasset.Result `json:"results,omitempty"`
	AwardedAt *int64               `json:"awarded_at,omitempty"`
}
type Period struct {
	ID                    string `json:"id"`
	Title                 string `json:"title"`
	Description           string `json:"description"`
	StartDate             string `json:"start_date"`
	EndDate               string `json:"end_date"`
	Mode                  string `json:"mode"`
	Published             bool   `json:"published"`
	Revision              int64  `json:"revision"`
	TimezoneOffsetMinutes int    `json:"timezone_offset_minutes"`
	CreatedAt             int64  `json:"created_at"`
	Status                string `json:"status"`
	SiteDate              string `json:"site_date"`
	ConfigEditable        bool   `json:"config_editable"`
	Available             bool   `json:"available"`
	CanSignIn             bool   `json:"can_sign_in"`
	SignedToday           bool   `json:"signed_today"`
	TotalDays             int    `json:"total_days"`
	CurrentStreak         int    `json:"current_streak"`
	BestStreak            int    `json:"best_streak"`
	EligibleDays          int    `json:"eligible_days"`
	Tiers                 []Tier `json:"tiers"`
	startsAt, endsAt      int64
	actor                 sql.NullInt64
}
type Page struct {
	Items                 []Period `json:"items"`
	Page                  int      `json:"page"`
	PageSize              int      `json:"page_size"`
	Total                 int64    `json:"total"`
	SiteDate              string   `json:"site_date"`
	TimezoneOffsetMinutes int      `json:"timezone_offset_minutes"`
	Available             bool     `json:"available"`
	UnavailableReason     string   `json:"unavailable_reason,omitempty"`
}
type SignInInput struct {
	SiteDate string `json:"site_date"`
}
type Award struct {
	Days      int                  `json:"days"`
	Results   []rewardasset.Result `json:"results"`
	AwardedAt int64                `json:"awarded_at"`
}
type SignInReceipt struct {
	PeriodID      string  `json:"period_id"`
	SiteDate      string  `json:"site_date"`
	SignedAt      int64   `json:"signed_at"`
	AlreadySigned bool    `json:"already_signed"`
	Awards        []Award `json:"awards"`
}
type SignInResult struct {
	SignInReceipt
	Period Period `json:"period"`
}
