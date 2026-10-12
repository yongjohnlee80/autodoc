package tui

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// THE PLUGIN LOG — what a plugin writes to stderr, kept in a file of its own (plugins.go).

// pluginLog is a plugin's stderr, kept in <LogDir>/<name>.log (truncated at each start, capped at
// pluginLogCap), with its last line for the toast of a failure. No LogDir keeps the last line only.
type pluginLog struct {
	mu       sync.Mutex
	f        *os.File
	written  int
	lastLine string
	partial  []byte
}

func openPluginLog(dir, name string) *pluginLog {
	l := &pluginLog{}
	if dir != "" {
		if err := os.MkdirAll(dir, 0o700); err == nil {
			l.f, _ = os.OpenFile(filepath.Join(dir, name+".log"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
		}
	}
	return l
}

func (l *pluginLog) Write(b []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f != nil && l.written < pluginLogCap {
		n, _ := l.f.Write(b[:min(len(b), pluginLogCap-l.written)])
		l.written += n
	}
	l.partial = append(l.partial, b...)
	for {
		i := strings.IndexByte(string(l.partial), '\n')
		if i < 0 {
			break
		}
		if line := strings.TrimSpace(string(l.partial[:i])); line != "" {
			l.lastLine = line
		}
		l.partial = l.partial[i+1:]
	}
	if len(l.partial) > 4096 {
		l.partial = l.partial[len(l.partial)-4096:]
	}
	return len(b), nil
}

// note writes the host's own line into the plugin's log; the last line stays the plugin's.
func (l *pluginLog) note(s string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f != nil && l.written < pluginLogCap {
		n, _ := io.WriteString(l.f, "autodoc: "+s+"\n")
		l.written += n
	}
}

// last is the last line the plugin wrote (or an unfinished one), "" for none.
func (l *pluginLog) last() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if p := strings.TrimSpace(string(l.partial)); p != "" {
		return p
	}
	return l.lastLine
}

func (l *pluginLog) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return nil
	}
	err := l.f.Close()
	l.f = nil
	return err
}
