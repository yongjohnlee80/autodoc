package crew

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/yongjohnlee80/golib/errs"

	"github.com/yongjohnlee80/autodoc/internal/peercred"
)

// Crew Bridges (crew.bridges, crew.bridge_kill): the helper processes the crew runs on this machine,
// each joined to the session it serves, so a helper that outlived its owner is seen, reclaimed by the
// daemon once it has stayed orphaned (Reclaim), and killed by the user by hand
// (docs/decisions/crew.md#crew-bridges).

// The helper kinds (BridgeRow.Kind).
const (
	BridgeAgy         = "agy-bridge"          // autodoc crew bridge --agy --foreground --session <id>
	BridgeCodexPrompt = "codex-prompt-bridge" // autodoc crew bridge --codex … --foreground --session <id>
	BridgeCodexServer = "codex-server"        // codex app-server --listen unix://<agent dir>/codex.sock …
	BridgeCodexShared = "codex-shared-daemon" // Codex's own managed daemon: the crew never started it
)

// A row's state (BridgeRow.State).
const (
	BridgeLive    = "live"    // it serves a live session
	BridgeOrphan  = "orphan"  // it serves no one: BridgeRow.Why says why
	BridgeForeign = "foreign" // not proven this store's: never joined to an agent, never reclaimed
)

// Why a row is foreign (BridgeRow.Why).
const (
	ForeignShared = "Codex's shared daemon"
	// ForeignUnowned is a helper no session of this store is proven to own: another daemon's (another
	// store's), or one whose session this store doesn't know. A missing row is no proof of an orphan.
	ForeignUnowned = "not one of this store's sessions"
)

// Why a row is an orphan (BridgeRow.Why).
const (
	OrphanSessionEnded = "session ended"
	OrphanHostGone     = "host gone"
	OrphanNoEndpoint   = "no endpoint names its socket"
	OrphanAgyGone      = "language server gone"
)

// The outcomes of a kill (BridgeKilled.Outcome).
const (
	KilledTerm = "term" // it ended on SIGTERM
	KilledKill = "kill" // it ignored SIGTERM for bridgeKillWait and ended on SIGKILL
	KilledGone = "gone" // it had already ended, or the pid is another process now
	// KilledIncomplete is a kill some of whose processes still ran after SIGKILL and its wait:
	// BridgeKilled.Survivors says how many. Nothing is claimed ended that wasn't seen to end.
	KilledIncomplete = "incomplete"
)

// The crew's helper verbs' codes.
const (
	CodeBridgeConfirm int64 = -32097 // a kill of a live or foreign helper sent without the user's confirmation
	CodeNoBridges     int64 = -32098 // a daemon that can't read the machine's processes
)

// ErrBridgeConfirm is crew.bridge_kill of a live or foreign row without confirmed: the window asks
// the user first, naming the agent it would cut off.
var ErrBridgeConfirm = errs.Sentinel(errs.ErrPrecondition, "crew: killing this helper needs the user's confirmation")

// ErrNoBridges is crew.bridges where the daemon can't read the machine's processes (only Linux can
// yet).
var ErrNoBridges = errs.Sentinel(errs.ErrUnsupported, "crew: listing the crew's helper processes needs Linux")

// bridgeKillWait is how long a helper has to end on SIGTERM before SIGKILL: the daemon of the incident
// that started this ignored SIGTERM for ~15 s.
const bridgeKillWait = 5 * time.Second

// BridgeRow is one helper process (crew.bridges).
type BridgeRow struct {
	Kind    string   `json:"kind"`
	PID     int      `json:"pid"`
	Start   string   `json:"start"`   // its process reference (ProcRef): a kill names it, so a reused pid is never this row
	Started int64    `json:"started"` // when it started, Unix seconds
	Argv    []string `json:"argv"`
	Socket  string   `json:"socket,omitempty"`  // the socket it serves, "" for none
	Session string   `json:"session,omitempty"` // the crew session it serves, "" when none joins
	Owner   string   `json:"owner,omitempty"`   // codename@scope of that session's profile
	Host    string   `json:"host,omitempty"`    // that session's host (its scope)
	State   string   `json:"state"`             // live | orphan | foreign
	Why     string   `json:"why,omitempty"`     // an orphan's reason (Orphan*)
	// ReclaimAt is when the daemon reclaims this orphan if it stays one for the same reason, Unix
	// seconds; 0 until the daemon's own look has seen it orphaned, and for every other row.
	ReclaimAt int64 `json:"reclaim_at,omitempty"`
	// ReclaimStopped is an orphan whose reclaims left processes running reclaimRetries+1 times: the
	// daemon tries no more, and the user ends it (or not). Survivors is how many its last left.
	ReclaimStopped bool `json:"reclaim_stopped,omitempty"`
	Survivors      int  `json:"survivors,omitempty"`
}

