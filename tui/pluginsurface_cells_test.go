package tui

import (
	"bytes"
	"image"
	"image/png"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yongjohnlee80/gio/op/extimage"
)

// memFrame is a w×h memory frame of format, every pixel px; released counts its release.
func memFrame(t *testing.T, w, h int, format extimage.Format, px [4]byte, released *atomic.Int32) *extimage.Frame {
	t.Helper()
	data := make([]byte, 4*w*h)
	for i := 0; i < len(data); i += 4 {
		copy(data[i:], px[:])
	}
	f, err := extimage.NewFrame(extimage.Desc{Kind: extimage.KindMemory, Format: format, Size: image.Pt(w, h),
		Planes: []extimage.Plane{{Data: data, Pitch: uint32(4 * w)}}}, func() { released.Add(1) })
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// paintedPictures are pictures whose card paints each one handed to it, as a shown card does;
// shown counts them.
func paintedPictures(shown *atomic.Int32) *cellPictures {
	var p *cellPictures
	p = newCellPictures(func(fn func()) { fn(); shown.Add(1); p.paintedNow() })
	return p
}

// TestATerminalSurfaceKeepsOneFrameInFlight: frames pushed faster than the encoder takes them leave
// one in flight and one waiting, the newest: each one overtaken while it waits is answered
// "superseded" at once, the one in flight and the last one "drawn", the encoder runs twice, and
// every frame's buffer is released exactly once.
func TestATerminalSurfaceKeepsOneFrameInFlight(t *testing.T) {
	var shown, released atomic.Int32
	s := newCellSink(paintedPictures(&shown))
	gate := make(chan struct{})
	var encodes atomic.Int32
	s.encode = func(f *extimage.Frame) ([]byte, error) {
		if encodes.Add(1) == 1 {
			<-gate // the first encode is slow: the rest arrive while it runs
		}
		return encodePNG(f)
	}
	var rs []surfaceReceipt
	for range 5 {
		rs = append(rs, s.Push(memFrame(t, 4, 2, extimage.FormatRGBA8, [4]byte{1, 2, 3, 255}, &released)))
	}
	for i := 1; i <= 3; i++ {
		select {
		case <-rs[i].Done():
			if o, _ := rs[i].Result(); o != "superseded" {
				t.Fatalf("frame %d overtaken while it waited: %q; want superseded", i, o)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("frame %d, overtaken, was not answered at once", i)
		}
	}
	select {
	case <-rs[0].Done():
		t.Fatal("the frame in flight was answered before its encode ended")
	default:
	}
	close(gate)
	for _, i := range []int{0, 4} {
		select {
		case <-rs[i].Done():
			if o, _ := rs[i].Result(); o != "drawn" {
				t.Fatalf("frame %d: %q; want drawn", i, o)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("frame %d was never answered", i)
		}
	}
	if n := encodes.Load(); n != 2 {
		t.Errorf("the encoder ran %d times; want 2: the frame in flight and the newest", n)
	}
	if n := released.Load(); n != 5 {
		t.Errorf("%d of 5 frames released", n)
	}
}

// TestATerminalSurfaceKeepsItsPace: frames pushed one after another are shown at most
// cellSurfaceFPS a second, each after the last one's turn.
func TestATerminalSurfaceKeepsItsPace(t *testing.T) {
	var shown, released atomic.Int32
	s := newCellSink(paintedPictures(&shown))
	start := time.Now()
	for range 4 {
		r := s.Push(memFrame(t, 2, 2, extimage.FormatRGBA8, [4]byte{9, 9, 9, 255}, &released))
		<-r.Done()
	}
	if min := 3 * time.Second / cellSurfaceFPS; time.Since(start) < min {
		t.Fatalf("4 frames shown in %v; want %v at least (%d a second)", time.Since(start), min, cellSurfaceFPS)
	}
}

// TestATerminalSurfaceReplacesItsPictureInPlace: a card's frames are one picture, its id kept and
// its version moving, the newest frame's pixels in it (a BGRA frame's channels put in RGBA order);
// closed, the picture goes and a frame after is answered "closed".
func TestATerminalSurfaceReplacesItsPictureInPlace(t *testing.T) {
	var shown, released atomic.Int32
	pics := paintedPictures(&shown)
	s := newCellSink(pics)
	var ids sync.Map
	var last uint64
	for i := range 6 {
		format, px := extimage.FormatRGBA8, [4]byte{byte(10 * i), 20, 30, 255}
		if i == 5 {
			format, px = extimage.FormatBGRA8, [4]byte{30, 20, 200, 255} // blue 30, red 200
		}
		<-s.Push(memFrame(t, 3, 3, format, px, &released)).Done()
		img, ok := pics.image()
		if !ok || img.Version <= last {
			t.Fatalf("frame %d: picture %v (version %d after %d)", i, ok, img.Version, last)
		}
		last = img.Version
		ids.Store(img.ID, true)
	}
	n := 0
	ids.Range(func(any, any) bool { n++; return true })
	if n != 1 {
		t.Fatalf("the card's frames made %d picture ids; want one, replaced in place", n)
	}
	img, _ := pics.image()
	m, err := png.Decode(bytes.NewReader(img.PNG))
	if err != nil {
		t.Fatal(err)
	}
	if r, g, b, _ := m.At(1, 1).RGBA(); r>>8 != 200 || g>>8 != 20 || b>>8 != 30 {
		t.Fatalf("the BGRA frame's pixel reads %d,%d,%d; want 200,20,30", r>>8, g>>8, b>>8)
	}
	s.Close()
	if _, ok := pics.image(); ok {
		t.Fatal("the picture outlived its surface's close")
	}
	if o, _ := waitResult(t, s.Push(memFrame(t, 1, 1, extimage.FormatRGBA8, [4]byte{}, &released))); o != "closed" {
		t.Fatalf("a frame after the close: %q; want closed", o)
	}
	if n := released.Load(); n != 7 {
		t.Errorf("%d of 7 frames released", n)
	}
}

// TestAHiddenTerminalSurfaceIsNotShown: a card that is not painted (hidden) answers its frame
// "not_shown"; a frame the terminal can't show (not memory RGBA or BGRA) is "unsupported".
func TestAHiddenTerminalSurfaceIsNotShown(t *testing.T) {
	var released atomic.Int32
	s := newCellSink(newCellPictures(func(fn func()) { fn() })) // never painted
	if o, _ := waitResult(t, s.Push(memFrame(t, 2, 2, extimage.FormatRGBA8, [4]byte{}, &released))); o != "not_shown" {
		t.Fatalf("a hidden card's frame: %q; want not_shown", o)
	}
	nv12, err := extimage.NewFrame(extimage.Desc{Kind: extimage.KindMemory, Format: extimage.FormatNV12, Size: image.Pt(2, 2),
		Planes: []extimage.Plane{{Data: make([]byte, 4), Pitch: 2}, {Data: make([]byte, 2), Pitch: 2}}}, func() { released.Add(1) })
	if err != nil {
		t.Fatal(err)
	}
	if o, _ := waitResult(t, s.Push(nv12)); o != "unsupported" {
		t.Fatalf("an NV12 frame: %q; want unsupported", o)
	}
}

func waitResult(t *testing.T, r surfaceReceipt) (string, time.Time) {
	t.Helper()
	select {
	case <-r.Done():
		return r.Result()
	case <-time.After(5 * time.Second):
		t.Fatal("a frame was never answered")
		return "", time.Time{}
	}
}

// TestACloseIsNotUndoneByAFrameInFlight: a close that comes while a frame's picture is being
// published waits for it, then clears it, so a picture never outlives its close. The interleaving is
// controlled: the close starts at the moment of publication.
func TestACloseIsNotUndoneByAFrameInFlight(t *testing.T) {
	var shown, released atomic.Int32
	pics := paintedPictures(&shown)
	s := newCellSink(pics)
	closed := make(chan struct{})
	s.publishing = func() {
		go func() { s.Close(); close(closed) }()
		select { // under the fence the close waits for this publication; without it, it clears first
		case <-closed:
		case <-time.After(100 * time.Millisecond):
		}
	}
	r := s.Push(memFrame(t, 2, 2, extimage.FormatRGBA8, [4]byte{1, 1, 1, 255}, &released))
	waitResult(t, r)
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("the close never ended")
	}
	if _, ok := pics.image(); ok {
		t.Fatal("a frame published as the card closed outlived the close")
	}
}

// TestAnOldPaintAcknowledgesNoNewerPicture: a paint of an earlier picture, its signal still waiting,
// doesn't make a newer one drawn; a paint of the newer one does.
func TestAnOldPaintAcknowledgesNoNewerPicture(t *testing.T) {
	pics := newCellPictures(func(fn func()) { fn() })
	pics.publish([]byte("first"))
	pics.paintedNow() // the first painted: its signal waits unread
	v2 := pics.publish([]byte("second"))
	if pics.waitPainted(v2, nil) {
		t.Fatal("the first picture's paint acknowledged the second")
	}
	pics.paintedNow()
	if !pics.waitPainted(v2, nil) {
		t.Fatal("the second picture's paint did not acknowledge it")
	}
}

// TestAFrameClosedBeforeItsPaintIsClosed: a frame published, then the card's surface closed before
// the card paints it, is answered "closed", never "drawn" by the close's own paint (which shows no
// picture) nor "not_shown".
func TestAFrameClosedBeforeItsPaintIsClosed(t *testing.T) {
	var released atomic.Int32
	pics := newCellPictures(func(fn func()) { fn() }) // the card paints only when the test says
	s := newCellSink(pics)
	published := make(chan struct{})
	s.publishing = func() { close(published) }
	r := s.Push(memFrame(t, 2, 2, extimage.FormatRGBA8, [4]byte{5, 5, 5, 255}, &released))
	<-published
	time.Sleep(20 * time.Millisecond) // the frame waits for its paint
	s.Close()
	pics.paintedNow() // the close's paint: no picture
	if o, _ := waitResult(t, r); o != "closed" {
		t.Fatalf("a frame closed before its paint: %q; want closed", o)
	}
}
