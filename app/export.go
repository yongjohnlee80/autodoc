package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/yongjohnlee80/autodoc/core/export"
	"github.com/yongjohnlee80/autodoc/internal/localfs"
)

// exportMain writes sourcePath exported to destination; its relative links are read from base, or
// from the source's own directory when base is "", so the page reaches them wherever it is written.
func exportMain(sourcePath, format, destination, theme, base string) error {
	if sourcePath == "" || destination == "" {
		return errors.New("--export needs a Markdown source and --output destination")
	}
	if strings.ToLower(filepath.Ext(sourcePath)) != ".md" {
		return errors.New("--export currently accepts Markdown (.md) sources")
	}
	sourceInfo, err := os.Stat(sourcePath)
	if err != nil {
		return err
	}
	if destinationInfo, err := os.Stat(destination); err == nil && os.SameFile(sourceInfo, destinationInfo) {
		return errors.New("export destination is the source file")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if base == "" {
		base = filepath.Dir(sourcePath)
	} else if baseInfo, err := os.Stat(base); err != nil {
		return fmt.Errorf("--base: %w", err)
	} else if !baseInfo.IsDir() {
		return fmt.Errorf("--base %s is not a directory", base)
	}
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		return err
	}
	content, err := export.RenderWith(source, export.Format(format), theme, export.Options{Base: base, Document: sourcePath, Chart: chartSVG})
	if err != nil {
		return fmt.Errorf("%s: %w", sourcePath, err)
	}
	return localfs.WriteFile(context.Background(), destination, content)
}

// siteMain builds the site docs/site.toml describes into out
// (docs/decisions/editor.md#the-documentation-site): theme, when given, over the site's own. out is
// a new or empty folder, never inside docs, as its symlinks resolve: a page left from an earlier
// build would still be published, and a site written into docs would be read as docs.
func siteMain(docs, out, theme, base string) error {
	if base != "" {
		return errors.New("--base does not go with --export=site: a site's links are to its own pages")
	}
	if docs == "" || out == "" {
		return errors.New("--export=site needs a docs folder and an --output folder")
	}
	info, err := os.Stat(docs)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("--export=site: %s is not a folder", docs)
	}
	absDocs, err := filepath.Abs(docs)
	if err != nil {
		return err
	}
	absOut, err := filepath.Abs(out)
	if err != nil {
		return err
	}
	if r, err := filepath.Rel(localfs.Resolve(absDocs), localfs.Resolve(absOut)); err == nil && (r == "." || filepath.IsLocal(r)) {
		return fmt.Errorf("--output %s is inside the docs folder %s", out, docs)
	}
	if entries, err := os.ReadDir(absOut); err == nil && len(entries) > 0 {
		return fmt.Errorf("--output %s is not empty: a site is built into a new or empty folder", out)
	}
	return export.Site(absDocs, export.SiteOptions{Theme: theme, Chart: chartSVG}, func(rel string, page []byte) error {
		path := filepath.Join(absOut, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		return localfs.WriteFile(context.Background(), path, page)
	})
}
