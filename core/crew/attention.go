package crew

import (
	"context"
	"fmt"
)

// MaxAttentionText is the longest text an attention may carry, as long as a mail body: what the
// session waits on is shown in every window's snapshot, which pages within the transport's message.
const MaxAttentionText = 64 << 10

// Attention kinds, and the inbox_change kinds raising and clearing one journal (§4.8).
const (
	AttentionPermission = "permission"
	AttentionBlocked    = "blocked"

	inboxAttention = "attention"
	inboxCleared   = "cleared"
)

// RaiseAttention marks the calling session as waiting on the user (crew.attention raise, §4.8):
// a permission prompt the CLI's adapter saw, or blocked. The host that shows the prompt is the
// session's own. Every window's toast comes from the inbox_change it journals, in the same write.
func (m *Manager) RaiseAttention(ctx context.Context, credential, kind, text string) error {
	if kind != AttentionPermission && kind != AttentionBlocked {
		return fmt.Errorf("%w: attention kind %q", ErrInvalid, kind)
	}
	if len(text) > MaxAttentionText {
		return fmt.Errorf("%w: attention text of %d bytes, at most %d: say what the session waits on, briefly", ErrInvalid, len(text), MaxAttentionText)
	}
	return m.store.Write(ctx, func(tx Tx) error {
		s, err := m.Authenticate(tx, credential)
		if err != nil {
			return err
		}
		if err := tx.PutAttention(Attention{Session: s.ID, Owner: s.Host, Kind: kind, Text: text,
			Raised: tx.Now().UnixNano()}); err != nil {
			return err
		}
		return tx.AddInboxChange(inboxAttention, s.ID)
	})
}

// ClearAttention ends the calling session's wait on the user (crew.attention clear).
func (m *Manager) ClearAttention(ctx context.Context, credential string) error {
	return m.store.Write(ctx, func(tx Tx) error {
		s, err := m.Authenticate(tx, credential)
		if err != nil {
			return err
		}
		return clearAttention(tx, s.ID)
	})
}

// clearAttention clears session's raised attention, if it has one, journaling the change.
func clearAttention(tx Tx, session string) error {
	a, ok, err := tx.Attention(session)
	if err != nil || !ok || a.Cleared != nil {
		return err
	}
	at := tx.Now().UnixNano()
	a.Cleared = &at
	if err := tx.PutAttention(a); err != nil {
		return err
	}
	return tx.AddInboxChange(inboxCleared, session)
}
