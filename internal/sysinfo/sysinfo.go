// Package sysinfo reports resource usage of this app for the status bar.
package sysinfo

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

type Stats struct {
	RSSMB      float64 `json:"rssMB"`  // resident memory of this process and its children (WebKit web/network processes)
	HeapMB     float64 `json:"heapMB"` // Go heap in use
	Goroutines int     `json:"goroutines"`
	Processes  int     `json:"processes"`
}

func Collect() Stats {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	s := Stats{HeapMB: float64(m.HeapAlloc) / 1048576, Goroutines: runtime.NumGoroutine()}
	page := float64(os.Getpagesize())
	self := os.Getpid()
	parents := map[int]int{}
	rss := map[int]float64{}
	entries, _ := os.ReadDir("/proc")
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		stat, err := os.ReadFile(filepath.Join("/proc", e.Name(), "stat"))
		if err != nil {
			continue
		}
		// "pid (comm) state ppid ..."; comm may contain spaces, so split after the last ')'
		i := strings.LastIndexByte(string(stat), ')')
		if i < 0 {
			continue
		}
		f := strings.Fields(string(stat[i+2:])) // f[0]=state f[1]=ppid ... f[21]=rss (pages)
		if len(f) < 22 {
			continue
		}
		ppid, _ := strconv.Atoi(f[1])
		pages, _ := strconv.ParseFloat(f[21], 64)
		parents[pid] = ppid
		rss[pid] = pages * page
	}
	isOurs := func(pid int) bool {
		for d := 0; pid > 1 && d < 16; d++ {
			if pid == self {
				return true
			}
			pid = parents[pid]
		}
		return false
	}
	for pid, r := range rss {
		if isOurs(pid) {
			s.RSSMB += r / 1048576
			s.Processes++
		}
	}
	if s.Processes == 0 { // no /proc (Windows, macOS): report what the Go runtime holds
		s.RSSMB = float64(m.Sys) / 1048576
		s.Processes = 1
	}
	return s
}
