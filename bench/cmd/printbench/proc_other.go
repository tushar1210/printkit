//go:build !linux

package main

import (
	"os"
	"os/exec"
	"time"
)

func treeRSS(int) uint64 { return 0 }

func treeCPU(int) time.Duration { return 0 }

func processCPU(*os.ProcessState) time.Duration { return 0 }

func sampleTree(int, bool) (uint64, uint64, time.Duration) { return 0, 0, 0 }

func configureCmd(*exec.Cmd) {}
