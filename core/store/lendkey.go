package store

import (
	"net/url"
	"strings"

	"github.com/yongjohnlee80/golib/errs"
)

// A STORED KEY STAYS WITH ITS SERVER — an embedding provider's or a ranker's API key is sent only
// to the server it was typed for. An edit may leave the key out to keep it (the forms do, so a key
// never travels back to a client), but an edit that also moves the base URL to another server
// would otherwise send that key to wherever the edit points: anyone allowed to edit the settings,
// a Lua package granted them among others, could read the key off the wire without ever being
// shown it. So moving to another server needs the key typed again, or removed.
//
// "The same server" is the same scheme, host and port, the browser's origin: a key is a
// credential for a server, not for one path on it, so /v1 and /v1/ on one host keep it.

// ErrKeyForNewServer is an edit that moves a keyed provider or ranker to another server without
// typing its key again.
var ErrKeyForNewServer = errs.Sentinel(errs.ErrInvalidArgument,
	"store: the base URL moves to another server: type the API key again, or remove it")

// LendKey is the key an edit of a stored provider or ranker is called with: the typed key when the
// edit has one; else the stored key, while editBase is on the server of storedBase. An edit that
// moves to another server with a stored key and none typed is ErrKeyForNewServer, and no key is
// returned. Every caller that sends a stored key to an edit's endpoint goes through here.
func LendKey(storedBase, stored, editBase string, typed *string) (string, error) {
	if typed != nil {
		return *typed, nil
	}
	if err := keyMayStay(storedBase, editBase, stored != "", nil); err != nil {
		return "", err
	}
	return stored, nil
}

// keyMayStay is LendKey's rule on a key the caller has not opened: hasKey says only whether one is
// stored. A typed key, or no stored key, may go anywhere.
func keyMayStay(storedBase, editBase string, hasKey bool, typed *string) error {
	if typed != nil || !hasKey || sameServer(storedBase, editBase) {
		return nil
	}
	return ErrKeyForNewServer
}

// sameServer reports whether a and b are URLs of one origin: scheme, host and port alike, the
// default port written or not. A URL that does not parse is no server's.
func sameServer(a, b string) bool {
	ua, err := url.Parse(strings.TrimSpace(a))
	if err != nil {
		return false
	}
	ub, err := url.Parse(strings.TrimSpace(b))
	if err != nil {
		return false
	}
	return ua.Host != "" && strings.EqualFold(ua.Scheme, ub.Scheme) &&
		strings.EqualFold(ua.Hostname(), ub.Hostname()) && portOf(ua) == portOf(ub)
}

// portOf is u's port, its scheme's default when it names none.
func portOf(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	switch strings.ToLower(u.Scheme) {
	case "http":
		return "80"
	case "https":
		return "443"
	}
	return ""
}