// BridgeKilled is what crew.bridge_kill did.
type BridgeKilled struct {
	Outcome   string `json:"outcome"`             // term | kill | gone | incomplete
	Survivors int    `json:"survivors,omitempty"` // incomplete: how many of its processes still ran
}

// helperProc is one process found by the scan, before it is joined to the crew's rows.
type helperProc struct {
	pid, ppid, pgid int
	ref             string // ProcRef
	started         int64  // Unix seconds
	argv            []string
	kind            string
	session         string // a bridge's --session
	socket          string // a Codex server's --listen socket
	agyProc         string // an agy bridge's language server (AUTODOC_AGY_PROC), "" when unread
	context         string // a Codex server's session context file (AUTODOC_CREW_CONTEXT), "" when unread
	statStart       string // its start time as /proc/<pid>/stat gives it, as ref holds it
}

// helperOf is the helper argv is, by its shape alone: ok is false for any other process. A bridge
// is only its foreground process (the launcher without --foreground detaches it and ends).
func helperOf(argv []string) (kind, session, socket string, ok bool) {
	if len(argv) >= 2 && (base(argv[0]) == "node" || base(argv[0]) == "bun") && base(argv[1]) == "codex" {
		argv = argv[1:] // Codex's npm launcher runs the CLI under node
	}
	if len(argv) < 3 {
		return "", "", "", false
	}
	if argv[1] == "crew" && argv[2] == "bridge" {
		rest := argv[3:]
		session := flagValue(rest, "--session")
		if session == "" || !hasArg(rest, "--foreground") {
			return "", "", "", false
		}
		switch {
		case hasArg(rest, "--agy"):
			return BridgeAgy, session, "", true
		case hasArg(rest, "--codex"):
			return BridgeCodexPrompt, session, "", true
		}
		return "", "", "", false
	}
	if base(argv[0]) != "codex" || argv[1] != "app-server" {
		return "", "", "", false
	}
	rest := argv[2:]
	if hasArg(rest, "--managed-daemon") || rest[0] == "daemon" {
		return BridgeCodexShared, "", "", true
	}
	listen := flagValue(rest, "--listen")
	if path, ok := strings.CutPrefix(listen, "unix://"); ok && filepath.Base(path) == CodexServerSocket {
		return BridgeCodexServer, "", path, true
	}
	return "", "", "", false
}

func base(s string) string { return s[strings.LastIndexByte(s, '/')+1:] }

func hasArg(args []string, a string) bool {
	for _, x := range args {
		if x == a {
			return true
		}
	}
	return false
}

// flagValue is the value after flag in args ("--flag v" or "--flag=v"), "" when absent.
func flagValue(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
		if v, ok := strings.CutPrefix(a, flag+"="); ok {
			return v
		}
	}
	return ""
}

// Bridges is crew.bridges: every crew helper process of the daemon's user on this machine, joined to
// the sessions they serve, by kind, owner and pid.
func (m *Manager) Bridges(ctx context.Context) ([]BridgeRow, error) {
	procs, err := scanHelpers(m.uid)
	if err != nil {
		return nil, err
	}
	var rows []BridgeRow
	err = m.store.Read(ctx, func(tx Tx) error {
		rows, err = m.joinBridges(tx, procs)
		return err
	})
	if err != nil {
		return nil, err
	}
	m.bridgeMu.Lock()
	for i := range rows {
		if rows[i].State != BridgeOrphan {
			continue
		}
		k := reclaimKey(rows[i])
		rows[i].Survivors = m.bridgeLeft[k]
		if m.bridgeTries[k] > reclaimRetries {
			rows[i].ReclaimStopped = true // the daemon gave up on it: the user's to end
			continue
		}
		if first, ok := m.bridgeSeen[k]; ok {
			rows[i].ReclaimAt = first.Add(reclaimAfter).Unix()
		}
	}
	m.bridgeMu.Unlock()
	return rows, nil
}

