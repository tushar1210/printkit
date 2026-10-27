//go:build linux

package main

import (
	"os"
	"testing"
	"time"
)

func TestProcStatSelf(t *testing.T) {
	_, _, _, _, ppid, ok := procStat(os.Getpid())
	if !ok {
		t.Fatal("procStat")
	}
	if ppid != os.Getppid() {
		t.Fatalf("ppid %d, want %d", ppid, os.Getppid())
	}
	if treeRSS(os.Getpid()) == 0 {
		t.Fatal("treeRSS")
	}
	before := treeCPU(os.Getpid())
	acc := 0
	deadline := time.Now().Add(400 * time.Millisecond)
	for time.Now().Before(deadline) {
		for i := 0; i < 100_000; i++ {
			acc += i
		}
	}
	if acc == 0 {
		t.Fatal("loop")
	}
	after := treeCPU(os.Getpid())
	if after <= before {
		t.Fatalf("cpu %s -> %s", before, after)
	}
}
