//go:build gui

package tui

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	graphlayout "github.com/yongjohnlee80/golib/graph/layout"
	"github.com/yongjohnlee80/golib/gui"
	tuicore "github.com/yongjohnlee80/golib/tui"
)

// Run as a window, the GUI writes its relation views into main.qml once each: the Relations menu's
// two items, and a panel each whose view is its own type. Run in a terminal, the same build writes
// nothing: the terminal has Links alone.
func TestTheGUIWritesTheRelationViews(t *testing.T) {
	if !bytes.Equal(withRelationViews(layout, false), layout) {
		t.Fatal("a terminal run of the GUI build wrote the relation views in")
	}
	doc := string(withRelationViews(layout, true))
	for _, want := range []string{
		`MenuItem { text: "&GlobeMap"; onTriggered: App.toggleGlobeMap() }`,
		`MenuItem { text: "&Mindmap"; onTriggered: App.toggleMindmap() }`,
		"id: globeMap\n", "GlobeMap {\n            id: globeMapView", `App.panelOpened("globeMap")`,
		"id: mindmap\n", "Mindmap {\n            id: mindmapView", `App.panelOpened("mindmap")`,
	} {
		if n := strings.Count(doc, want); n != 1 {
			t.Errorf("main.qml holds %q %d times, want once", want, n)
		}
	}
	if !bytes.Equal(withRelationViews([]byte("Window {}"), true), []byte("Window {}")) {
		t.Error("a layout without the markers was written to")
	}
}

// relMapOf is a map of n documents, each linked to the next and every seventh superseded.
func relMapOf(n int) relMap {
	var m relMap
	types := append([]string{""}, relationTypes...)
	for i := range n {
		m.Nodes = append(m.Nodes, mapNode{Path: fmt.Sprintf("d%04d.md", i), Title: fmt.Sprint("Doc ", i),
			Type: types[i%len(types)], Superseded: i%7 == 3})
		if i > 0 {
			m.Edges = append(m.Edges, mapEdge{Src: i - 1, Dst: i, Kind: []string{"wikilink", "supersedes", "related"}[i%3]})
		}
	}
	return m
}

// settledGlobe is m laid out by the simulation to its end, as the GlobeMap's last frame.
func settledGlobe(t *testing.T, m relMap) *globe {
	t.Helper()
	in := graphlayout.ForceInput{Nodes: len(m.Nodes), Seed: 1}
	for _, e := range m.Edges {
		in.Edges = append(in.Edges, graphlayout.Edge{From: e.Src, To: e.Dst})
	}
	sim, err := graphlayout.NewSimulation(in, globeLimits)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sim.Step(context.Background(), 1000); err != nil {
		t.Fatal(err)
	}
	at, b, err := sim.Frame(nil)
	if err != nil {
		t.Fatal(err)
	}
	return newGlobe("kb", m, degrees(m), at, b, true)
}

var globeView800 = globeViewState{size: gui.Size{W: 800, H: 600}, pal: defaultPalette(), hover: -1}

// A picture of the map draws its paths in a constant number of operations whatever its size: the
// ground, a stroke per edge class, a fill per document type, the rings. Names add one DrawText a
// row of them, never one a name.
func TestTheGlobeMapsPictureIsConstantOperations(t *testing.T) {
	paths := 0
	for _, n := range []int{12, 600} {
		gl := settledGlobe(t, relMapOf(n))
		rc := gui.NewRecordingCanvas(globeView800.size, gui.Size{W: 8, H: 16})
		p := pictureOf(gl, globeView800, globeDots{})
		p.paint(rc)
		got := countOps(rc, "FillRect") + countOps(rc, "StrokePath") + countOps(rc, "FillPath")
		if paths == 0 {
			paths = got
		} else if got != paths {
			t.Errorf("%d documents: %d path operations; %d for the smaller map", n, got, paths)
		}
		if fills := countOps(rc, "FillPath"); fills != len(relationTypes)+1 {
			t.Errorf("%d documents: %d path fills, want one per type", n, fills)
		}
		if texts, rows := countOps(rc, "DrawText"), len(p.nameRows(globeView800.size)); texts != rows {
			t.Errorf("%d documents: %d texts for %d rows of names", n, texts, rows)
		}
	}
}

func countOps(rc *gui.RecordingCanvas, op string) int {
	n := 0
	for _, c := range rc.Calls {
		if c.Op == op {
			n++
		}
	}
	return n
}

