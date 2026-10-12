package app

import (
	"errors"
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

// openDebugRecording opens the file the recording goes to, says where on stderr,
// and returns the recorder that owns it. The file is created private — it holds
// everything typed — and a path that already exists, a file or a symlink, is
// refused: never truncated, never followed.
func openDebugRecording(path string) (*os.File, *record.JSONLRecorder, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, nil, fmt.Errorf("the debug recording %s already exists: choose a new one, or omit =PATH for the timestamped default", path)
		}
		return nil, nil, fmt.Errorf("cannot open the debug recording %s: %w", path, err)
	}
	fmt.Fprintf(os.Stderr, "autodoc: recording the input and event stream to %s (the file contains everything typed)\n", path)
	return f, record.NewJSONLRecorder(f), nil
}

// debugRecording is one --debug run's recording: the file it lands in and the
// recorder that owns it. The owner closes it once, after the UI that produced
// the recording has exited — in the terminal mode when Launch returns, in the
// window mode inside gui.Main's callback, the last code that runs before the
// process ends.
type debugRecording struct {
	f   *os.File
	rec *record.JSONLRecorder
}

// close finishes the recording once the UI that produced it has exited: flush
// the owner, then close the file. A recording that could not be written whole
// is the command's failure, never a silent one.
func (d *debugRecording) close() error {
	if d == nil {
		return nil
	}
	var err error
	if cerr := d.rec.Close(); cerr != nil {
		err = fmt.Errorf("debug recording incomplete: %w", cerr)
	}
	if ferr := d.f.Close(); err == nil && ferr != nil {
		err = fmt.Errorf("debug recording incomplete: %w", ferr)
	}
	return err
}

// nonClosingRecorder hands the window's backend the recording without the close
// that comes with it: the backend closes what it is given when the window ends,
// but the TUI's trace is still recording then — two producers, one owner, and
// only the owner closes.
type nonClosingRecorder struct{ rec record.Recorder }

// Record forwards to the owner's recorder; there is no Close for the backend to
// find.
func (n nonClosingRecorder) Record(rec record.Record) { n.rec.Record(rec) }
