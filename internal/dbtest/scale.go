package dbtest

import (
	"runtime/debug"
	"testing"
	"testing/synctest"
	"time"
)

// Scale keeps ordinary runs on the real clock to verify production deadlines.
// Race runs use a controlled clock so instrumentation cost does not replace
// the full-data correctness checks with an incidental timeout.
// Call Scale inside each subtest; synctest does not allow nested t.Run calls.
func Scale(t *testing.T, run func(*testing.T)) {
	t.Helper()
	instrumented := false
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "-race" && setting.Value == "true" {
				instrumented = true
			}
		}
	}
	started := time.Now()
	if instrumented {
		synctest.Test(t, run)
	} else {
		run(t)
	}
	t.Logf("scale test wall time: %s (race=%t)", time.Since(started), instrumented)
}
