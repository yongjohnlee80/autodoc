package crew

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
)

// The endpoint kinds and their locators (§4.7). A locator names only the CLI's own channel on
// this machine: a socket under the CLI's own directory for this user, or a loopback address. The
// daemon never connects to an arbitrary host, and a locator never carries a secret.
type (
	// claudeLocator is a Claude Code session's inbox socket and the process holding it: Start is
	// that process's reference (ProcRef), so a pid a later process reuses is never taken for it.
	claudeLocator struct {
		Socket  string `json:"socket"`
		PID     int    `json:"pid"`
		Start   string `json:"start"`
		Session string `json:"session"`
	}
	codexLocator struct {
		Server string `json:"server"`
		Thread string `json:"thread"`
	}
	agyLocator struct {
		Address      string `json:"address"`
		Conversation string `json:"conversation"`
	}
	opencodeLocator struct {
		Session string `json:"session"`
		PID     int    `json:"pid"`
		Start   string `json:"start"`
	}
)

// strictJSON decodes s into v, refusing unknown fields and trailing data.
func strictJSON(s string, v any) error {
	d := json.NewDecoder(strings.NewReader(s))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	if err := d.Decode(new(json.RawMessage)); err != io.EOF {
		return fmt.Errorf("trailing data after the locator")
	}
	return nil
}

// under reports whether path, cleaned and absolute, lies inside one of roots.
func under(path string, roots ...string) bool {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return false
	}
	for _, r := range roots {
		if strings.HasPrefix(path, r+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// ClaudeSocketDirs are the folders Claude Code binds its sessions' inbox sockets in, for user uid.
func ClaudeSocketDirs(uid int) []string {
	u := strconv.Itoa(uid)
	return []string{"/run/user/" + u + "/cc-socks", "/tmp/cc-socks-" + u}
}

// CodexSocketDirs are the folders Codex's app-server binds its sockets in, for user uid.
func CodexSocketDirs(uid int) []string { return []string{"/tmp/codex-daemon-" + strconv.Itoa(uid)} }

// threadOK reports whether t can name a Codex session as one argv value: a waker passes it as
// --thread=<t>, and a leading '-' or a control character has no place in a session id or name.
func threadOK(t string) bool {
	if t == "" || len(t) > MaxIdent || strings.HasPrefix(t, "-") {
		return false
	}
	for _, r := range t {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// checkLocator validates a locator of kind for the daemon's user uid, in its canonical JSON.
func checkLocator(kind, locator string, uid int) (string, error) {
	bad := func(why string) (string, error) {
		return "", fmt.Errorf("%w: %s locator: %s", ErrBadEndpoint, kind, why)
	}
	var v any
	switch kind {
	case "claude":
		var l claudeLocator
		if err := strictJSON(locator, &l); err != nil {
			return bad(err.Error())
		}
		if !under(l.Socket, ClaudeSocketDirs(uid)...) {
			return bad("the socket is not under /run/user/<uid>/cc-socks/ or /tmp/cc-socks-<uid>/")
		}
		if l.PID <= 0 || l.Start == "" || l.Session == "" {
			return bad("pid, start and session are required")
		}
		if p, ok := parseProc(l.Start); !ok || p.pid != l.PID {
			return bad("start is not the pid's process reference (boot:ns:pid:start)")
		}
		v = l
	case "codex":
		var l codexLocator
		if err := strictJSON(locator, &l); err != nil {
			return bad(err.Error())
		}
		if !under(l.Server, CodexSocketDirs(uid)...) {
			return bad("the app-server socket is not under /tmp/codex-daemon-<uid>/")
		}
		if !threadOK(l.Thread) {
			return bad("thread is not a session id or name: required, at most 256 bytes, no leading '-', no control characters")
		}
		v = l
	case "agy":
		var l agyLocator
		if err := strictJSON(locator, &l); err != nil {
			return bad(err.Error())
		}
		host, port, err := net.SplitHostPort(l.Address)
		if err != nil || port == "" {
			return bad("address is not host:port")
		}
		if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
			return bad("the address is not loopback")
		}
		if l.Conversation == "" {
			return bad("conversation is required")
		}
		v = l
	case "opencode":
		var l opencodeLocator
		if err := strictJSON(locator, &l); err != nil {
			return bad(err.Error())
		}
		if l.Session == "" || l.PID <= 0 || l.Start == "" {
			return bad("session, pid and start are required")
		}
		v = l
	default:
		return "", fmt.Errorf("%w: unknown kind %q", ErrBadEndpoint, kind)
	}
	b, err := json.Marshal(v)
	return string(b), err
}

// SetEndpoint records how to wake the calling session (crew.endpoint, §4.7). One write: the
// credential is authenticated; the kind must be the session's profile's; the locator is checked for
// this user. A new endpoint raises the generation and restarts the session's wake row with no
// deadline and no backoff, so pending mail is reservable at once and its first failure retries in
// 5 s. It answers the generation.
func (m *Manager) SetEndpoint(ctx context.Context, credential, kind, locator string) (int64, error) {
	canon, err := checkLocator(kind, locator, m.uid)
	if err != nil {
		return 0, err
	}
	var gen int64
	err = m.store.Write(ctx, func(tx Tx) error {
		s, err := m.Authenticate(tx, credential)
		if err != nil {
			return err
		}
		p, ok, err := tx.Profile(s.Profile)
		if err != nil {
			return err
		}
		if !ok || p.Kind != kind {
			return fmt.Errorf("%w: a %s endpoint for a %s session", ErrBadEndpoint, kind, p.Kind)
		}
		e, _, err := tx.Endpoint(s.ID)
		if err != nil {
			return err
		}
		gen = e.Generation + 1
		if err := tx.PutEndpoint(Endpoint{Session: s.ID, Generation: gen, Kind: kind, Locator: canon,
			Registered: tx.Now().UnixNano()}); err != nil {
			return err
		}
		w, ok, err := tx.Wake(s.ID)
		if err != nil {
			return err
		}
		if !ok {
			w = Wake{Session: s.ID}
		}
		w.State, w.Token, w.NextAt, w.Failures = WakeIdle, "", nil, 0
		return tx.PutWake(w)
	})
	return gen, err
}