// The names cost one DrawText a row, whatever their number: none, 50 or 300 names in view draw at
// most a row's worth of texts (the view's height over globeNameRow), and the hovered name one more.
// The view's own paint is the stream's slot alone.
func TestTheGlobeMapsNamesCostOneDrawTextARow(t *testing.T) {
	maxRows := int(globeView800.size.H/globeNameRow) + 2
	for _, docs := range []int{0, 50, globeLabelCap} {
		gl := settledGlobe(t, relMapOf(max(docs, 1)))
		for _, hover := range []int{-1, 0} {
			p := pictureOf(gl, globeView800, globeDots{})
			p.hover = hover
			rc := gui.NewRecordingCanvas(globeView800.size, gui.Size{W: 8, H: 16})
			p.paint(rc)
			named := 0
			for _, runs := range p.nameRows(globeView800.size) {
				named += len(runs)
			}
			texts := countOps(rc, "DrawText")
			if texts > maxRows+1 || docs > 1 && named == 0 {
				t.Errorf("%d documents, hover %d: %d texts for %d names; want a row's worth at most", docs, hover, texts, named)
			}
		}
	}
	rc := gui.NewRecordingCanvas(globeView800.size, gui.Size{W: 8, H: 16})
	globeOverlay{pres: &globeStream{pub: &paintPresenter{}}, pal: defaultPalette()}.paint(rc)
	if n := countOps(rc, "DrawText") + countOps(rc, "PaintCells"); n != 0 {
		t.Errorf("the view's own paint drew %d texts or cells over the picture", n)
	}
}

// A dot is about the same size on screen whatever the zoom (a little larger past the fit, never in
// proportion): zoomed in eightfold, the space between two documents grows eightfold and the dots
// barely, so a cluster comes apart. Its paths are made again only when the zoom changes its step.
func TestTheGlobeMapsDotsKeepTheirSizeOnScreen(t *testing.T) {
	gl := settledGlobe(t, relMapOf(40))
	d1 := dotsAt(globeDots{}, gl, 1, 1)
	if again := dotsAt(d1, gl, 1, 1); again.dots[0] != d1.dots[0] {
		t.Fatal("the same frame and zoom made its dots again")
	}
	if dotsAt(d1, gl, 1.05, 1).dots[0] != d1.dots[0] {
		t.Fatal("a zoom within its step made the dots again")
	}
	if dotsAt(d1, gl, 8, 1).dots[0] == d1.dots[0] {
		t.Fatal("a zoom eight times in kept the dots made for the fit")
	}
	// the path itself: one document's dot, in layout units, shrinks with the zoom (a little less)
	one := settledGlobe(t, relMapOf(1))
	w1 := dotsAt(globeDots{}, one, 1, 1).dots[len(relationTypes)].Bounds().W
	w8 := dotsAt(globeDots{}, one, 8, 1).dots[len(relationTypes)].Bounds().W
	if ratio := w8 * 8 / w1; ratio < 1 || ratio > 2 {
		t.Errorf("a dot %v layout units wide at zoom 1, %v at zoom 8: on screen it grew %v times; want 1 to 2", w1, w8, ratio)
	}
	r1, r8 := gl.dotRadius(0, 1, 1), gl.dotRadius(0, 8, 1)
	if r8 > 2*r1 || r8 < r1 {
		t.Errorf("a dot %v px at zoom 1, %v px at zoom 8: want it about the same, a little larger", r1, r8)
	}
	a, b := gl.at[0], gl.at[1]
	gap := float32(math.Hypot(float64(a.X-b.X), float64(a.Y-b.Y)))
	if gap*8-2*r8 <= gap*1-2*r1 {
		t.Error("zooming in eightfold did not spread two documents apart on screen")
	}
}

// Once globeLabelCap or fewer documents are in view, each one's name is set under its dot, in rows,
// none running into the one before it in its row, and following the pan; more in view, none are.
func TestTheGlobeMapNamesTheDocumentsInView(t *testing.T) {
	gl := settledGlobe(t, relMapOf(globeLabelCap+50))
	view := globeViewState{size: gui.Size{W: 800, H: 592}, pal: defaultPalette(), hover: -1}
	if rows := pictureOf(gl, view, globeDots{}).nameRows(view.size); len(rows) != 0 {
		t.Fatalf("the whole map in view named %d rows", len(rows))
	}
	fz, foff := fitView(gl.bounds, view.size)
	view.moved, view.zoom = true, fz*8
	view.off = gui.Pt(400-(400-foff.X)*8, 296-(296-foff.Y)*8)
	rows := pictureOf(gl, view, globeDots{}).nameRows(view.size)
	if len(rows) == 0 {
		t.Fatal("zoomed in eightfold: no names")
	}
	firstX := map[string]float32{}
	for _, runs := range rows {
		for k, r := range runs {
			firstX[r.Text] = r.X
			if k > 0 && r.X < runs[k-1].X+nameWidth(runs[k-1].Text) {
				t.Fatalf("%q runs into %q", r.Text, runs[k-1].Text)
			}
		}
	}
	view.off.X += 80
	moved := false
	for _, runs := range pictureOf(gl, view, globeDots{}).nameRows(view.size) {
		for _, r := range runs {
			if x, ok := firstX[r.Text]; ok && r.X == x+80 {
				moved = true
			}
		}
	}
	if !moved {
		t.Error("no name followed an 80-pixel pan")
	}

	// two documents side by side on one row: the second name would run into the first, so it is left out
	m := relMapOf(3)
	at := []graphlayout.Point{{X: 100, Y: 100}, {X: 110, Y: 100}, {X: 400, Y: 100}}
	close3 := newGlobe("kb", m, degrees(m), at, graphlayout.Rect{X: 100, Y: 100, W: 300, H: 1}, true)
	pic := globePicture{gl: close3, zoom: 1, pal: defaultPalette(), hover: -1}
	var names []string
	for _, runs := range pic.nameRows(gui.Size{W: 800, H: 600}) {
		for _, r := range runs {
			names = append(names, r.Text)
		}
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"Doc 0", "Doc 2"}) {
		t.Errorf("names on a row with two documents 10px apart: %q; want the first and the far one, not the second", names)
	}
}

