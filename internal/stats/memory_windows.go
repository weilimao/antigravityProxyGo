//go:build windows

package stats

import (
	"os"
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
}

var (
	psapi                = windows.NewLazySystemDLL("psapi.dll")
	getProcessMemoryInfo = psapi.NewProc("GetProcessMemoryInfo")
)

func GetAppMemoryStats() (uint64, int, error) {
	myPid := uint32(os.Getpid())

	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return 0, 0, err
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

	for _, pid := range pidsToQuery {
		h, err := windows.OpenProcess(windows.PROCESS_QUERY_INFORMATION|windows.PROCESS_VM_READ, false, pid)
		if err != nil {
			continue
		}

		var counters PROCESS_MEMORY_COUNTERS
		counters.CB = uint32(unsafe.Sizeof(counters))

		r1, _, _ := getProcessMemoryInfo.Call(
			uintptr(h),
			uintptr(unsafe.Pointer(&counters)),
			uintptr(counters.CB),
		)
		windows.CloseHandle(h)

		if r1 != 0 {
			totalMemory += uint64(counters.WorkingSetSize)
			count++
		}
	}

	return totalMemory, count, nil
}
