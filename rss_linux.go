//go:build linux

package printkit

import (
	"bytes"
	"os"
	"strconv"
	"strings"
)

// rssForProfile sums VmRSS for the Chromium process whose command line
// contains the converter's user-data-dir, plus that process's descendants.
func rssForProfile(dir string) uint64 {
	if dir == "" {
		return 0
	}
	pid := pidForProfile(dir)
	if pid == 0 {
		return 0
	}
	return treeRSS(pid)
}

func pidForProfile(dir string) int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0
	}
	needle := []byte(dir)
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 {
			continue
		}
		cmd, err := os.ReadFile("/proc/" + e.Name() + "/cmdline")
		if err != nil || !bytes.Contains(cmd, needle) {
			continue
		}
		return pid
	}
	return 0
}

func treeRSS(pid int) uint64 {
	seen := map[int]bool{}
	var walk func(int) uint64
	walk = func(pid int) uint64 {
		if pid <= 0 || seen[pid] {
			return 0
		}
		seen[pid] = true
		sum := vmRSS(pid)
		for _, child := range childPIDs(pid) {
			sum += walk(child)
		}
		return sum
	}
	return walk(pid)
}

func childPIDs(pid int) []int {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/task/" + strconv.Itoa(pid) + "/children")
	if err != nil {
		return nil
	}
	fields := strings.Fields(string(b))
	out := make([]int, 0, len(fields))
	for _, f := range fields {
		n, err := strconv.Atoi(f)
		if err == nil {
			out = append(out, n)
		}
	}
	return out
}

func vmRSS(pid int) uint64 {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/status")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(line, "VmRSS:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0
		}
		kb, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return 0
		}
		return kb * 1024
	}
	return 0
}