// A press finds the document under it, within a few pixels of its dot, and only that.
func TestTheGlobeMapHitsTheDocumentUnderThePointer(t *testing.T) {
	gl := settledGlobe(t, relMapOf(40))
	shot := &globeShot{f: gl, zoom: 1}
	for _, i := range []int{0, 17, 39} {
		at := gl.at[i]
		if got := shot.at(gui.Pt(at.X, at.Y)); got != i {
			t.Errorf("at document %d's centre: %d", i, got)
		}
	}
	if got := shot.at(gui.Pt(gl.bounds.X-1000, gl.bounds.Y-1000)); got != -1 {
		t.Errorf("far off the map: %d", got)
	}
}

// heldReceipt is a picture's outcome the test completes by hand.
type heldReceipt struct {
	done    chan struct{}
	outcome gui.Outcome
}

func (r *heldReceipt) Done() <-chan struct{} { return r.done }
func (r *heldReceipt) Outcome() gui.Outcome {
	select {
	case <-r.done:
		return r.outcome
	default:
		return 0
	}
}

// heldPresenter hands each push's receipt to the test, which presents it when it chooses.
type heldPresenter struct{ pushed chan *heldReceipt }

func (p heldPresenter) Push(gui.Size, func(gui.Canvas)) globeReceipt {
	r := &heldReceipt{done: make(chan struct{})}
	p.pushed <- r
	return r
}

// The settling is paced by presentation, one picture in flight at a time. The simulation steps only
// once the picture before has been presented (Drawn) or replaced (Superseded); a frame is shown
// only when its picture is Drawn, with the transform it was drawn at. View changes that come while
// a receipt is in hand wait for it (two of them: no push until it is handled, then one, at the
// newest). A hidden view (NotShown) holds the load until the view changes; a closed stream ends it.
func TestTheGlobeMapSettlesAsTheWindowPresents(t *testing.T) {
	m := relMapOf(30)
	in := graphlayout.ForceInput{Nodes: len(m.Nodes), Seed: 1}
	for _, e := range m.Edges {
		in.Edges = append(in.Edges, graphlayout.Edge{From: e.Src, To: e.Dst})
	}
	sim, err := graphlayout.NewSimulation(in, globeLimits)
	if err != nil {
		t.Fatal(err)
	}
	type pushed struct {
		r    *heldReceipt
		shot *globeShot
	}
	pushes := make(chan pushed, 4)
	push := func(shot *globeShot, _ func(gui.Canvas)) globeReceipt {
		r := &heldReceipt{done: make(chan struct{})}
		pushes <- pushed{r, shot}
		return r
	}
	views := make(chan globeViewState, 1)
	shown, retired, hid := make(chan *globeShot, 64), make(chan *globeShot, 64), make(chan struct{}, 4)
	ended := make(chan error, 1)
	go func() {
		ended <- settle(context.Background(), sim, "kb", m, push, views,
			func(s *globeShot) { shown <- s }, func(s *globeShot) { retired <- s }, func() { hid <- struct{}{} })
	}()
	views <- globeView800
	next := func(what string) pushed {
		t.Helper()
		select {
		case p := <-pushes:
			return p
		case <-time.After(10 * time.Second):
			t.Fatalf("%s: nothing pushed", what)
			return pushed{}
		}
	}
	none := func(what string) {
		t.Helper()
		select {
		case s := <-shown:
			t.Fatalf("%s: a picture was shown (%p)", what, s)
		case p := <-pushes:
			t.Fatalf("%s: a push (%p)", what, p.shot)
		case <-time.After(150 * time.Millisecond):
		}
	}
	present := func(p pushed, o gui.Outcome) { p.r.outcome = o; close(p.r.done) }
	from := func(ch chan *globeShot, what string) *globeShot {
		t.Helper()
		select {
		case s := <-ch:
			return s
		case <-time.After(10 * time.Second):
			t.Fatalf("%s: nothing came", what)
			return nil
		}
	}

	p0 := next("the seeded start's picture")
	none("receipt 0 in hand: neither shown nor stepped")
	present(p0, gui.Drawn)
	if s := from(shown, "picture 0 drawn"); s != p0.shot || s.zoom <= 0 {
		t.Fatalf("shown %p, want picture 0 with its transform", s)
	}
	p1 := next("picture 1, a step on")
	if sameFrame(p0.shot.f, p1.shot.f) {
		t.Fatal("presenting picture 0 did not step the simulation")
	}
	present(p1, gui.Superseded)
	select {
	case s := <-shown:
		t.Fatalf("a superseded picture was shown (%p)", s)
	default:
	}
	if s := from(retired, "picture 1 superseded"); s != p1.shot {
		t.Fatalf("a superseded picture %p was not retired", s)
	}
	p2 := next("picture 2")
	// two view changes while receipt 2 is in hand: they wait for it
	views <- globeViewState{size: gui.Size{W: 700, H: 500}, pal: defaultPalette(), hover: -1}
	time.Sleep(50 * time.Millisecond)
	select {
	case <-views: // not yet taken: replace it, as the loop does, with the newest
	default:
	}
	views <- globeViewState{size: gui.Size{W: 640, H: 480}, pal: defaultPalette(), hover: -1}
	none("two view changes while receipt 2 is in hand")
	present(p2, gui.Drawn)
	if s := from(shown, "picture 2 drawn"); s != p2.shot {
		t.Fatalf("shown %p, want picture 2", s)
	}
	p3 := next("picture 3, at the newest view")
	if p3.shot.size != (gui.Size{W: 640, H: 480}) {
		t.Fatalf("picture 3 at %v, want the newest view's 640×480", p3.shot.size)
	}
	present(p3, gui.NotShown)
	select {
	case <-hid:
	case <-time.After(10 * time.Second):
		t.Fatal("a hidden view was not reported")
	}
	none("the view hidden")
	views <- globeViewState{size: gui.Size{W: 640, H: 480}, pal: defaultPalette(), hover: 3} // drawn again, hovering
	p4 := next("the same step again at the view")
	if !sameFrame(p3.shot.f, p4.shot.f) {
		t.Fatal("a view change after a hidden view stepped the simulation")
	}
	present(p4, gui.Closed)
	select {
	case err := <-ended:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a closed stream did not end the load")
	}
}

