package activities

import (
	"fmt"
	"time"
)

var beijingLocation = time.FixedZone("Asia/Shanghai", 8*60*60)

func validateThursdayWindow(periodKey string, opensAt, decisionNow int64) error {
	if opensAt < 0 || opensAt > maxUnixSecond-86400 || decisionNow < 0 || decisionNow > maxUnixSecond {
		return ErrInvalidRequest
	}
	parsed, err := time.ParseInLocation("2006-01-02", periodKey, beijingLocation)
	if err != nil || parsed.Format("2006-01-02") != periodKey || parsed.Weekday() != time.Thursday || parsed.Unix() != opensAt {
		return ErrInvalidRequest
	}
	if decisionNow >= opensAt {
		return ErrConflict
	}
	return nil
}

func naturalThursdayOpen(now, opensAt, closesAt int64) bool {
	return opensAt >= 0 && closesAt == opensAt+86400 && now >= opensAt && now < closesAt
}

func resultVisible(now, opensAt int64) bool {
	if opensAt < 0 || opensAt > maxUnixSecond-7*86400 {
		return false
	}
	return now >= opensAt+86400 && now < opensAt+7*86400
}

func poolCursorScope(poolType, state string) string {
	return fmt.Sprintf("admin-pools/v1/%s/%s", poolType, state)
}
