//go:build gui

package tui

import (
	"context"
	"image/color"
	"math/rand/v2"
	"os"
	"slices"
	"time"

	"github.com/yongjohnlee80/autodoc/rpc"
)

// THE VIEWS' GRAPH FILES — one graphStore for the GlobeMap and the Mindmap: each workspace's graph
// file (graphconf.go) read once through the daemon (its root is on the daemon's disk), the user's
// beside the config; a view's groups initialised the first time it asks for them and the file
// written; the views' settings writing their edits. All on the loop: its reads and writes run off
// it, and hand back on it.

type graphStore struct {
	c        Core
	files    absoluteFiles // the daemon's files by path (core's)
	userFile string        // graph.toml beside the config; "" for none
	byWS     map[string]*graphWS
	rnd      *rand.Rand
}

// graphWS is one workspace's graph file, as read and written.
type graphWS struct {
	file    string
	conf    graphConf
	version string // as last read or written; "" while there is no file
	user    graphConf
	read    bool
	waiting []func(*graphWS) // asked while the files are read
	writing bool             // a write is under way: one write at a time, each from the last version
	again   bool             // changed while it was: written once more after it
}

// absoluteFiles reads and writes a file on the daemon's disk by its absolute path (core's
// ReadAbsolute and WriteAbsolute: the host's readFile and writeFile).
type absoluteFiles interface {
	ReadAbsolute(ctx context.Context, path string) ([]byte, string, error)
	WriteAbsolute(ctx context.Context, path string, content []byte, version string) (string, error)
}

func newGraphStore(c Core, files absoluteFiles, userFile string) *graphStore {
	now := uint64(time.Now().UnixNano())
	return &graphStore{c: c, files: files, userFile: userFile, byWS: map[string]*graphWS{}, rnd: rand.New(rand.NewPCG(now, now>>17|1))}
}

// with runs fn with ws's graph file once it is read: at once when it has been. Nothing for a
// workspace the daemon does not list.
func (s *graphStore) with(ws string, fn func(*graphWS)) {
	g := s.byWS[ws]
	if g == nil {
		i := slices.IndexFunc(s.c.Workspaces(), func(w Workspace) bool { return w.Name == ws })
		if i < 0 || s.c.Workspaces()[i].Root == "" {
			return
		}
		g = &graphWS{file: graphFile(s.c.Workspaces()[i].Root)}
		s.byWS[ws] = g
	}
	if g.read {
		fn(g)
		return
	}
	g.waiting = append(g.waiting, fn)
	if len(g.waiting) > 1 {
		return // being read
	}
	type readBack struct {
		conf, user graphConf
		version    string
		err        error
	}
	file, userFile := g.file, s.userFile
	s.c.Go(func(ctx context.Context) any {
		var r readBack
		b, version, err := s.files.ReadAbsolute(ctx, file)
		switch {
		case err == nil:
			r.version = version
			r.conf, r.err = parseGraphConf(b)
		case code(err) != rpc.CodeNotFound: // none yet is no groups
			r.err = err
		}
		if userFile != "" {
			if b, err := os.ReadFile(userFile); err == nil {
				if u, err := parseGraphConf(b); err == nil {
					r.user = u
				} else if r.err == nil {
					r.err = err
				}
			}
		}
		return r
	}, func(v any) {
		r := v.(readBack)
		if r.err != nil {
			s.c.Failed(s.c.Trf("autodoc.notice.graphstore_gui.the_graph_groups", file), r.err) // the views go on with what read
		}
		g.conf, g.user, g.version, g.read = r.conf, r.user, r.version, true
		waiting := g.waiting
		g.waiting = nil
		for _, fn := range waiting {
			fn(g)
		}
	})
}

// groups hands done view's groups in ws: the file's, or, with none, its first groups (the user's,
// then one a type of types and one a folder of the workspace's files), each a colour legible on
// ground, written into the file.
func (s *graphStore) groups(ws, view string, types []string, ground color.NRGBA, done func([]relGroup)) {
	s.with(ws, func(g *graphWS) {
		if list := g.conf.groups(view); list != nil {
			done(list)
			return
		}
		list := initialGroups(view, g.conf, g.user, groupQueries(types, s.c.Files().Paths), ground, s.rnd)
		if len(list) == 0 {
			done(nil) // no schema types and no folders: nothing to write
			return
		}
		s.save(ws, view, list)
		done(list)
	})
}

// addMissing hands done view's groups in ws with a group added for each type of types and each
// folder no group of them queries, written into the file.
func (s *graphStore) addMissing(ws, view string, current []relGroup, types []string, ground color.NRGBA, done func([]relGroup)) {
	s.with(ws, func(g *graphWS) {
		have := map[string]bool{}
		for _, x := range current {
			have[x.Query] = true
		}
		var missing []string
		for _, q := range groupQueries(types, s.c.Files().Paths) {
			if !have[q] {
				missing = append(missing, q)
			}
		}
		pal := g.conf.palette()
		if len(pal) == 0 {
			pal = g.user.palette()
		}
		list := append(slices.Clone(current), initGroups(missing, pal, ground, s.rnd)...)
		s.save(ws, view, list)
		done(list)
	})
}

// save writes list as view's groups in ws's file, the palette in with them when the file names
// none, so the file says what a new group takes its colour from.
func (s *graphStore) save(ws, view string, list []relGroup) {
	s.with(ws, func(g *graphWS) {
		g.conf.setGroups(view, list)
		if len(g.conf.Palette) == 0 {
			g.conf.Palette = g.user.Palette
			if len(g.conf.Palette) == 0 {
				for _, c := range groupPalette {
					g.conf.Palette = append(g.conf.Palette, hexOf(c))
				}
			}
		}
		s.write(g)
	})
}

// write puts g's groups in its file: after the write under way, when there is one, so each write
// carries the version the one before it left.
func (s *graphStore) write(g *graphWS) {
	if g.writing {
		g.again = true
		return
	}
	g.writing = true
	content, file, version := []byte(g.conf.format()), g.file, g.version
	s.c.Go(func(ctx context.Context) any {
		v, err := s.files.WriteAbsolute(ctx, file, content, version)
		if err != nil {
			return err
		}
		return v
	}, func(v any) {
		g.writing = false
		switch v := v.(type) {
		case string:
			g.version = v
		case error:
			s.c.Failed(s.c.Trf("autodoc.notice.graphstore_gui.save_the_graph_groups", file), v)
			if code(v) == rpc.CodeConflict { // changed on disk: read again before another write
				g.read, g.again = false, false
				return
			}
		}
		if g.again {
			g.again = false
			s.write(g)
		}
	})
}
