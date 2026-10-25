//go:build linux

package main

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// clkTck is the Linux user-space clock tick (getconf CLK_TCK) on x86_64.
const clkTck = 100

// treeRSS returns the sum of VmRSS, in bytes, for pid and its live descendants.
// Shared mappings are counted once per process.
func treeRSS(pid int) uint64 {
	var sum uint64
	for _, p := range tree(pid) {
		sum += rssBytes(p)
	}
	return sum
}

// processCPU is user+sys from wait4, including children the process reaped.
// It misses Chromium children that are not reaped into the parent. Callers
// keep the greater of this and the live-tree high-water mark.
func processCPU(ps *os.ProcessState) time.Duration {
	if ps == nil {
		return 0
	}
	ru, ok := ps.SysUsage().(*syscall.Rusage)
	if !ok || ru == nil {
		return 0
	}
	return time.Duration(ru.Utime.Nano()) + time.Duration(ru.Stime.Nano())
}

// configureCmd puts the child in its own process group so a timeout can
// signal the group. Chromium may still leave the group; successful runs reap
// it from inside the tool.
func configureCmd(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = 2 * time.Second
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}

// treeCPU returns user+sys CPU time for pid and its live descendants.
// cutime and cstime pick up children that have already been reaped. On this
// kernel those fields stay near zero while the child is still alive, so the
// live sum is the renderer's, GPU's, and zygote's own time as well as the
// parent's. wait4 rusage is not used: Chrome's children are not all reaped
// into the wkhtml-style parent, and that rusage misses most of their CPU.
func treeCPU(pid int) time.Duration {
	_, _, cpu := sampleTree(pid, false)
	return cpu
}

// sampleTree walks pid's live descendants once. withPSS also sums
// smaps_rollup, which is much more expensive than VmRSS on a large Chrome.
func sampleTree(pid int, withPSS bool) (rss, pss uint64, cpu time.Duration) {
	var ticks int64
	for _, p := range tree(pid) {
		rss += rssBytes(p)
		if withPSS {
			pss += pssBytes(p)
		}
		u, s, cu, cs, ok := procTimes(p)
		if !ok {
			continue
		}
		ticks += u + s + cu + cs
	}
	return rss, pss, time.Duration(ticks) * time.Second / clkTck
}

func tree(root int) []int {
	children := map[int][]int{}
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return []int{root}
	}
	for _, ent := range ents {
		pid, err := strconv.Atoi(ent.Name())
		if err != nil {
			continue
		}
		_, _, _, _, ppid, ok := procStat(pid)
		if !ok || ppid <= 0 {
			continue
		}
		children[ppid] = append(children[ppid], pid)
	}
	out := make([]int, 0, 8)
	var walk func(int)
	walk = func(pid int) {
		out = append(out, pid)
		for _, child := range children[pid] {
			if child != pid {
				walk(child)
			}
		}
	}
	walk(root)
	return out
}

func pssBytes(pid int) uint64 {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/smaps_rollup")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(line, "Pss:") {
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

func rssBytes(pid int) uint64 {
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

func procTimes(pid int) (utime, stime, cutime, cstime int64, ok bool) {
	utime, stime, cutime, cstime, _, ok = procStat(pid)
	return utime, stime, cutime, cstime, ok
}

// procStat parses /proc/pid/stat. comm is wrapped in parentheses and may
// contain spaces, so the numeric fields start after the last ')'.
func procStat(pid int) (utime, stime, cutime, cstime int64, ppid int, ok bool) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0, 0, 0, 0, 0, false
	}
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	if i < 0 || i+2 >= len(s) {
		return 0, 0, 0, 0, 0, false
	}
	fields := strings.Fields(s[i+2:])
	// After comm: state, ppid, pgrp, session, tty, tpgid, flags, minflt,
	// cminflt, majflt, cmajflt, utime, stime, cutime, cstime.
	if len(fields) < 15 {
		return 0, 0, 0, 0, 0, false
	}
	ppid, err = strconv.Atoi(fields[1])
	if err != nil {
		return 0, 0, 0, 0, 0, false
	}
	vals := make([]int64, 4)
	for n, idx := range []int{11, 12, 13, 14} {
		vals[n], err = strconv.ParseInt(fields[idx], 10, 64)
		if err != nil {
			return 0, 0, 0, 0, 0, false
		}
	}
	return vals[0], vals[1], vals[2], vals[3], ppid, true
}
