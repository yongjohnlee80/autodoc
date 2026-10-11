// Command autodoc is AutoDoc's one binary, the community build: app.Main with no registrations
// (ADR 0216 §1.1). The modes and their flags are app's.
package main

import (
	"context"
	"os"
	"runtime/debug"

	"github.com/yongjohnlee80/autodoc/app"
)

// version is stamped at build time (-ldflags "-X main.version=…"). Unstamped, as
// `go install github.com/yongjohnlee80/autodoc/cmd/autodoc@v0.1.0` builds it, it is the module's
// version the build recorded. The TUI compares it with the installed binary's, so it must be real.
var version = "dev"

// buildDate is the day the build was made, stamped at build time (-ldflags "-X
// main.buildDate=2006-01-02"). Unstamped, it is the day of the commit the build recorded (vcs.time),
// or "" when it recorded none: Help › About then shows no date.
var buildDate = ""

func init() {
	if info, ok := debug.ReadBuildInfo(); ok {
		version = moduleVersion(version, info)
		buildDate = recordedDate(buildDate, info)
	}
}

// recordedDate is the stamped date, unless the build stamped none and recorded its commit's time.
func recordedDate(stamped string, info *debug.BuildInfo) string {
	if stamped != "" {
		return stamped
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.time" && len(s.Value) >= len("2006-01-02") {
			return s.Value[:len("2006-01-02")]
		}
	}
	return ""
}

// moduleVersion is stamped, unless the build stamped nothing and recorded the module's version.
func moduleVersion(stamped string, info *debug.BuildInfo) string {
	if v := info.Main.Version; stamped == "dev" && v != "" && v != "(devel)" {
		return v
	}
	return stamped
}

// exit is os.Exit; a test sees main's exit code through it.
var exit = os.Exit

func main() {
	exit(app.Main(context.Background(), os.Args[1:], app.Options{Version: version, BuildDate: buildDate}))
}
