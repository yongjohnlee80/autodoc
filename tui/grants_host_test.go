package tui

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/internal/ipc"
	"github.com/yongjohnlee80/autodoc/tui/feature"
	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
	"github.com/yongjohnlee80/golib/server/rpc/msgpackrpc"
	tuicore "github.com/yongjohnlee80/golib/tui"
)

// End to end: a capability an agent was granted, revoked from Agent permissions, is refused at the
// agent's next call and asked for again by its card, which names the agent as an agent; the
// preference no longer holds it, and Allow grants it again.
func TestARevokedCapabilityIsRefusedAndAskedForAgain(t *testing.T) {
	sockDir := t.TempDir()
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "kestrel\n"}})
	r := runTUI(t, NewSession(d.sock, nil), Options{EditorDir: sockDir, LuaDir: t.TempDir()})
	r.connected(t)
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	onLoop(r, func() bool {
		r.h.agents.agentEnv(feature.Workspace{Name: "kb"}, t.TempDir(), agentProfile{Codename: "zen"})
		return true
	})
	path := onLoop(r, r.h.editorSocketPath)
	ctx := context.Background()
	cli, err := golibrpc.Dial(ctx, path, msgpackrpc.New(nil), golibrpc.ClientNetwork(ipc.Network), golibrpc.WithConnDialer(ipc.RPCDialer))
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	token := onLoop(r, func() string { return r.h.agents.editorToken("zen") })
	read := func() *golibrpc.Error {
		_, err := cli.Call(ctx, "editor.read", token)
		var re *golibrpc.Error
		if errors.As(err, &re) {
			return re
		}
		if err != nil {
			t.Fatalf("editor.read: %v", err)
		}
		return nil
	}
	granted := func() bool { return onLoop(r, func() bool { return r.h.prefs.luaGrants["agent:zen"]["editor.read"] }) }

	if re := read(); re == nil || !strings.Contains(re.Message, "needs editor.read") {
		t.Fatalf("an ungranted read: %v", re)
	}
	r.s.WaitForText(t, "let the agent zen use editor.read?")
	r.s.WaitForText(t, "read your open documents")
	onLoop(r, func() bool { r.h.asker.Accepted(); r.h.closeDialog("confirm"); return true }) // as Allow does: the card closes
	r.s.WaitFor(t, "the read granted", func(string) bool { return granted() })
	if re := read(); re != nil {
		t.Fatalf("a granted read: %v", re)
	}

	onLoop(r, func() bool { r.h.runEntry("agents.permissions"); return true })
	r.s.WaitFor(t, "Agent permissions drawn", func(sc string) bool {
		return strings.Contains(sc, "Agent permissions") && strings.Contains(sc, "CAPABILITY") && strings.Contains(sc, "editor.read")
	})
	onLoop(r, func() bool {
		i := slices.IndexFunc(r.h.grantsView.rows.All(), func(g grantRow) bool { return g.holder == "agent:zen" && g.capability == "editor.read" })
		if i < 0 {
			t.Error("the dialog lists no editor.read of zen's")
		}
		r.h.grantsView.revoke(i)
		return true
	})
	r.s.WaitForText(t, "revoke editor.read from zen?")
	onLoop(r, func() bool { r.h.asker.Accepted(); r.h.closeDialog("confirm"); return true }) // as Allow does: the card closes
	r.s.WaitFor(t, "the read revoked", func(string) bool { return !granted() })
	r.s.WaitForText(t, "Nothing is granted.")

	if re := read(); re == nil || re.Code != codeEditorNotGranted || !strings.Contains(re.Message, "needs editor.read") {
		t.Fatalf("a read after the revoke: %v, want needs editor.read", re)
	}
	r.s.WaitForText(t, "let the agent zen use editor.read?")
	onLoop(r, func() bool { r.h.asker.Accepted(); r.h.closeDialog("confirm"); return true }) // as Allow does: the card closes
	r.s.WaitFor(t, "the read granted again", func(string) bool { return granted() })
	if re := read(); re != nil {
		t.Fatalf("a read granted again: %v", re)
	}
}

// From the keyboard, on screen: Agent permissions opens with the list holding the keys (Down moves
// its cursor, the detail following), and each button answers its letter with the list focused:
// r asks to revoke the row's capability, a to revoke everything its holder holds (Esc keeps
// both), and q closes the dialog.
func TestAgentPermissionsAnswersItsKeysFromTheList(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "kestrel\n"}})
	r := runTUI(t, NewSession(d.sock, nil), Options{LuaDir: t.TempDir()})
	r.connected(t)
	onLoop(r, func() bool {
		r.h.prefs.luaGrants = map[string]map[string]bool{"agent:zen": {"editor.read": true, "terminal": true}}
		r.h.runEntry("agents.permissions")
		return true
	})
	r.s.WaitFor(t, "Agent permissions drawn", func(sc string) bool {
		return strings.Contains(sc, "Agent permissions") && strings.Contains(sc, "zen (agent) holds editor.read")
	})
	r.s.WaitFor(t, "the list holding the keys", func(string) bool {
		return r.focused(grantsListID)
	})

	r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyDown})
	r.s.WaitForText(t, "zen (agent) holds terminal")

	r.keys(t, key('r'))
	r.s.WaitForText(t, "revoke terminal from zen?")
	r.keys(t, esc())
	r.s.WaitFor(t, "the revoke card gone", func(sc string) bool { return !strings.Contains(sc, "revoke terminal from zen?") })

	r.keys(t, key('a'))
	r.s.WaitForText(t, "revoke everything zen holds?")
	r.keys(t, esc())
	r.s.WaitFor(t, "the revoke-all card gone", func(sc string) bool { return !strings.Contains(sc, "revoke everything zen holds?") })
	if !onLoop(r, func() bool {
		return r.h.prefs.luaGrants["agent:zen"]["editor.read"] && r.h.prefs.luaGrants["agent:zen"]["terminal"]
	}) {
		t.Fatal("Esc on a revoke card took a grant back")
	}

	r.keys(t, key('q'))
	r.s.WaitFor(t, "the dialog closed", func(sc string) bool { return !strings.Contains(sc, "Agent permissions") })
}
