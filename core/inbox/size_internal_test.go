package inbox

import (
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/core/mail"
)

// TestTheLargestEntryFitsAPage: every field an entry carries is bounded at admission, so the
// largest entry admission allows, measured by the encoder the RPC writes with, fits one page with
// its envelope, and a page that must carry at least one entry never passes the transport.
func TestTheLargestEntryFitsAPage(t *testing.T) {
	long := func(n int) string { return strings.Repeat("x", n) }
	id := long(64) // ids, tokens and timestamps the daemon makes are far shorter
	item := Item{Item: mail.Item{
		Message: mail.Message{ID: id, Sender: id, SenderSession: id, Kind: mail.KindRequest, Subject: long(mail.MaxSubject),
			Body: long(mail.MaxBody), ReplyTo: id, State: mail.MessageOpen, Created: 1, Expires: new(int64)},
		Delivery: mail.Delivery{ID: id, Seq: 1 << 62, State: mail.Queued, Why: long(64)},
	}, Codename: long(crew.MaxName), Scope: long(crew.MaxIdent)}
	attention := Attention{Attention: crew.Attention{Session: id, Owner: long(crew.MaxIdent), Kind: crew.AttentionPermission,
		Text: long(crew.MaxAttentionText), Raised: 1}, Codename: long(crew.MaxName), Scope: long(crew.MaxIdent)}
	for name, size := range map[string]int{"item": wireSize(toWireItem(item)), "attention": wireSize(toWireAttention(attention))} {
		if size < mail.MaxBody || envelopeBytes+size > pageBytes {
			t.Errorf("the largest %s encodes to %d bytes: want at least its text, and with the envelope within a page's %d",
				name, size, pageBytes)
		}
	}
}
