//go:build !windows

package app

import "golang.org/x/sys/unix"

func loadCPUTimeNS() int64 {
	var usage unix.Rusage
	if err := unix.Getrusage(unix.RUSAGE_SELF, &usage); err != nil {
		return -1
	}
	return int64(usage.Utime.Sec+usage.Stime.Sec)*1e9 + int64(usage.Utime.Usec+usage.Stime.Usec)*1e3
}
