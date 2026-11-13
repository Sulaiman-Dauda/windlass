package selfstat

import (
	"os"
	"strconv"
	"strings"
)

// RSS reports the process's resident memory from /proc/self/statm, whose
// second field is resident pages.
func RSS() (uint64, bool) {
	data, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return 0, false
	}
	fields := strings.Fields(string(data))
	if len(fields) < 2 {
		return 0, false
	}
	pages, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return 0, false
	}
	return pages * uint64(os.Getpagesize()), true //nolint:gosec // G115: a page size is positive
}
