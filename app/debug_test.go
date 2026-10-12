package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/gui/record"
)

// --debug is a companion of the UI modes: without --ui or --gui it is refused, whichever
// mode (or none) it stands beside.
func TestTheDebugFlagIsForTheUIModes(t *testing.T) {
	for _, c := range []struct {
		args []string
		says string
	}{
		{args: []string{"--debug"}, says: "--debug is for --ui and --gui"},
		{args: []string{"--debug", "--serve"}, says: "--debug is for --ui and --gui"},
		{args: []string{"--call", "doc.read", "--debug"}, says: "--debug is for --ui and --gui"},
		{args: []string{"--version", "--debug"}, says: "--debug is for --ui and --gui"},
		{args: []string{"--compact", "--debug"}, says: "--debug is for --ui and --gui"},
	} {
		var stderr bytes.Buffer
		if code := run(context.Background(), c.args, Options{Version: "v-test"}, io.Discard, &stderr); code != 2 {
			t.Errorf("%v: exit %d; want 2", c.args, code)
		}
		if !strings.Contains(stderr.String(), c.says) {
			t.Errorf("%v: said %q; want it to say %q", c.args, stderr.String(), c.says)
		}
	}
}

// --debug takes an optional value: --debug alone is the default path, --debug=PATH is that
// path, and the default names the day and time of the recording.
func TestTheDebugFlagTakesAnOptionalValue(t *testing.T) {
	bare := &debugFlag{}
	if err := bare.Set("true"); err != nil { // --debug alone arrives as "true"
		t.Fatal(err)
	}
	if !bare.given || bare.path != "" {
		t.Fatalf("--debug alone: given %v, path %q; want the default path", bare.given, bare.path)
	}
	named := &debugFlag{}
	if err := named.Set("trace.jsonl"); err != nil {
		t.Fatal(err)
	}
	if !named.given || named.path != "trace.jsonl" {
		t.Fatalf("--debug=trace.jsonl: given %v, path %q", named.given, named.path)
	}
	if got := defaultDebugPath(); !regexp.MustCompile(`^autodoc-debug-\d{8}-\d{6}\.jsonl$`).MatchString(strings.TrimPrefix(got, "./")) {
		t.Fatalf("the default recording path is %q; want autodoc-debug-<YYYYMMDD-HHMMSS>.jsonl", got)
	}
}

// openDebugRecording says where the recording goes, on stderr, and what it says lands in
// the file.
func TestOpenDebugRecordingNamesTheFile(t *testing.T) {
	dir, err := os.MkdirTemp("", "addbg")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "rec.jsonl")
	pipeR, pipeW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr := os.Stderr
	os.Stderr = pipeW
	f, rec, err := openDebugRecording(path)
	os.Stderr = stderr
	if err != nil {
		t.Fatal(err)
	}
	pipeW.Close()
	said, _ := io.ReadAll(pipeR)
	if !strings.Contains(string(said), path) || !strings.Contains(string(said), "contains everything typed") {
		t.Fatalf("the startup notice said %q; want it to name %s and the privacy caveat", string(said), path)
	}
	rec.Record(record.Record{Stage: record.StageTui, Kind: "mount", Data: map[string]any{"comp": "*widget.Editor"}})
	if err := rec.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"Kind":"mount"`) {
		t.Fatalf("the recording holds %q; want the recorded line", string(data))
	}
	// the file holds everything typed: it is created private
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("the recording's mode is %v; want 0600, private", st.Mode().Perm())
	}
}

// An existing path is refused, never truncated — and a symlink is refused, never
// followed: a recording holds everything typed.
func TestOpenDebugRecordingRefusesWhatExists(t *testing.T) {
	dir, err := os.MkdirTemp("", "addbg")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	existing := filepath.Join(dir, "rec.jsonl")
	if err := os.WriteFile(existing, []byte("kept\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := openDebugRecording(existing); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("an existing file: err = %v; want the refusal", err)
	}
	if data, err := os.ReadFile(existing); err != nil || string(data) != "kept\n" {
		t.Fatalf("the existing file was touched: %q, %v", string(data), err)
	}
	link := filepath.Join(dir, "link.jsonl")
	if err := os.Symlink(existing, link); err != nil {
		t.Fatal(err)
	}
	if _, _, err := openDebugRecording(link); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("a symlink: err = %v; want the refusal", err)
	}
	if data, err := os.ReadFile(existing); err != nil || string(data) != "kept\n" {
		t.Fatalf("the symlink's target was touched: %q, %v", string(data), err)
	}
}

// The window's backend must not close the shared recording: the TUI's trace is
// still recording when the backend's teardown runs, and a send on a closed
// channel panics. The wrapper carries the records and no close; the owner closes
// after them all.
func TestTheBackendCannotCloseTheSharedRecording(t *testing.T) {
	var buf bytes.Buffer
	owner := record.NewJSONLRecorder(&buf)
	if _, ok := any(owner).(io.Closer); !ok {
		t.Fatal("the owner must be closeable")
	}
	wrapped := nonClosingRecorder{rec: owner}
	if _, ok := any(wrapped).(io.Closer); ok {
		t.Fatal("the wrapper must not be an io.Closer: the backend would close the shared recording mid-trace")
	}
	// The backend's teardown would close here (with the owner wired, as r0 did);
	// with the wrapper it cannot, and the TUI's teardown records still land.
	wrapped.Record(record.Record{Stage: record.StageTui, Kind: "unmount", Data: map[string]any{"comp": "*widget.Editor"}})
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	var r record.Record
	if err := json.Unmarshal(buf.Bytes(), &r); err != nil || r.Kind != "unmount" {
		t.Fatalf("the recording holds %q (%v); want the teardown record", buf.String(), err)
	}
}

// failingWriter fails every write, as a full disk does.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("no space left on device") }

// A recording that could not be written whole fails the command: the flush
// error is reported, never swallowed.
func TestAnIncompleteRecordingFailsTheCommand(t *testing.T) {
	f, err := os.CreateTemp("", "addbg")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(f.Name()) })
	dbg := &debugRecording{f: f, rec: record.NewJSONLRecorder(failingWriter{})}
	dbg.rec.Record(record.Record{Stage: record.StageTui, Kind: "key"})
	if err := dbg.close(); err == nil || !strings.Contains(err.Error(), "debug recording incomplete") || !strings.Contains(err.Error(), "no space left") {
		t.Fatalf("close = %v; want it to say the recording is incomplete", err)
	}
	if err := (*debugRecording)(nil).close(); err != nil {
		t.Fatalf("a run without --debug closes nothing: %v", err)
	}
}