// Every wait of a load ends with its context: before its first view, held on a hidden view, and
// settled waiting for a view change.
func TestTheGlobeMapsLoadEndsWhereverItWaits(t *testing.T) {
	m := relMapOf(6)
	for _, where := range []string{"the first view", "hidden", "settled"} {
		in := graphlayout.ForceInput{Nodes: len(m.Nodes), Seed: 1, Steps: 2}
		sim, _ := graphlayout.NewSimulation(in, globeLimits)
		ctx, cancel := context.WithCancel(context.Background())
		views := make(chan globeViewState, 1)
		pushed := make(chan *heldReceipt, 8)
		ended := make(chan error, 1)
		go func() {
			push := func(shot *globeShot, draw func(gui.Canvas)) globeReceipt {
				return heldPresenter{pushed: pushed}.Push(shot.size, draw)
			}
			ended <- settle(ctx, sim, "kb", m, push, views, func(*globeShot) {}, func(*globeShot) {}, func() {})
		}()
		present := func(o gui.Outcome) {
			select {
			case r := <-pushed:
				r.outcome = o
				close(r.done)
			case <-time.After(10 * time.Second):
				t.Fatalf("%s: nothing pushed", where)
			}
		}
		switch where {
		case "hidden":
			views <- globeView800
			present(gui.NotShown)
		case "settled":
			views <- globeView800
			present(gui.Drawn) // the start
			present(gui.Drawn) // both steps in one: settled, drawn; now it waits for a view change
		}
		time.Sleep(50 * time.Millisecond) // waiting where it should
		cancel()
		select {
		case <-ended:
		case <-time.After(10 * time.Second):
			t.Fatalf("%s: the load did not end with its context", where)
		}
	}
}

// gatedPublisher records what it publishes, and holds a publication until the test releases it.
type gatedPublisher struct {
	mu        sync.Mutex
	published []string
	drawing   string // the picture being recorded: set by its draw
	entered   chan string
	release   chan struct{}
}

func (p *gatedPublisher) Push(_ gui.Size, draw func(gui.Canvas)) globeReceipt {
	draw(nil) // a test picture: it names itself, drawing nothing
	name := p.drawing
	p.entered <- name
	<-p.release
	p.mu.Lock()
	p.published = append(p.published, name)
	p.mu.Unlock()
	return doneReceipt(gui.Drawn)
}
func (*gatedPublisher) Draw(gui.Canvas, gui.Rect) {}
func (*gatedPublisher) Close()                    {}

// picture is a test picture that says whose it is.
func (p *gatedPublisher) picture(name string) func(gui.Canvas) {
	return func(gui.Canvas) { p.drawing = name }
}

