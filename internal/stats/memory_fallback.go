//go:build !windows && !darwin

package stats

import "runtime"

func GetAppMemoryStats() (uint64, int, error) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.Sys, 1, nil
}
