package app

import (
	"bytes"
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
// plugin can read it, or write it into a KB.
func kbOperations(out io.Writer, version string) {
	v := kbtype.Vars{Date: time.Now().Format(time.DateOnly), AutoDocVersion: version}
	fmt.Fprintf(out, "%s", v.Operations(autodoc.KBOperations))
}

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
		for _, name := range pages {
			b, _ := fs.ReadFile(docs.Tutorials, name)
			first, _, _ := bytes.Cut(b, []byte("\n"))
			num, _, _ := strings.Cut(path.Base(name), "-")
			fmt.Fprintf(out, "%s  %s\n", num, strings.TrimPrefix(string(first), "# "))
		}
		fmt.Fprintln(out, "\nautodoc --tutorial N prints page N.")
		return nil
	}
	n, err := strconv.Atoi(page)
	if err != nil || n < 1 {
		return fmt.Errorf("--tutorial takes a page's number, 1 to %d, not %q", len(pages), page)
	}
	for _, name := range pages {
		if num, _, _ := strings.Cut(path.Base(name), "-"); num == fmt.Sprintf("%02d", n) {
			b, err := fs.ReadFile(docs.Tutorials, name)
			if err != nil {
				return err
			}
			_, err = out.Write(b)
			return err
		}
	}
	return fmt.Errorf("the tutorial has no page %d: its pages are 1 to %d", n, len(pages))
}
