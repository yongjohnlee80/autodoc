package app

import (
	"bytes"
	"context"
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
}
