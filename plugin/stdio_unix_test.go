//go:build unix

package plugin

import (
	"os"
	"strings"
	"testing"
	"time"
)

// TestTheLinksPipesAreItsOwn: stdioOf duplicates the descriptors, so closing the originals — as
// the finalizer of a lost os.Stdout did — leaves the link able to write; a terminal is refused; a
// descriptor that cannot be duplicated is an error, with nothing left open.
func TestTheLinksPipesAreItsOwn(t *testing.T) {
	inR, inW, _ := os.Pipe()
	outR, outW, _ := os.Pipe()
	defer inW.Close()
	defer outR.Close()
	conn, err := stdioOf(inR, int(inR.Fd()), int(outW.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	inR.Close() // the originals go: the link's own descriptors stay
	outW.Close()
	if err := conn.SetWriteDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("the link's pipe takes no deadline: %v", err)
	}
	if _, err := conn.Write([]byte("frame")); err != nil {
		t.Fatalf("a write after the originals closed: %v", err)
	}
	buf := make([]byte, 5)
	if _, err := outR.Read(buf); err != nil || string(buf) != "frame" {
		t.Fatalf("read %q, %v", buf, err)
	}

	tty, err := os.Open("/dev/null") // a character device, as a terminal is
	if err != nil {
		t.Skip("no /dev/null")
	}
	defer tty.Close()
	if _, err := stdioOf(tty, 0, 1); err == nil || !strings.Contains(err.Error(), "a terminal") {
		t.Errorf("a terminal's stdin: %v", err)
	}

	p, q, _ := os.Pipe()
	defer p.Close()
	defer q.Close()
	if _, err := stdioOf(p, -1, int(q.Fd())); err == nil {
		t.Error("an in descriptor that cannot be duplicated was taken")
	}
	if _, err := stdioOf(p, int(p.Fd()), -1); err == nil {
		t.Error("an out descriptor that cannot be duplicated was taken")
	}
}