// The stream publishes for the newest load only: an older load held in the middle of publishing
// finishes before the newer one can publish (pushes are one at a time), and once the newer has
// begun nothing of the older's is published again.
func TestTheGlobeMapsStreamPublishesTheNewestLoadOnly(t *testing.T) {
	pub := &gatedPublisher{entered: make(chan string, 4), release: make(chan struct{})}
	s := &globeStream{pub: pub}
	a := s.begin()
	go s.push(a, &globeShot{size: gui.Size{W: 1, H: 1}}, pub.picture("A"))
	if got := <-pub.entered; got != "A" {
		t.Fatalf("publishing %q", got)
	}
	b := s.begin() // B begins while A is held in publication
	bDone := make(chan globeReceipt, 1)
	go func() { bDone <- s.push(b, &globeShot{size: gui.Size{W: 1, H: 1}}, pub.picture("B")) }()
	select {
	case got := <-pub.entered:
		t.Fatalf("%s published while A held the stream", got)
	case <-time.After(150 * time.Millisecond):
	}
	close(pub.release) // A's publication completes, then B's
	if got := <-pub.entered; got != "B" {
		t.Fatalf("after A: %q", got)
	}
	<-bDone
	if r := s.push(a, &globeShot{size: gui.Size{W: 1, H: 1}}, pub.picture("A again")); r.Outcome() != gui.Closed {
		t.Fatalf("a push of the older load after the newer began: %v; want Closed", r.Outcome())
	}
	pub.mu.Lock()
	defer pub.mu.Unlock()
	if !slices.Equal(pub.published, []string{"A", "B"}) {
		t.Fatalf("published %v; want A then B, and nothing of A after B began", pub.published)
	}
}

// onScreen is a GlobeMap whose stream has presented gl at view, fitted, steady.
func onScreen(t *testing.T, gl *globe, view gui.Size) (*globeMap, uint64, *globeShot) {
	t.Helper()
	s := &globeStream{pub: &paintPresenter{}}
	gen := s.begin()
	z, off := fitView(gl.bounds, view)
	shot := &globeShot{f: gl, zoom: z, off: off, size: view}
	s.push(gen, shot, func(gui.Canvas) {})
	s.presented(gen, shot)
	return &globeMap{c: newScripted("kb"), pres: s, hover: -1}, gen, shot
}

// shifted is gl's documents all moved by d, as a later frame of the settling might place them.
func shifted(gl *globe, d float32) *globe {
	at := make([]graphlayout.Point, len(gl.at))
	for i, p := range gl.at {
		at[i] = graphlayout.Point{X: p.X + d, Y: p.Y + d}
	}
	b := gl.bounds
	b.X, b.Y = b.X+d, b.Y+d
	return newGlobe(gl.ws, gl.m, gl.deg, at, b, gl.final)
}

// The pointer acts only on what the screen agrees on: the picture presented and every picture pushed
// since that is not retired. Picture k pushed while k-1 is presented: a press where they find
// different documents is refused, never k-1's target, even once k's receipt is Drawn but not yet
// handled; where they agree it acts. A picture submitted but not yet handled stays possible when
// another is pushed after it (S0 presented, S1 pushed, S2 pushed back to S0's places: S1 still
// refuses). Presented, a picture retires every one before it. A resized view refuses too.
func TestTheGlobeMapsPointerActsOnlyOnWhatTheScreenAgreesOn(t *testing.T) {
	view := globeView800.size
	gl := settledGlobe(t, relMapOf(40))
	g, gen, s0 := onScreen(t, gl, view)
	at := gl.at[7]
	p := gui.Pt(at.X*s0.zoom+s0.off.X, at.Y*s0.zoom+s0.off.Y)
	if shot, i, ok := g.target(p, view); !ok || i != 7 || shot != s0 {
		t.Fatalf("steady, on document 7: %d, %v", i, ok)
	}
	push := func(f *globe) *globeShot {
		shot := &globeShot{f: f, zoom: s0.zoom, off: s0.off, size: view}
		g.pres.push(gen, shot, func(gui.Canvas) {})
		return shot
	}

	same := push(shifted(gl, 0)) // agrees under the pointer: acts
	if _, i, ok := g.target(p, view); !ok || i != 7 {
		t.Errorf("k pushed, agreeing under the pointer: %d, %v; want 7", i, ok)
	}
	g.pres.presented(gen, same)

	s1 := push(shifted(gl, 1e5)) // panned away: Drawn, its handling held
	if _, i, ok := g.target(p, view); ok {
		t.Fatalf("k pushed, disagreeing: acted on %d; want the press refused", i)
	}
	s2 := push(shifted(gl, 0)) // panned back: agrees with what is presented, but S1 may be on screen
	if _, i, ok := g.target(p, view); ok {
		t.Fatalf("S1 submitted and unhandled, S2 pushed back: acted on %d; want refused while S1 may show", i)
	}
	g.pres.presented(gen, s1)
	if shot, i, ok := g.target(p, view); ok || shot != nil {
		t.Fatalf("S1 presented, S2 still possible and disagreeing: %d, %v; want refused", i, ok)
	}
	g.pres.presented(gen, s2) // S2 presented: S1 retired with it
	if shot, i, ok := g.target(p, view); !ok || i != 7 || shot != s2 {
		t.Fatalf("S2 presented: %d, %v; want 7 from S2", i, ok)
	}
	s3 := push(shifted(gl, 1e5))
	g.pres.retire(gen, s3) // superseded: never drawn
	if _, i, ok := g.target(p, view); !ok || i != 7 {
		t.Fatalf("a superseded picture still refused the press: %d, %v", i, ok)
	}
	if _, _, ok := g.target(p, gui.Size{W: 640, H: 480}); ok {
		t.Error("a press in a view resized since its picture was drawn was acted on")
	}
}

