package plugin

import (
	"errors"
	"fmt"
	"unicode/utf8"
)

// PROTOCOL 11 (ADR 1792991184): a plugin's editor menu rows.
//
//	plugin → host   host.menu {id, title?, enabled?}
//
// A manifest's [[menu]] declares the rows: each runs one of the plugin's [[commands]], and its id is
// that command's. While the plugin runs it may change a row's title, or disable it, from the next
// time the menu opens; the host keeps the latest and forgets them when the plugin stops. The host
// never asks the plugin as the menu opens.

// MethodMenu sets one of the plugin's menu rows (protocol 11).
const MethodMenu = "host.menu"

// MaxMenuTitleBytes bounds a row's title, in bytes of UTF-8.
const MaxMenuTitleBytes = 256

// MenuUpdate is host.menu's: a row's id, and its new title or enabled, each nil to leave as it is.
type MenuUpdate struct {
	ID      string
	Title   *string
	Enabled *bool
}

// ErrMenu is a host.menu the protocol refuses: CheckMenu and ReadMenu say which rule.
var ErrMenu = errors.New("plugin: a menu row update refused")

// ErrNoMenu is a menu row update sent to a host speaking a protocol before 11.
var ErrNoMenu = errors.New("plugin: the host speaks a protocol before 11, without menu rows")

// CheckMenu is nil when u is an update the protocol takes.
func CheckMenu(u MenuUpdate) error {
	switch {
	case u.ID == "":
		return fmt.Errorf("%w: no id", ErrMenu)
	case u.Title == nil && u.Enabled == nil:
		return fmt.Errorf("%w: neither a title nor enabled", ErrMenu)
	case u.Title != nil && *u.Title == "":
		return fmt.Errorf("%w: an empty title", ErrMenu)
	case u.Title != nil && len(*u.Title) > MaxMenuTitleBytes:
		return fmt.Errorf("%w: a title of %d bytes, at most %d", ErrMenu, len(*u.Title), MaxMenuTitleBytes)
	case u.Title != nil && !utf8.ValidString(*u.Title):
		return fmt.Errorf("%w: a title that is not UTF-8", ErrMenu)
	}
	return nil
}

// MenuParams are host.menu's.
func MenuParams(u MenuUpdate) []any {
	m := map[string]any{"id": u.ID}
	if u.Title != nil {
		m["title"] = *u.Title
	}
	if u.Enabled != nil {
		m["enabled"] = *u.Enabled
	}
	return one(m)
}

// ReadMenu reads host.menu: each field of its type (an id string; a title string, or none; an
// enabled bool, or none), then CheckMenu.
func ReadMenu(params []any) (MenuUpdate, error) {
	m, err := arg(params)
	if err != nil {
		return MenuUpdate{}, err
	}
	var u MenuUpdate
	var ok bool
	if u.ID, ok = m["id"].(string); !ok {
		return MenuUpdate{}, fmt.Errorf("%w: id is not a string", ErrMenu)
	}
	if v, has := m["title"]; has {
		t, ok := v.(string)
		if !ok {
			return MenuUpdate{}, fmt.Errorf("%w: title is not a string", ErrMenu)
		}
		u.Title = &t
	}
	if v, has := m["enabled"]; has {
		e, ok := v.(bool)
		if !ok {
			return MenuUpdate{}, fmt.Errorf("%w: enabled is not a bool", ErrMenu)
		}
		u.Enabled = &e
	}
	return u, CheckMenu(u)
}