// joinBridges joins each helper to the crew's rows, in one read.
func (m *Manager) joinBridges(tx Tx, procs []helperProc) ([]BridgeRow, error) {
	servers, err := liveCodexServers(tx)
	if err != nil {
		return nil, err
	}
	byPID := make(map[int]helperProc, len(procs))
	for _, p := range procs {
		byPID[p.pid] = p
	}
	now := tx.Now()
	var rows []BridgeRow
	for _, p := range procs {
		if parent, ok := byPID[p.ppid]; ok && parent.kind == p.kind && parent.socket == p.socket {
			continue // the CLI under its launcher: one helper, ended with its parent's subtree
		}
		r := BridgeRow{Kind: p.kind, PID: p.pid, Start: p.ref, Started: p.started, Argv: p.argv,
			Socket: p.socket, Session: p.session}
		if err := m.joinBridge(tx, &r, p, servers, now); err != nil {
			return nil, err
		}
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Owner != b.Owner {
			return a.Owner < b.Owner
		}
		return a.PID < b.PID
	})
	return rows, nil
}

// joinBridge sets r's owner, host and state from the session it serves.
func (m *Manager) joinBridge(tx Tx, r *BridgeRow, p helperProc, servers map[string]string, now time.Time) error {
	switch p.kind {
	case BridgeCodexShared:
		r.State, r.Why = BridgeForeign, ForeignShared
		return nil
	case BridgeCodexServer:
		r.Session = servers[socketKey(p.socket)]
		if r.Session == "" {
			// no live endpoint names it: an orphan only when its own session context proves it this
			// store's (a session of this store that moved on); else another daemon's, or unknown
			owner, err := m.serverOwner(tx, p)
			if err != nil {
				return err
			}
			if owner == "" {
				r.State, r.Why = BridgeForeign, ForeignUnowned
				return nil
			}
			r.Session = owner
			r.State, r.Why = BridgeOrphan, OrphanNoEndpoint
			return m.ownerOf(tx, r)
		}
	}
	s, ok, err := tx.Session(r.Session)
	if err != nil {
		return err
	}
	if !ok {
		r.State, r.Why = BridgeForeign, ForeignUnowned // another store's session, or one this store doesn't know
		return nil
	}
	if s.Ended != nil {
		r.State, r.Why = BridgeOrphan, OrphanSessionEnded
		return m.ownerOf(tx, r)
	}
	if prof, ok, err := tx.Profile(s.Profile); err != nil {
		return err
	} else if ok {
		r.Owner = prof.Codename + "@" + prof.Scope
	}
	r.Host = s.Host
	gone, err := m.hostGone(tx, s, now)
	switch {
	case err != nil:
		return err
	case gone:
		r.State, r.Why = BridgeOrphan, OrphanHostGone
	case p.kind == BridgeAgy && p.agyProc != "" && procDead(p.agyProc):
		r.State, r.Why = BridgeOrphan, OrphanAgyGone
	default:
		r.State = BridgeLive
	}
	return nil
}

// codexServerOf is a Codex endpoint's own app-server: its socket (socketKey) and the process
// listening there now (ProcRef), "" for another kind or a locator naming no server. The process is
// the one the kernel says holds the socket (its peer credentials on a connection to it), and only
// when the helper scan knows that very process (its pid and reference) as a Codex app-server naming
// this socket, and a second connection finds the same listener. Any doubt (no listener, an
// unreadable peer, a process the scan doesn't know, a listener that changed) records no process,
// and so proves nothing: never the first process whose command line merely names the path.
func (m *Manager) codexServerOf(ctx context.Context, kind, locator string) (string, string) {
	if kind != "codex" {
		return "", ""
	}
	var l codexLocator
	if json.Unmarshal([]byte(locator), &l) != nil || l.Server == "" {
		return "", ""
	}
	server := socketKey(l.Server)
	pid, ok := listenerPID(ctx, l.Server)
	if !ok {
		return server, ""
	}
	ref, err := ProcRef(pid)
	if err != nil || ref == "" {
		return server, ""
	}
	procs, err := scanHelpers(m.uid)
	if err != nil {
		return server, ""
	}
	known := false
	for _, p := range procs {
		if p.pid == pid && p.ref == ref && p.kind == BridgeCodexServer && socketKey(p.socket) == server {
			known = true
		}
	}
	if again, ok := listenerPID(ctx, l.Server); !known || !ok || again != pid {
		return server, ""
	}
	return server, ref
}

// listenerPID is the process holding the unix socket at path, from the kernel's record of the peer
// of a connection to it (peercred.PeerPID), bounded by listenerWait; false when nothing answers.
func listenerPID(ctx context.Context, path string) (int, bool) {
	ctx, cancel := context.WithTimeout(ctx, listenerWait)
	defer cancel()
	c, err := peercred.Dial(ctx, "unix", path)
	if err != nil {
		return 0, false
	}
	defer func() { _ = c.Close() }()
	pid, err := peercred.PeerPID(c)
	return pid, err == nil && pid > 0
}

// listenerWait bounds each connection codexServerOf makes to a Codex server's socket.
const listenerWait = 2 * time.Second