// A load's frames reach the loop only while it is the newest and the panel is open: a newer load
// stops the older, whose frames never show; closing the panel stops the newest.
func TestTheGlobeMapShowsOnlyItsNewestLoad(t *testing.T) {
	a := &asyncCore{scriptedCore: newScripted("kb"), posted: make(chan func(), 64)}
	sizes := []int{20, 30, 40}
	a.answers["graph.map"] = func([]any) (any, error) {
		n := sizes[0]
		sizes = sizes[1:]
		return mapAnswer(nodesOf(n), nil), nil
	}
	g := newGlobeMap(a, func() globePublisher { return &paintPresenter{} })
	spec := g.Panels()[0]
	spec.Opened(nil) // the load of 20
	g.sendView(globeView800.size, true)
	a.run(t, func() bool { return g.cur != nil })
	g.load() // the load of 30: the load of 20 stops, and what it still hands over never shows
	g.sendView(globeView800.size, true)
	a.run(t, func() bool {
		if g.cur != nil && len(g.cur.m.Nodes) == 20 && g.status == "" && g.views != nil {
			return false // the older load's frame, shown before the newer began: wait on
		}
		return g.cur != nil && len(g.cur.m.Nodes) == 30 && g.cur.final
	})
	shown := g.cur
	g.load() // the load of 40, closed before it shows anything
	g.sendView(globeView800.size, true)
	h := &closeHost{}
	spec.Closed(h)
	for range 3 {
		select {
		case fn := <-a.posted:
			fn()
		case <-time.After(200 * time.Millisecond):
		}
	}
	if g.cur != shown || len(h.restored) != 1 {
		t.Fatalf("after closing: the frame shown changed (%v), the keyboard %v", g.cur != shown, h.restored)
	}
}

func nodesOf(n int) [][4]any {
	var out [][4]any
	for i := range n {
		out = append(out, [4]any{fmt.Sprintf("d%d.md", i), "", "", false})
	}
	return out
}

// The Mindmap is rooted at the page shown, and only by Shown: a click opens the document under it
// with the keyboard kept on the Mindmap, and reads nothing itself; the page the click shows roots
// it again.
func TestTheMindmapRootsAtThePageShown(t *testing.T) {
	c := newScripted("kb")
	c.answers["graph.map"] = func(p []any) (any, error) {
		root := p[1].(map[string]any)["root"].(string)
		return mapAnswer([][4]any{{"a.md", "A", "adr", false}, {"b.md", "B", "review", false}, {"c.md", "C", "", false}},
			[][3]string{{"a.md", "b.md", "wikilink"}, {"b.md", "c.md", "wikilink"}, {root, root, "wikilink"}}), nil
	}
	m := newMindmap(c)
	m.Shown(Page{Ref: fileRef{"kb", "a.md"}}) // closed: nothing read
	if len(c.calls) != 0 {
		t.Fatalf("a closed Mindmap read %v", c.calls)
	}
	m.Panels()[0].Opened(nil)
	if m.cur == nil || m.cur.root.path != "a.md" || len(c.calls) != 1 || !strings.Contains(c.calls[0], "root:a.md") {
		t.Fatalf("opened over a.md: %+v, calls %v", m.cur, c.calls)
	}
	p, ok := pointOf(m.cur, "b.md")
	if !ok {
		t.Fatal("b.md is nowhere in the drawn map")
	}
	m.click(p, gui.Point{})
	if want := []string{"kb:b.md focus " + mindmapPanelID}; !equalStrings(c.files, want) || len(c.calls) != 1 {
		t.Fatalf("the click opened %v and read %v; want b.md with the keyboard kept, and no read", c.files, c.calls)
	}
	m.Shown(Page{Ref: fileRef{"kb", "b.md"}}) // the page the click opened
	if m.cur.root.path != "b.md" || len(c.calls) != 2 {
		t.Fatalf("after b.md was shown: rooted at %s, calls %v", m.cur.root.path, c.calls)
	}
	m.Shown(Page{Ref: fileRef{"", "/tmp/x.md"}, Outside: true})
	if m.cur != nil || len(c.calls) != 2 {
		t.Fatalf("a file in no workspace: %+v, calls %v", m.cur, c.calls)
	}
}

