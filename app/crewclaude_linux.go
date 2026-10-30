//go:build linux

package app

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// parentOf is pid's parent, field 4 of /proc/<pid>/stat (read after the last ')': the command name
// may hold spaces and parentheses).
func parentOf(pid int) (int, error) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0, err
	}
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return 0, fmt.Errorf("/proc/%d/stat has no command", pid)
	}
	fields := strings.Fields(s[i+1:]) // field 3 (state) onwards
	if len(fields) < 2 {
		return 0, fmt.Errorf("/proc/%d/stat is short", pid)
	}
	return strconv.Atoi(fields[1])
}
