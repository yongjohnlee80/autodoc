package rpc

import (
	"errors"
	"testing"

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
