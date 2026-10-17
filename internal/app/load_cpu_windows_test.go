package app

import "golang.org/x/sys/windows"

func loadCPUTimeNS() int64 {
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(windows.CurrentProcess(), &created, &exited, &kernel, &user); err != nil {
		return -1
	}
	ticks := func(f windows.Filetime) uint64 { return uint64(f.HighDateTime)<<32 | uint64(f.LowDateTime) }
	return int64(ticks(kernel)+ticks(user)) * 100
}
