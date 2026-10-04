package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/yongjohnlee80/autodoc/core/schema"
	"github.com/yongjohnlee80/autodoc/rpc"
)

// schemaPoll is how often a workspace's schema file is checked for an edit. A schema may live
// outside the root (or be excluded from it), so the follower does not see it.
const schemaPoll = 2 * time.Second

// schemaHolder is one workspace's frontmatter schema (ADR 0212 §5): the stored path, the last valid
// schema read from it, and why the file as it is now is not one. It outlives the workspace's
// restarts (a pattern or provider change), so the last valid schema is kept across them.
type schemaHolder struct {
	root     string
	onChange func() // the active schema changed: revalidate the workspace's files
	// onFileChange is told the file changed on disk (a poll saw it), valid or not: an event no
	// client caused, which every client is told about
	onFileChange func()

	mu      sync.Mutex
	path    string // as stored: absolute, or under root; "" for none
	active  *schema.Schema
	fp      string // the active schema's fingerprint; "" for none
	loadErr error  // why the file is not a valid schema now; nil when it is
	stamp   string // the file's size and modification time at the last read
	cancel  func() // stops the poll
}

func newSchemaHolder(root string, onChange func()) *schemaHolder {
	return &schemaHolder{root: root, onChange: onChange}
}

// get is the active schema and its fingerprint, for the indexer and the search.
func (h *schemaHolder) get() (*schema.Schema, string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.active, h.fp
}

// status is the holder as workspace.list reports it.
func (h *schemaHolder) status() rpc.SchemaStatus {
	h.mu.Lock()
	defer h.mu.Unlock()
	st := rpc.SchemaStatus{Path: h.path, Active: h.active != nil}
	if h.active != nil {
		st.Fields = len(h.active.Fields)
	}
	if h.loadErr != nil {
		st.Err = h.loadErr.Error()
		var se *schema.Error
		if errors.As(h.loadErr, &se) {
			st.Err, st.Line = se.Msg, se.Line
		}
	}
	return st
}

func (h *schemaHolder) resolved() string {
	p := h.path
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, p[2:])
		}
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(h.root, p)
	}
	return filepath.Clean(p)
}

// setPath points the holder at a new file ("" for none) and reads it at once. A path with no
// valid schema yet still records it; the previous schema stays active until the file is valid,
// except that "" removes the schema.
func (h *schemaHolder) setPath(p string) {
	h.mu.Lock()
	h.path, h.stamp = p, ""
	if p == "" {
		changed := h.active != nil
		h.active, h.fp, h.loadErr = nil, "", nil
		h.mu.Unlock()
		if changed && h.onChange != nil {
			h.onChange()
		}
		return
	}
	h.mu.Unlock()
	h.reload(true)
}

// reload reads the file when it changed since the last read (always when force), and makes it the
// active schema when it is valid and different.
func (h *schemaHolder) reload(force bool) {
	h.mu.Lock()
	if h.path == "" {
		h.mu.Unlock()
		return
	}
	file := h.resolved()
	h.mu.Unlock()
	stamp, src, err := readSchema(file)
	h.mu.Lock()
	if !force && stamp == h.stamp {
		h.mu.Unlock()
		return
	}
	if !force && h.onFileChange != nil {
		defer h.onFileChange()
	}
	h.stamp = stamp
	var parsed *schema.Schema
	if err == nil {
		parsed, err = schema.Parse(src)
	}
	if err != nil {
		h.loadErr = err // the last valid schema stays active
		h.mu.Unlock()
		return
	}
	sum := sha256.Sum256(src)
	fp := hex.EncodeToString(sum[:6])
	changed := fp != h.fp
	h.active, h.fp, h.loadErr = parsed, fp, nil
	h.mu.Unlock()
	if changed && h.onChange != nil {
		h.onChange()
	}
}

// readSchema reads a schema file, bounded: its stamp (size and modification time, "" when it
// cannot be read) and its bytes.
func readSchema(file string) (string, []byte, error) {
	fi, err := os.Stat(file)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil, fmt.Errorf("no schema file at %s", file)
		}
		return "", nil, err
	}
	if !fi.Mode().IsRegular() {
		return "", nil, fmt.Errorf("%s is not a regular file", file)
	}
	stamp := fmt.Sprintf("%d:%d", fi.Size(), fi.ModTime().UnixNano())
	if fi.Size() > schema.MaxSize {
		return stamp, nil, schema.ErrTooLarge
	}
	f, err := os.Open(file)
	if err != nil {
		return stamp, nil, err
	}
	defer f.Close()
	src, err := io.ReadAll(io.LimitReader(f, schema.MaxSize+1))
	if err != nil {
		return stamp, nil, err
	}
	if len(src) > schema.MaxSize {
		return stamp, nil, schema.ErrTooLarge
	}
	return stamp, src, nil
}

// watch polls the file until ctx ends or stop is called.
func (h *schemaHolder) watch(ctx context.Context, every time.Duration) {
	ctx, cancel := context.WithCancel(ctx)
	h.mu.Lock()
	if h.cancel != nil {
		h.cancel()
	}
	h.cancel = cancel
	h.mu.Unlock()
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				h.reload(false)
			}
		}
	}()
}

func (h *schemaHolder) stop() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cancel != nil {
		h.cancel()
		h.cancel = nil
	}
}
