package app

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/logger"
)

// The daemon's [log] level leaves out the records less severe than it, --log-level is taken over
// the key, and with neither every record is logged, as before. The record observed is the Warning
// a daemon with no workspace logs while it starts, before it answers; the banner, written with no
// logger, shows the output was captured either way.
func TestTheLogLevelFiltersTheDaemonsLog(t *testing.T) {
	const record = "no workspace yet"
	for _, c := range []struct {
		name   string
		key    string
		flag   logger.Severity
		logged bool
	}{
		{"no level", "", "", true},
		{"the key at the record's severity", "warning", "", true},
		{"the key above it, in any case", "ERROR", "", false},
		{"the flag over a key above it", "error", logger.SeverityWarning, true},
		{"the flag above the record, over a key below it", "debug", logger.SeverityCritical, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := short(t)
			sock := filepath.Join(dir, "a.sock")
			extra := ""
			if c.key != "" {
				extra = fmt.Sprintf("[log]\nlevel = %q\n", c.key)
			}
			d := startWith(t, writeConfig(t, sock, filepath.Join(dir, "state"), extra), sock, testBuild, c.flag)
			out := d.out.String()
			if !strings.Contains(out, "serving msgpack-RPC on") {
				t.Fatalf("no banner: the output was not captured:\n%s", out)
			}
			if got := strings.Contains(out, record); got != c.logged {
				t.Errorf("key %q, flag %q: the Warning logged %v, want %v:\n%s", c.key, c.flag, got, c.logged, out)
			}
		})
	}
}