// serverOwner is the session of this store that a Codex server is proven to serve, "" when none
// is: first a session, live or ended, whose endpoint recorded this socket AND this very process
// (its ProcRef: a socket path alone could be a later server's); else the session its own session
// context names, while that file lasts: this store's, and one this store holds.
func (m *Manager) serverOwner(tx Tx, p helperProc) (string, error) {
	if p.ref != "" {
		recorded, err := tx.SessionsByServer(socketKey(p.socket))
		if err != nil {
			return "", err
		}
		for _, s := range recorded {
			if s.ServerRef == p.ref {
				return s.ID, nil
			}
		}
	}
	if p.context == "" {
		return "", nil
	}
	c, ok := readHelperContext(p.context)
	if !ok || c.Session == "" || (m.storeID != "" && c.Store != m.storeID) {
		return "", nil
	}
	if _, ok, err := tx.Session(c.Session); err != nil || !ok {
		return "", err
	}
	return c.Session, nil
}

// ownerOf names r's owner, codename@scope, from its session's profile.
func (m *Manager) ownerOf(tx Tx, r *BridgeRow) error {
	s, ok, err := tx.Session(r.Session)
	if err != nil || !ok {
		return err
	}
	prof, ok, err := tx.Profile(s.Profile)
	if err != nil {
		return err
	}
	if ok {
		r.Owner = prof.Codename + "@" + prof.Scope
	}
	r.Host = s.Host
	return nil
}

// liveCodexServers maps each live Codex session's app-server socket (socketKey) to its session.
func liveCodexServers(tx Tx) (map[string]string, error) {
	out := map[string]string{}
	profiles, err := tx.ActiveProfiles()
	if err != nil {
		return nil, err
	}
	for _, prof := range profiles {
		s, live, err := tx.LiveSession(prof.ID)
		if err != nil {
			return nil, err
		}
		if !live {
			continue
		}
		e, ok, err := tx.Endpoint(s.ID)
		if err != nil {
			return nil, err
		}
		if !ok || e.Kind != "codex" {
			continue
		}
		var l codexLocator
		if json.Unmarshal([]byte(e.Locator), &l) != nil || l.Server == "" {
			continue
		}
		out[socketKey(l.Server)] = s.ID
	}
	return out, nil
}

// socketKey is a socket path with its links resolved: the agent folder's codex.sock is a link to the
// socket Codex binds in its own folder, which the endpoint names.
func socketKey(path string) string {
	if r, err := filepath.EvalSymlinks(path); err == nil {
		return r
	}
	return filepath.Clean(path)
}

// BridgeKill is crew.bridge_kill: the host holding scope (run, epoch) ends helper pid, the process
// start names (its ProcRef). The helper is re-checked as it is signalled: one that ended, or a pid
// another process holds now, is "gone" and nothing is signalled. A live or foreign row needs
// confirmed (the user agreed, told whom it cuts off). An agent's credential never reaches this: it
// is a host verb.
func (m *Manager) BridgeKill(ctx context.Context, scope, run string, epoch int64, pid int, start string, confirmed bool) (BridgeKilled, error) {
	if pid <= 0 || start == "" {
		return BridgeKilled{}, fmt.Errorf("%w: pid and start name the helper", ErrInvalid)
	}
	if err := m.store.Read(ctx, func(tx Tx) error { return guardHost(tx, scope, run, epoch) }); err != nil {
		return BridgeKilled{}, err
	}
	return m.killHelper(ctx, pid, start, func(r BridgeRow) error {
		if (r.State == BridgeLive || r.State == BridgeForeign) && !confirmed {
			return ErrBridgeConfirm
		}
		return nil
	})
}

// killHelper opens helper pid (start its ProcRef), joins it again, lets allow refuse it, and ends it.
func (m *Manager) killHelper(ctx context.Context, pid int, start string, allow func(BridgeRow) error) (BridgeKilled, error) {
	h, p, ok, err := openHelper(pid, start)
	if err != nil {
		return BridgeKilled{}, err
	}
	if !ok {
		return BridgeKilled{Outcome: KilledGone}, nil
	}
	defer h.close()
	var rows []BridgeRow
	if err := m.store.Read(ctx, func(tx Tx) error {
		var err error
		rows, err = m.joinBridges(tx, []helperProc{p})
		return err
	}); err != nil {
		return BridgeKilled{}, err
	}
	if len(rows) != 1 {
		return BridgeKilled{Outcome: KilledGone}, nil
	}
	if err := allow(rows[0]); err != nil {
		return BridgeKilled{}, err
	}
	return h.end(bridgeKillWait)
}
