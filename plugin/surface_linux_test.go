package plugin

import (
	"os/exec"
	"strconv"
	"testing"

	"golang.org/x/sys/unix"
)

// The SDK makes its side channel close-on-exec as it opens it: a process the plugin starts doesn't
// inherit it. The positive control is the same fd, inherited before.
func TestAPluginsChildDoesNotInheritTheSideChannel(t *testing.T) {
	_, child, err := NewSurfacePair()
	if err != nil {
		t.Fatal(err)
	}
	defer child.Close()
	fd := int(child.Fd())
	probe := func() string {
		out, _ := exec.Command("/bin/sh", "-c", "test -e /proc/self/fd/"+strconv.Itoa(fd)+" && echo open || echo closed").Output()
		return string(out)
	}
	// fd 3 in a plugin arrives inheritable, as ExtraFiles hands it: the same state, here
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_SETFD, 0); err != nil {
		t.Fatal(err)
	}
	if got := probe(); got != "open\n" {
		t.Fatalf("the control: an inheritable fd is %q in a child", got)
	}
	t.Setenv(SurfaceFDEnv, strconv.Itoa(fd))
	conn, ok, err := OpenPluginSurfaceConn()
	if err != nil || !ok {
		t.Fatal(err)
	}
	_ = conn
	if got := probe(); got != "closed\n" {
		t.Errorf("a child of the plugin sees the side channel %q", got)
	}
}

// Consecutive moves coalesce to the latest and scrolls sum, while a press ends the run.
func TestSurfacePointerMovesCoalesceAndScrollsSum(t *testing.T) {
	q := newQueue()
	push := func(p SurfacePointer) { q.push(MethodSurfacePointer, SurfacePointerParams(p)) }
	push(SurfacePointer{Kind: "move", X: 1})
	push(SurfacePointer{Kind: "move", X: 2})
	push(SurfacePointer{Kind: "move", X: 3})
	push(SurfacePointer{Kind: "scroll", DY: 10})
	push(SurfacePointer{Kind: "scroll", DY: 5, DX: 1})
	push(SurfacePointer{Kind: "press", Button: "left", X: 3})
	push(SurfacePointer{Kind: "move", X: 4})
	var got []SurfacePointer
	q.end()
	for {
		n, ok := q.pop()
		if !ok {
			break
		}
		p, err := ReadSurfacePointer(n.params)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, p)
	}
	if len(got) != 4 || got[0].X != 3 || got[1].DY != 15 || got[1].DX != 1 || got[2].Kind != "press" || got[3].X != 4 {
		t.Errorf("queued %+v, want the last move, the summed scroll, the press, the move after it", got)
	}
}
