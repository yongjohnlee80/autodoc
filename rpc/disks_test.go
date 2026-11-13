package rpc

import (
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/vfs/local"

	"github.com/yongjohnlee80/autodoc/core/docs"
)

// localDisks are a test server's disks as the daemon's are: each volume's tree through vfs/local,
// opened when first asked for, closed with the test.
func localDisks(t *testing.T, opts ...docs.Option) *Disks {
	t.Helper()
	k := NewDisks(func(root string) (*docs.Docs, func(), error) {
		disk, err := local.New(root)
		if err != nil {
			return nil, nil, err
		}
		return docs.New(disk, nil, opts...), func() { _ = disk.Close() }, nil
	})
	t.Cleanup(k.Close)
	return k
}

// A volume is opened once, however often it is asked for; after Close none is.
func TestADisksVolumeIsOpenedOnce(t *testing.T) {
	opened := 0
	k := NewDisks(func(root string) (*docs.Docs, func(), error) {
		opened++
		return docs.New(nil, nil), func() {}, nil
	})
	a, err := k.For("")
	if err != nil {
		t.Fatal(err)
	}
	b, err := k.For("")
	if err != nil || a != b || opened != 1 {
		t.Fatalf("the same volume twice: %p %p, %v, opened %d times", a, b, err, opened)
	}
	k.Close()
	if _, err := k.For(""); !errors.Is(err, errDisksClosed) {
		t.Errorf("the volume already open, after Close: %v, want refused", err)
	}
	if _, err := k.For("other"); !errors.Is(err, errDisksClosed) {
		t.Errorf("another volume after Close: %v, want refused", err)
	}
	if opened != 1 {
		t.Errorf("opened %d times; Close opens nothing", opened)
	}
}

// A volume that fails to open is tried again by the next call, not kept failed.
func TestAVolumeThatFailedIsTriedAgain(t *testing.T) {
	tries := 0
	k := NewDisks(func(root string) (*docs.Docs, func(), error) {
		tries++
		if tries == 1 {
			return nil, nil, errors.New("the share did not answer")
		}
		return docs.New(nil, nil), func() {}, nil
	})
	if _, err := k.For("v"); err == nil {
		t.Fatal("the first try's error was not answered")
	}
	if _, err := k.For("v"); err != nil || tries != 2 {
		t.Fatalf("the second try: %v, %d tries", err, tries)
	}
}

// WithFiles is Unix's one volume: a path naming another volume has no files.
func TestWithFilesServesTheOneVolume(t *testing.T) {
	var o options
	WithFiles(docs.New(nil, nil))(&o)
	if _, err := o.disks.For(""); err != nil {
		t.Errorf("the one volume: %v", err)
	}
	if _, err := o.disks.For("C:"); !errors.Is(err, errNoFiles) {
		t.Errorf("another volume: %v, want errNoFiles", err)
	}
}

// gatedOpener is an opener that, for the volumes in slow, waits at a barrier: entered says it has
// begun, and it goes on when release is closed. It counts its opens and its closes.
type gatedOpener struct {
	slow    map[string]bool
	entered chan string
	release chan struct{}
	opens   atomic.Int32
	closes  atomic.Int32
}

func newGatedOpener(slow ...string) *gatedOpener {
	g := &gatedOpener{slow: map[string]bool{}, entered: make(chan string, 16), release: make(chan struct{})}
	for _, v := range slow {
		g.slow[v] = true
	}
	return g
}

func (g *gatedOpener) open(root string) (*docs.Docs, func(), error) {
	g.opens.Add(1)
	if g.slow[strings.TrimSuffix(root, string(filepath.Separator))] {
		g.entered <- root
		<-g.release
	}
	return docs.New(nil, nil), func() { g.closes.Add(1) }, nil
}

// A volume still opening when Close comes is closed once it opens, and never served: the call that
// opened it is refused.
func TestAVolumeOpeningAsTheDisksCloseIsNeverServed(t *testing.T) {
	g := newGatedOpener("v")
	k := NewDisks(g.open)
	got := make(chan error, 1)
	go func() {
		d, err := k.For("v")
		if err == nil && d != nil {
			err = errors.New("served")
		}
		got <- err
	}()
	<-g.entered // the open is under way
	k.Close()
	close(g.release)
	if err := <-got; !errors.Is(err, errDisksClosed) {
		t.Errorf("the call whose volume opened after Close: %v, want refused", err)
	}
	if n := g.closes.Load(); n != 1 {
		t.Errorf("the late volume closed %d times, want once", n)
	}
}

// A volume that takes long to open (a share whose server is slow) holds back only its own calls.
func TestASlowVolumeHoldsBackNoOther(t *testing.T) {
	g := newGatedOpener("slow")
	k := NewDisks(g.open)
	defer k.Close()
	defer close(g.release)
	go func() { _, _ = k.For("slow") }()
	<-g.entered // the slow volume is opening, and stays so
	done := make(chan error, 1)
	go func() { _, err := k.For("fast"); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("the other volume: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("another volume waited for the slow one to open")
	}
}

// Calls for one volume at once open it once, and each gets the same documents.
func TestCallsForOneVolumeAtOnceOpenItOnce(t *testing.T) {
	g := newGatedOpener("v")
	k := NewDisks(g.open)
	defer k.Close()
	const n = 8
	got := make(chan *docs.Docs, n)
	for range n {
		go func() { d, _ := k.For("v"); got <- d }()
	}
	<-g.entered
	close(g.release)
	first := <-got
	for range n - 1 {
		if d := <-got; d != first {
			t.Errorf("two calls got different documents")
		}
	}
	if o := g.opens.Load(); o != 1 {
		t.Errorf("opened %d times, want once", o)
	}
}
