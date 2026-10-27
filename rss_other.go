//go:build !linux

package printkit

func rssForProfile(dir string) uint64 { return 0 }
