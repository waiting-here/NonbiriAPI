//go:build !windows

package forward

import "time"

var benchmarkEpoch = time.Now()

func benchmarkNow() int64 { return time.Since(benchmarkEpoch).Nanoseconds() }
