package plugin

import (
	"errors"
	"fmt"
	"unicode/utf8"
)

// PROTOCOL 8 (docs/decisions/plugins.md#notifications): a plugin's notifications.
//
//	plugin → host   host.notify {message, id, ongoing}
//
// No id: a toast, kept in the history. An id and ongoing: a toast updated in place under that id,
// until a notice with the id and not ongoing ends it, kept in the history; an id posted ongoing
// again after its end starts again. The host names the id for the plugin (plugin:<name>:<id>), so
// no plugin reaches another's, and ends any the plugin leaves ongoing when it stops.

// MethodNotify posts a notification (protocol 8).
const MethodNotify = "host.notify"

// The bounds of a notice: past any, host.notify is refused and nothing shows.
const (
	MaxNoticeBytes   = 1024 // the message, in bytes of UTF-8
	MaxNoticeIDBytes = 64   // the id: letters, digits, '.', '_' and '-'
	MaxActiveNotices = 16   // ongoing ids one run of a plugin may hold at once
)

// Notice is host.notify's.
type Notice struct {
	Message string
	ID      string
	Ongoing bool
}

// ErrNotice is a notice the protocol refuses: CheckNotice and ReadNotice say which rule. Their
// errors never hold the message.
var ErrNotice = errors.New("plugin: a notice refused")

// ErrNoNotify is a notice sent to a host speaking a protocol before 8.
var ErrNoNotify = errors.New("plugin: the host speaks a protocol before 8, without notifications")

// CheckNotice is nil when n is a notice the protocol takes.
func CheckNotice(n Notice) error {
	switch {
	case n.Message == "":
		return fmt.Errorf("%w: an empty message", ErrNotice)
	case len(n.Message) > MaxNoticeBytes:
		return fmt.Errorf("%w: a message of %d bytes, at most %d", ErrNotice, len(n.Message), MaxNoticeBytes)
	case !utf8.ValidString(n.Message):
		return fmt.Errorf("%w: a message that is not UTF-8", ErrNotice)
	case len(n.ID) > MaxNoticeIDBytes:
		return fmt.Errorf("%w: an id of %d bytes, at most %d", ErrNotice, len(n.ID), MaxNoticeIDBytes)
	case n.Ongoing && n.ID == "":
		return fmt.Errorf("%w: an ongoing notice with no id", ErrNotice)
	}
	for i := 0; i < len(n.ID); i++ {
		if c := n.ID[i]; !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-') {
			return fmt.Errorf("%w: an id with a character other than letters, digits, '.', '_' and '-'", ErrNotice)
		}
	}
	return nil
}

// NotifyParams are host.notify's.
func NotifyParams(n Notice) []any {
	m := map[string]any{"message": n.Message}
	if n.ID != "" {
		m["id"] = n.ID
	}
	if n.Ongoing {
		m["ongoing"] = true
	}
	return one(m)
}

// ReadNotice reads host.notify: each field of its type (a message string; an id string, or none; an
// ongoing bool, or none), then CheckNotice. Its error names the rule broken, never the message.
func ReadNotice(params []any) (Notice, error) {
	m, err := arg(params)
	if err != nil {
		return Notice{}, err
	}
	var n Notice
	var ok bool
	if n.Message, ok = m["message"].(string); !ok {
		return Notice{}, fmt.Errorf("%w: message is not a string", ErrNotice)
	}
	if v, has := m["id"]; has {
		if n.ID, ok = v.(string); !ok {
			return Notice{}, fmt.Errorf("%w: id is not a string", ErrNotice)
		}
	}
	if v, has := m["ongoing"]; has {
		if n.Ongoing, ok = v.(bool); !ok {
			return Notice{}, fmt.Errorf("%w: ongoing is not a bool", ErrNotice)
		}
	}
	return n, CheckNotice(n)
}
