package antiabuse

import (
	"strconv"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/credits"
	"github.com/waiting-here/NonbiriAPI/internal/db"
)

// Site-config keys owned by the anti-abuse rail. Keeping the registry here
// gives the runtime policy reader and the administrator station one spelling.
const (
	KeyRPMBanThreshold                  = "rpm_ban_threshold"
	KeyRPMBanWindowSeconds              = "rpm_ban_window_seconds"
	KeyRPMBanDurationSeconds            = "rpm_ban_duration_seconds"
	KeyCharityMinChars                  = "charity_min_chars"
	KeyCharityViolationDeductMilli      = "charity_violation_deduct_milli"
	KeyCharityViolationBanSeconds       = "charity_violation_ban_seconds"
	KeyCharityViolationWindowSeconds    = "charity_violation_window_seconds"
	KeyCharityViolationBanThreshold     = "charity_violation_ban_threshold"
	KeyCharityViolationWindowBanSeconds = "charity_violation_window_ban_seconds"
	KeyCharitySuspendWindowSeconds      = "charity_suspend_window_seconds"
	KeyCharitySuspendThreshold          = "charity_suspend_threshold"
	KeyCharitySuspendDurationSeconds    = "charity_suspend_duration_seconds"
)

const (
	DefaultRPMBanThreshold     = 5
	DefaultRPMBanWindow        = 24 * time.Hour
	DefaultRPMBanDuration      = 24 * time.Hour
	DefaultCharityMinChars     = 20
	DefaultViolationWindow     = 24 * time.Hour
	DefaultSuspendWindow       = 24 * time.Hour
	MaxWindowDuration          = db.MaxBanDurationSeconds
	MaxWindowUsers             = 10000
	MaxEventsPerUser           = 256
	MaxRememberedOperationIDs  = 256
	MaxViolationThreshold      = 4096
	MaxCharityContentRuneCount = 1 << 20
)

// Config is the authoritative anti-abuse snapshot used for one event. It is
// read per request, so administrator changes take effect without stale process
// state. Zero threshold/duration values are intentional feature switches.
type Config struct {
	RPMBanThreshold       int
	RPMBanWindowSeconds   int64
	RPMBanDurationSeconds int64

	CharityMinChars                  int
	CharityViolationDeductMilli      int64
	CharityViolationBanSeconds       int64
	CharityViolationWindowSeconds    int64
	CharityViolationBanThreshold     int
	CharityViolationWindowBanSeconds int64
	CharitySuspendWindowSeconds      int64
	CharitySuspendThreshold          int
	CharitySuspendDurationSeconds    int64
}

func DefaultConfig() Config {
	return Config{
		RPMBanThreshold:                  DefaultRPMBanThreshold,
		RPMBanWindowSeconds:              int64(DefaultRPMBanWindow / time.Second),
		RPMBanDurationSeconds:            int64(DefaultRPMBanDuration / time.Second),
		CharityMinChars:                  DefaultCharityMinChars,
		CharityViolationWindowSeconds:    int64(DefaultViolationWindow / time.Second),
		CharitySuspendWindowSeconds:      int64(DefaultSuspendWindow / time.Second),
		CharityViolationDeductMilli:      0,
		CharityViolationBanSeconds:       0,
		CharityViolationBanThreshold:     0,
		CharityViolationWindowBanSeconds: 0,
		CharitySuspendThreshold:          0,
		CharitySuspendDurationSeconds:    0,
	}
}

// ConfigReader is intentionally narrower than db.Store so policy tests can
// inject a deterministic configuration source.
type ConfigReader interface {
	GetSiteConfigValue(string) (string, error)
}

func readConfig(reader ConfigReader) Config {
	cfg := DefaultConfig()
	if reader == nil {
		return cfg
	}
	readInt := func(key string, fallback, min, max int) int {
		raw, err := reader.GetSiteConfigValue(key)
		if err != nil || raw == "" {
			return fallback
		}
		n, err := strconv.Atoi(raw)
		if err != nil || strconv.Itoa(n) != raw || n < min || n > max {
			return fallback
		}
		return n
	}
	readInt64 := func(key string, fallback int64, min, max int64) int64 {
		raw, err := reader.GetSiteConfigValue(key)
		if err != nil || raw == "" {
			return fallback
		}
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || strconv.FormatInt(n, 10) != raw || n < min || n > max {
			return fallback
		}
		return n
	}
	readAmount := func(key string, fallback int64) int64 {
		raw, err := reader.GetSiteConfigValue(key)
		if err != nil || raw == "" {
			return fallback
		}
		n, err := credits.ParseAmount(raw)
		if err != nil || n < 0 {
			return fallback
		}
		return n
	}
	maxSeconds := int64(MaxWindowDuration)
	cfg.RPMBanThreshold = readInt(KeyRPMBanThreshold, cfg.RPMBanThreshold, 0, MaxViolationThreshold)
	cfg.RPMBanWindowSeconds = readInt64(KeyRPMBanWindowSeconds, cfg.RPMBanWindowSeconds, 1, maxSeconds)
	cfg.RPMBanDurationSeconds = readInt64(KeyRPMBanDurationSeconds, cfg.RPMBanDurationSeconds, 1, maxSeconds)
	cfg.CharityMinChars = readInt(KeyCharityMinChars, cfg.CharityMinChars, 0, MaxCharityContentRuneCount)
	cfg.CharityViolationDeductMilli = readAmount(KeyCharityViolationDeductMilli, 0)
	cfg.CharityViolationBanSeconds = readInt64(KeyCharityViolationBanSeconds, 0, 0, maxSeconds)
	cfg.CharityViolationWindowSeconds = readInt64(KeyCharityViolationWindowSeconds, cfg.CharityViolationWindowSeconds, 1, maxSeconds)
	cfg.CharityViolationBanThreshold = readInt(KeyCharityViolationBanThreshold, 0, 0, MaxViolationThreshold)
	cfg.CharityViolationWindowBanSeconds = readInt64(KeyCharityViolationWindowBanSeconds, 0, 0, maxSeconds)
	cfg.CharitySuspendWindowSeconds = readInt64(KeyCharitySuspendWindowSeconds, cfg.CharitySuspendWindowSeconds, 1, maxSeconds)
	cfg.CharitySuspendThreshold = readInt(KeyCharitySuspendThreshold, 0, 0, MaxViolationThreshold)
	cfg.CharitySuspendDurationSeconds = readInt64(KeyCharitySuspendDurationSeconds, 0, 0, maxSeconds)
	return cfg
}
