package app

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/yongjohnlee80/autodoc"
	"github.com/yongjohnlee80/autodoc/core/kbtype"
	"github.com/yongjohnlee80/autodoc/docs"
)

// kbOperations is --kb-operations: the KB operations manual this build carries, as a KB's scaffold
// writes it (stamped with this build's version and today's date), so an install without the Neovim
// plugin can read it, or write it into a KB. A failed write is its error: a redirect into a KB
// that fails must not look like a written manual.
func kbOperations(out io.Writer, version string) error {
	v := kbtype.Vars{Date: time.Now().Format(time.DateOnly), AutoDocVersion: version}
	_, err := out.Write(v.Operations(autodoc.KBOperations))
	return err
}

// errNoPage is --tutorial's argument naming no page of the tutorial: a usage error (exit 2).
var errNoPage = errors.New("no such tutorial page")

// tutorialPages are the English tutorial's pages, in order (tutorials/NN-name.md).
func tutorialPages() []string {
	names, _ := fs.Glob(docs.Tutorials, "tutorials/[0-9]*.md") // the pattern is valid
	return names
}

// tutorial is --tutorial: with no page, the tutorial's pages, one line each (its number and its
// heading); with a page's number, that page, as Help › Tutorial shows it in English.
func tutorial(out io.Writer, page string) error {
	pages := tutorialPages()
	if page == "" {
		var list bytes.Buffer
		for _, name := range pages {
			b, _ := fs.ReadFile(docs.Tutorials, name) // a name the embedded FS's own glob gave
			first, _, _ := bytes.Cut(b, []byte("\n"))
			num, _, _ := strings.Cut(path.Base(name), "-")
			fmt.Fprintf(&list, "%s  %s\n", num, strings.TrimPrefix(string(first), "# "))
		}
		list.WriteString("\nautodoc --tutorial N prints page N.\n")
		_, err := out.Write(list.Bytes())
		return err
	}
	n, err := strconv.Atoi(page)
	if err != nil || n < 1 {
		return fmt.Errorf("%w: --tutorial takes a page's number, 1 to %d, not %q", errNoPage, len(pages), page)
	}
	for _, name := range pages {
		if num, _, _ := strings.Cut(path.Base(name), "-"); num == fmt.Sprintf("%02d", n) {
			b, _ := fs.ReadFile(docs.Tutorials, name) // a name the embedded FS's own glob gave
			_, err := out.Write(b)
			return err
		}
	}
	return fmt.Errorf("%w: the tutorial has no page %d: its pages are 1 to %d", errNoPage, n, len(pages))
}