// pointOf is a point in the build's drawing (its own coordinates) over the node of document p.
func pointOf(cur *mind, p string) (gui.Point, bool) {
	sz := cur.laid.Size()
	for y := float32(0); y < sz.H; y += 2 {
		for x := float32(0); x < sz.W; x += 2 {
			if id, ok := cur.laid.NodeAt(gui.Pt(x, y)); ok {
				if i, ok := cur.m.mindIndex(id); ok && cur.m.Nodes[i].Path == p {
					return gui.Pt(x, y), true
				}
			}
		}
	}
	return gui.Point{}, false
}

func equalStrings(a, b []string) bool { return strings.Join(a, "\x00") == strings.Join(b, "\x00") }

// In the host, Relations › GlobeMap opens over the workspace's map, and Relations › Mindmap over
// the page's document, one link out; d in the Mindmap shows two links out, and d again one. Each
// closes back to the editor.
func TestTheRelationViewsOpenInTheHost(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "[[b]]\n", "b.md", "[[c]]\n", "c.md", "ccc\n"}})
	r := windowed(t, d)
	r.waitListed(t, 3)
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	for _, v := range []struct{ panel, view, title string }{
		{globeMapPanelID, globeMapViewID, "kb · 3 documents, 2 links"},
		{mindmapPanelID, mindmapViewID, "a.md · 2 documents · depth 1 · d depth"},
	} {
		onLoop(r, func() bool { core{r.h}.TogglePanel(v.panel); return true })
		r.s.WaitForText(t, v.title)
		r.s.WaitFor(t, v.panel+" open with the keyboard", func(string) bool { return r.panelIsOpen(v.panel) && r.focusedOn(v.view) })
		if v.panel == mindmapPanelID {
			r.keys(t, key('d'))
			r.s.WaitForText(t, "a.md · 3 documents · depth 2 · d depth")
			r.keys(t, key('d'))
			r.s.WaitForText(t, "a.md · 2 documents · depth 1 · d depth")
		}
		onLoop(r, func() bool { core{r.h}.TogglePanel(v.panel); return true })
		r.s.WaitFor(t, v.panel+" closed, the editor's keyboard", func(string) bool { return !r.panelIsOpen(v.panel) && r.focusedOn("editor") })
	}
}

// A theme switch builds the views again in the theme's colours: the document reloads, the GlobeMap
// open keeps its map in a new view, and the Mindmap lays its map out again.
func TestTheRelationViewsFollowATheme(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "[[b]]\n", "b.md", "bbb\n"}})
	r := windowed(t, d)
	r.waitListed(t, 2)
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	var g *globeMap
	var m *mindmap
	onLoop(r, func() bool {
		for _, f := range r.h.relationViews {
			switch f := f.(type) {
			case *globeMap:
				g = f
			case *mindmap:
				m = f
			}
		}
		core{r.h}.TogglePanel(globeMapPanelID)
		return true
	})
	r.s.WaitForText(t, "kb · 2 documents, 1 links")
	before := onLoop(r, func() *globeView { return g.view })
	builds := onLoop(r, func() uint64 { return m.seq })
	onLoop(r, func() bool { r.h.useTheme("light"); return true })
	r.s.WaitFor(t, "the light theme, a new GlobeMap view", func(string) bool {
		return onLoop(r, func() bool {
			return r.h.theme == "light" && g.view != nil && g.view != before && g.view.pal.background != before.pal.background
		})
	})
	if !r.panelIsOpen(globeMapPanelID) || onLoop(r, func() bool { return g.counts[0] != 2 }) {
		t.Fatal("the theme switch closed the GlobeMap or lost its map")
	}
	if onLoop(r, func() uint64 { return m.seq }) != builds {
		t.Error("a closed Mindmap laid itself out on a theme switch")
	}
}

// windowed runs the host as the GUI runs it (Options.GUI: the program is a window) on d's socket,
// and waits for the workspace's files to be listed.
func windowed(t *testing.T, d *daemon) *running {
	t.Helper()
	r := runTUI(t, NewSession(d.sock, nil), Options{GUI: true})
	r.s.WaitForText(t, "connected — autodoc v-test")
	r.s.WaitFor(t, "the notes listed", func(string) bool { return len(r.listed()) > 0 })
	return r
}

