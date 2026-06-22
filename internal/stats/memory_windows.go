//go:build windows

package stats

import (
	"os"
	"runtime"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type PROCESS_MEMORY_COUNTERS struct {
	CB                         uint32
	PageFaultCount             uint32
	PeakWorkingSetSize         uintptr
	WorkingSetSize             uintptr
	QuotaPeakPagedPoolUsage    uintptr
	QuotaPagedPoolUsage        uintptr
	QuotaPeakNonPagedPoolUsage uintptr
	QuotaNonPagedPoolUsage     uintptr
	PagefileUsage              uintptr
	PeakPagefileUsage          uintptr
	PrivateUsage               uintptr
}

var (
	psapi                = windows.NewLazySystemDLL("psapi.dll")
	getProcessMemoryInfo = psapi.NewProc("GetProcessMemoryInfo")
	kernel32             = windows.NewLazySystemDLL("kernel32.dll")
	getProcessTimes      = kernel32.NewProc("GetProcessTimes")
)

type processCpuRecord struct {
	kernelTime uint64
	userTime   uint64
}

var (
	lastCpuQueryTime int64
	lastProcessCpu   = make(map[uint32]processCpuRecord)
)

func GetAppMemoryStats() (uint64, int, float64, error) {
	myPid := uint32(os.Getpid())

	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return 0, 0, 0.0, err
	}
	defer windows.CloseHandle(snapshot)

	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))

	parentToChildren := make(map[uint32][]uint32)

	err = windows.Process32First(snapshot, &entry)
	for err == nil {
		parentToChildren[entry.ParentProcessID] = append(parentToChildren[entry.ParentProcessID], entry.ProcessID)
		err = windows.Process32Next(snapshot, &entry)
	}

	pidsToQuery := []uint32{myPid}
	queue := []uint32{myPid}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		if children, exists := parentToChildren[current]; exists {
			for _, child := range children {
				pidsToQuery = append(pidsToQuery, child)
				queue = append(queue, child)
			}
		}
	}

	var totalMemory uint64
	var count int
	var totalCpuTimeDiff uint64

	nowNano := time.Now().UnixNano()
	newProcessCpu := make(map[uint32]processCpuRecord)

	for _, pid := range pidsToQuery {
		h, err := windows.OpenProcess(windows.PROCESS_QUERY_INFORMATION|windows.PROCESS_VM_READ, false, pid)
		if err != nil {
			continue
		}

		// 1. Query memory counters
		var counters PROCESS_MEMORY_COUNTERS
		counters.CB = uint32(unsafe.Sizeof(counters))

		r1, _, _ := getProcessMemoryInfo.Call(
			uintptr(h),
			uintptr(unsafe.Pointer(&counters)),
			uintptr(counters.CB),
		)

		if r1 != 0 {
			// Using PrivateUsage instead of WorkingSetSize to avoid shared DLL double counting.
			totalMemory += uint64(counters.PrivateUsage)
			count++
		}

		// 2. Query process times for CPU usage calculation
		var creationTime, exitTime, kernelTime, userTime windows.Filetime
		rCpu, _, _ := getProcessTimes.Call(
			uintptr(h),
			uintptr(unsafe.Pointer(&creationTime)),
			uintptr(unsafe.Pointer(&exitTime)),
			uintptr(unsafe.Pointer(&kernelTime)),
			uintptr(unsafe.Pointer(&userTime)),
		)
		windows.CloseHandle(h)

		if rCpu != 0 {
			kVal := (uint64(kernelTime.HighDateTime) << 32) | uint64(kernelTime.LowDateTime)
			uVal := (uint64(userTime.HighDateTime) << 32) | uint64(userTime.LowDateTime)

			if lastRecord, exists := lastProcessCpu[pid]; exists {
				kDiff := kVal - lastRecord.kernelTime
				uDiff := uVal - lastRecord.userTime
				totalCpuTimeDiff += (kDiff + uDiff) * 100 // Convert 100ns units to nanoseconds
			}

			newProcessCpu[pid] = processCpuRecord{kernelTime: kVal, userTime: uVal}
		}
	}

	var cpuPercent float64
	if lastCpuQueryTime > 0 {
		deltaNs := nowNano - lastCpuQueryTime
		if deltaNs > 0 {
			numCpu := runtime.NumCPU()
			cpuPercent = (float64(totalCpuTimeDiff) / (float64(deltaNs) * float64(numCpu))) * 100.0
			if cpuPercent > 100.0 {
				cpuPercent = 100.0
			}
			if cpuPercent < 0.0 {
				cpuPercent = 0.0
			}
		}
	}

	lastCpuQueryTime = nowNano
	lastProcessCpu = newProcessCpu

	return totalMemory, count, cpuPercent, nil
}
