package index

import (
	"context"

	"github.com/yongjohnlee80/autodoc/core/kind"
	"github.com/yongjohnlee80/autodoc/core/store"
)

// ResolveLink is the document that raw, one link as written in the file at from, reaches now: the
// extraction and the resolution indexing uses, so an editor following a link it has just typed
// lands where the index will. path is "" when it reaches none: reason is then ReasonMissing or
// ReasonAmbiguous, or "" when raw is no link the index keeps (a URL, a heading of from itself, an
// attachment the workspace does not index).
func (x *Indexer) ResolveLink(ctx context.Context, from, raw string) (path, reason string, err error) {
	links := extractLinks(kind.ParseMarkdown([]byte(raw)), from, x.opts.Match, x.mode(from).Vars, x.opts.Root)
	if len(links) != 1 {
		return "", "", nil
	}
	l := links[0]
	err = x.store.read(ctx, func(tx *store.Tx) error {
		dst, why, err := x.store.resolve(tx, l.Kind, l.Name)
		if err != nil || dst == 0 {
			reason = why
			return err
		}
		d, err := x.store.documents(tx).With(DocID, dst).Get(DocPath)
		if err != nil {
			return err
		}
		path = d.Path
		return nil
	})
	return path, reason, err
}
