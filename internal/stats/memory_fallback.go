//go:build !windows && !darwin

package stats

import "runtime"

func GetAppMemoryStats() (uint64, int, float64, error) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.Sys, 1, 0.0, nil
}
