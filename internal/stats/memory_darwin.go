//go:build darwin

package stats

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

func GetAppMemoryStats() (uint64, int, float64, error) {
	myPid := os.Getpid()

	cmd := exec.Command("ps", "-ax", "-o", "pid,ppid,rss")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return 0, 0, err
	}

	lines := strings.Split(out.String(), "\n")
	if len(lines) < 2 {
		return 0, 0, fmt.Errorf("unexpected ps output")
	}

	parentToChildren := make(map[int][]int)
	pidToRssBytes := make(map[int]uint64)

	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}

		pid, err1 := strconv.Atoi(fields[0])
		ppid, err2 := strconv.Atoi(fields[1])
		rssKB, err3 := strconv.ParseUint(fields[2], 10, 64)
		if err1 != nil || err2 != nil || err3 != nil {
			continue
		}

		parentToChildren[ppid] = append(parentToChildren[ppid], pid)
		pidToRssBytes[pid] = rssKB * 1024
	}

	pidsToQuery := []int{myPid}
	queue := []int{myPid}

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
		if rss, exists := pidToRssBytes[pid]; exists {
			totalMemory += rss
			count++
		}
	}

	return totalMemory, count, 0.0, nil
}