// The GUI build run in a terminal (no --gui) is a terminal: its document names neither view, it
// has neither feature nor command, and Relations › Links is its only item. Run as a window, the
// same build has all three.
func TestTheRelationViewsAreTheWindowsAlone(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "[[b]]\n", "b.md", "bbb\n"}})
	for _, gui := range []bool{false, true} {
		r := runTUI(t, NewSession(d.sock, nil), Options{GUI: gui})
		r.s.WaitForText(t, "connected — autodoc v-test")
		doc, feats, cmds := onLoop(r, func() string { return string(r.h.layoutSrc) }), onLoop(r, func() int { return len(r.h.relationViews) }),
			onLoop(r, func() []string {
				var out []string
				for _, c := range []string{"App.toggleGlobeMap", "App.toggleMindmap", "App.toggleRelations"} {
					if _, ok := r.h.reg.handlers[c]; ok {
						out = append(out, c)
					}
				}
				return out
			})
		views := strings.Contains(doc, "&GlobeMap") || strings.Contains(doc, "GlobeMap {") || strings.Contains(doc, "Mindmap {")
		switch {
		case !strings.Contains(doc, `MenuItem { text: "&Links  (SPC l)"`):
			t.Errorf("gui %v: Relations › Links is gone", gui)
		case gui != views:
			t.Errorf("gui %v: the document has the relation views: %v", gui, views)
		case gui && (feats != 2 || len(cmds) != 3), !gui && (feats != 0 || len(cmds) != 1):
			t.Errorf("gui %v: %d relation view features, commands %v", gui, feats, cmds)
		}
	}
}

// A GlobeMap drag holds the pointer: it ends on its release wherever that happens, and when the
// pointer is lost (the window's focus gone, the panel closed), so a pointer coming back with no
// button held hovers instead of panning. Every event goes through the App's own routing.
func TestTheGlobeMapDragEndsWhereverItIsReleased(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "[[b]]\n", "b.md", "bbb\n"}})
	r := windowed(t, d)
	var g *globeMap
	onLoop(r, func() bool {
		g = r.h.relationViews[0].(*globeMap)
		core{r.h}.TogglePanel(globeMapPanelID)
		return true
	})
	r.s.WaitForText(t, "kb · 2 documents, 1 links")
	const inX, inY, outX, outY = 50, 15, 0, 29 // the panel's middle; the status line, outside it
	mouse := func(k tuicore.MouseKind, b tuicore.MouseButton, x, y int) tuicore.Event {
		return tuicore.MouseEvent{Kind: k, Button: b, X: x, Y: y}
	}
	dragging := func() bool { return onLoop(r, func() bool { return g.drag != nil }) }
	press := func(what string) {
		t.Helper()
		r.keys(t, mouse(tuicore.MousePress, tuicore.MouseLeft, inX, inY), mouse(tuicore.MouseMotion, tuicore.MouseLeft, inX+3, inY+1))
		r.s.WaitFor(t, what+": the press began a drag", func(string) bool { return dragging() })
	}
	ended := func(what string) {
		t.Helper()
		r.s.WaitFor(t, what+": the drag ended", func(string) bool { return !dragging() })
		r.keys(t, mouse(tuicore.MouseMotion, tuicore.MouseNone, inX+1, inY)) // back over the view, no button held
		if dragging() {
			t.Fatalf("%s: a motion with no button held found a drag", what)
		}
	}

	press("released inside")
	r.keys(t, mouse(tuicore.MouseRelease, tuicore.MouseLeft, inX+3, inY+1))
	ended("released inside")

	press("released outside")
	r.keys(t, mouse(tuicore.MouseMotion, tuicore.MouseLeft, outX, outY), mouse(tuicore.MouseRelease, tuicore.MouseLeft, outX, outY))
	ended("released outside")

	press("the window's focus lost")
	r.keys(t, tuicore.FocusEvent{Gained: false, Terminal: true})
	r.keys(t, tuicore.FocusEvent{Gained: true, Terminal: true})
	ended("the window's focus lost")

	press("the panel closed")
	onLoop(r, func() bool { core{r.h}.TogglePanel(globeMapPanelID); return true })
	r.s.WaitFor(t, "the GlobeMap closed", func(string) bool { return !r.panelIsOpen(globeMapPanelID) })
	onLoop(r, func() bool { core{r.h}.TogglePanel(globeMapPanelID); return true })
	r.s.WaitFor(t, "the GlobeMap open again", func(string) bool { return r.panelIsOpen(globeMapPanelID) })
	ended("the panel closed")
}

func sameFrame(a, b *globe) bool { return slices.Equal(a.at, b.at) }

// asyncCore runs a load off the test goroutine, as the host does, and queues what it posts for the
// test to run as the loop.
type asyncCore struct {
	*scriptedCore
	posted chan func()
}

func (a *asyncCore) Go(fn func(context.Context) any, then func(any)) {
	go func() {
		v := fn(context.Background())
		a.posted <- func() { then(v) }
	}()
}
func (a *asyncCore) Post(fn func()) { a.posted <- fn }

// run runs what the loads post, as the loop does, until done says stop or nothing more comes.
func (a *asyncCore) run(t *testing.T, done func() bool) {
	t.Helper()
	for !done() {
		select {
		case fn := <-a.posted:
			fn()
		case <-time.After(10 * time.Second):
			t.Fatal("the load posted nothing more")
		}
	}
}

// closeHost is a panel's host as its Closed hook uses it.
type closeHost struct {
	PanelHost
	restored []string
}

func (*closeHost) Keep(error) {}
func (c *closeHost) RestoreKeyboardPane(id string) error {
	c.restored = append(c.restored, id)
	return nil
}
