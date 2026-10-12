package app

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/yongjohnlee80/golib/gui/record"
)

// --debug[=path]: record the input and event stream — the window's platform
// events, the IME's state, the commands sent back, the frames' boundaries, and
// the TUI's own trace — to one JSONL file, in the order they happened. A
// recording is how an input or IME bug on one machine is replayed as a test on
// another (golib's gui replay harness); the file also contains everything typed,
// including passwords, so it is written only where this flag says.

// debugFlag is --debug[=path]: given without a value it means the default path,
// and the flag package delivers that as Set("true").
type debugFlag struct {
	given bool
	path  string
}

func (f *debugFlag) String() string { return f.path }

// IsBoolFlag makes --debug take an optional value: --debug is the default path,
// --debug=PATH is that path.
func (f *debugFlag) IsBoolFlag() bool { return true }

func (f *debugFlag) Set(s string) error {
	f.given = true
	if s != "true" {
		f.path = s
	}
	return nil
}

// defaultDebugPath is the recording's name when --debug names none: the day and
// time it was made, in the working directory.
func defaultDebugPath() string {
	return filepath.Join(".", fmt.Sprintf("autodoc-debug-%s.jsonl", time.Now().Format("20060102-150405")))
}

// openDebugRecording opens the file the recording goes to and says where, on
// stderr: the one line a --debug run starts with.
func openDebugRecording(path string) (*os.File, *record.JSONLRecorder, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot open the debug recording %s: %w", path, err)
	}
	fmt.Fprintf(os.Stderr, "autodoc: recording the input and event stream to %s (the file contains everything typed)\n", path)
	return f, record.NewJSONLRecorder(f), nil
}
