package todo

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"

	"github.com/yongjohnlee80/golib/vfs"

	"github.com/yongjohnlee80/autodoc/core/docs"
)

// A TASK'S FILE IS WRITTEN AT ONE PATH (docs/decisions/todos.md#a-tasks-file-is-written-at-one-path).
// The daemon moves a task's file between buckets through the store's own documents; a client that
// writes a store's file goes through doc.write or file.write, whose create of a task file is
// checked here: a second file of a task that is at another path is refused.

// Elsewhere is the task that a file created at rel, in the store fsys holds, with content would be
// a second file of, and where it is: rel is a task file's place (a bucket's, or an archive
// partition's), and content's frontmatter id is, byte for byte, that of a task at another path. at
// is "" for none: a file that is no task's, an id no task has, or the task's own path.
func Elsewhere(ctx context.Context, fsys vfs.FS, rel string, content []byte) (id, at string, err error) {
	if !taskPlace(rel) {
		return "", "", nil
	}
	id = idOf(content)
	if !safeID(id) {
		return "", "", nil // no task's id: a malformed file is the refresh's to report
	}
	st := &store{fsys: fsys, docs: docs.New(fsys, eligible)}
	p, err := st.find(ctx, id)
	if errors.Is(err, ErrNotFound) || p == rel {
		return "", "", nil
	}
	if err != nil {
		return "", "", err
	}
	// found by its file's name: the file's own id says whose it is
	doc, err := st.docs.Read(ctx, p)
	if errors.Is(err, vfs.ErrNotExist) {
		return "", "", nil
	}
	if err != nil {
		return "", "", err
	}
	if idOf(doc.Content) != id {
		return "", "", nil
	}
	return id, p, nil
}

// MayBeTaskFile reports whether the absolute path abs ends as a task file's place in a store does:
// the cheap test before a store is looked for.
func MayBeTaskFile(abs string) bool {
	parts := strings.Split(filepath.ToSlash(abs), "/")
	n := len(parts)
	return n >= 2 && taskPlace(strings.Join(parts[n-2:], "/")) || n >= 4 && taskPlace(strings.Join(parts[n-4:], "/"))
}

// taskPlace reports a path a task file lies at in a store: <bucket>/<name>.md, or
// archived/<year>/<month>/<name>.md.
func taskPlace(p string) bool {
	if !strings.HasSuffix(p, ".md") {
		return false
	}
	parts := strings.Split(p, "/")
	switch len(parts) {
	case 2:
		return slices.Contains(activeBuckets, parts[0])
	case 4:
		return parts[0] == "archived" && yearRe.MatchString(parts[1]) && monthRe.MatchString(parts[2])
	}
	return false
}

// idOf is the id a file's frontmatter gives, whatever else in it the format would refuse; "" for
// none.
func idOf(content []byte) string {
	fm, _, err := splitFrontmatter(string(content))
	if err != nil {
		return ""
	}
	root, err := decodeYAML(fm)
	if err != nil || root == nil {
		return ""
	}
	if n := root.get("id"); n != nil && (n.kind == nString || n.kind == nNumber) {
		return n.str
	}
	return ""
}
