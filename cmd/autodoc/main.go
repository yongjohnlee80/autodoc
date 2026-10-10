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

func init() {
	if info, ok := debug.ReadBuildInfo(); ok {
		version = moduleVersion(version, info)
	}
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
	exit(app.Main(context.Background(), os.Args[1:], app.Options{Version: version}))
}
