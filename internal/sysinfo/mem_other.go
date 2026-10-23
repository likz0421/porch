//go:build !windows

package sysinfo

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

// readMemInfo 在类 Unix 上读取 /proc/meminfo
func readMemInfo() (usedMB, totalMB uint64) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	var total, available uint64
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		v, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		switch fields[0] {
		case "MemTotal:":
			total = v / 1024
		case "MemAvailable:":
			available = v / 1024
		}
	}
	if total == 0 {
		return 0, 0
	}
	return total - available, total
}
