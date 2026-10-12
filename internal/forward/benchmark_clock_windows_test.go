package forward

import (
	"syscall"
	"unsafe"
)

// QueryPerformanceCounter keeps sub-millisecond samples meaningful when the
// Windows system clock runs at a coarser resolution.
var benchmarkCounter = syscall.NewLazyDLL("kernel32.dll").NewProc("QueryPerformanceCounter")
var benchmarkFrequency = func() int64 {
	var value int64
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("QueryPerformanceFrequency")
	if ok, _, err := proc.Call(uintptr(unsafe.Pointer(&value))); ok == 0 {
		panic(err)
	}
	return value
}()

func benchmarkNow() int64 {
	var value int64
	if ok, _, err := benchmarkCounter.Call(uintptr(unsafe.Pointer(&value))); ok == 0 {
		panic(err)
	}
	return value/benchmarkFrequency*1e9 + value%benchmarkFrequency*1e9/benchmarkFrequency
}
