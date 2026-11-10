package paths

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/yongjohnlee80/golib/errs"
)

// Under reports whether p is dir or inside it, by whole segments: "a/bc" is not under "a/b". Every
// relative path is under ".", and every path on a volume is under its root ("/", "C:/",
// "//server/share/"). A relative dir ending in "/" ("docs/") is not a root, and keeps the rule above.
func Under(p, dir string) bool {
	if root, rel := rootOf(dir, win); root != "" && rel == "" {
		return p == dir || strings.HasPrefix(p, root) // a volume's root: already ends in "/"
	}
	return dir == "." || p == dir || strings.HasPrefix(p, dir+"/")
}

// UnderAbs is Under for absolute paths, compared as this OS compares them: on Windows a volume and
// its names without regard to case ("c:/KB/a.md" is under "C:/kb"), elsewhere byte for byte. A
// path that matches is dir's own bytes long up to its rest, so p[len(dir):] is that rest.
func UnderAbs(p, dir string) bool { return underAbs(p, dir, win) }

// UnderAbsFor is UnderAbs under Windows' rule when windows is set, Unix' otherwise.
func UnderAbsFor(p, dir string, windows bool) bool { return underAbs(p, dir, windows) }

// SameAbs reports whether two absolute paths in the slash form name one path, compared as this OS
// compares them: on Windows without regard to case.
func SameAbs(a, b string) bool { return sameAbs(a, b, win) }

func underAbs(p, dir string, win bool) bool {
	if root, rel := rootOf(dir, win); root != "" && rel == "" {
		return sameAbs(p, dir, win) || hasPrefix(p, root, win) // a volume's root: already ends in "/"
	}
	return sameAbs(p, dir, win) || hasPrefix(p, dir+"/", win)
}

func sameAbs(a, b string, win bool) bool {
	if win {
		return len(a) == len(b) && strings.EqualFold(a, b)
	}
	return a == b
}

// hasPrefix is strings.HasPrefix, without regard to case under Windows' rule: byte for byte as long
// as prefix, so what follows it starts at len(prefix).
func hasPrefix(s, prefix string, win bool) bool {
	return len(s) >= len(prefix) && sameAbs(s[:len(prefix)], prefix, win)
}

// THE SLASH FORM. The core keeps an absolute path slash-separated and clean, with its volume kept
// whole: "/srv/kb" on a Unix; on Windows a drive's "C:/Users/u" (its root "C:/", never "C:", which
// names the drive's current directory) and a share's "//server/share/kb" (its root
// "//server/share/"). Every operation on such a path goes through this package, which splits the
// volume off once, applies package path to the rest, and puts the volume back: package path alone
// would make "C:/" "C:" and "//server/share" "/server/share". A path relative to a workspace or a
// store ("open/x.md") has no volume, and package path is enough for it.
//
// Each function follows this OS's rule, Windows' where the separator is a backslash; the rule is a
// parameter inside, so either is testable anywhere.

// win is whether this OS's paths follow Windows' rule: drives, shares and backslashes.
var win = os.PathSeparator == '\\'

// Slash is the OS path p in the slash form. On Windows a verbatim path names what its plain form
// does: `\\?\C:\x` is "C:/x" and `\\?\UNC\server\share\x` is "//server/share/x"; any other device
// path (`\\?\Volume{…}\x`, `\\.\pipe\x`) keeps its whole prefix as its volume. "" stays "": no path
// is not the current directory.
func Slash(p string) string { return slash(p, win) }

// Clean is the slash-form p cleaned, its volume kept: Clean("C:/a/..") is "C:/".
func Clean(p string) string { return clean(p, win) }

// Join is the slash-form abs with elem joined after it, cleaned, its volume kept: Join("C:/",
// ".todo-list") is "C:/.todo-list".
func Join(abs string, elem ...string) string { return join(abs, win, elem...) }

// Dir is the directory holding the slash-form p, its volume kept: Dir("C:/x") is "C:/", and a
// volume's root is its own directory.
func Dir(p string) string { return dir(p, win) }

// Root splits the slash-form p into its volume's root ("/", "C:/", "//server/share/") and the
// path inside it, slash-separated with no leading slash ("" for the root itself). root is "" for a
// path that is not absolute.
func Root(p string) (root, rel string) { return rootOf(p, win) }

// IsRoot reports whether p, in either form, is a volume's root.
func IsRoot(p string) bool {
	root, rel := rootOf(slash(p, win), win)
	return root != "" && rel == ""
}

// Rel is p relative to dir, both absolute in the slash form: "." for dir itself; ok is false when p
// is not under dir (another volume included).
func Rel(dir, p string) (rel string, ok bool) { return relOf(dir, p, win) }

// Windows is whether this OS's paths follow Windows' rule: drives, shares and backslashes. A caller
// that applies the rule to data of its own (and tests it under either rule) passes it to SlashFor
// and IsAbsFor.
var Windows = win

// SlashFor is Slash under Windows' rule when windows is set, Unix' otherwise.
func SlashFor(p string, windows bool) string { return slash(p, windows) }

// IsAbsFor is IsAbs under Windows' rule when windows is set, Unix' otherwise.
func IsAbsFor(p string, windows bool) bool {
	root, _ := rootOf(slash(p, windows), windows)
	return root != ""
}

// IsAbs reports whether p, in either form (the OS's own or the slash form), is an absolute path:
// rooted ("/srv/kb"), or a drive's or a share's on Windows ("C:/Users/u", `C:\Users\u`,
// "//server/share/kb"); "C:u", a drive's current directory, is not. The disk that opens p has the
// last word: a rooted path with no drive is refused there on Windows.
func IsAbs(p string) bool { return IsAbsFor(p, win) }

func slash(p string, win bool) string {
	if p == "" {
		return ""
	}
	if win {
		p = strings.ReplaceAll(p, `\`, "/")
		if rest, ok := cutPrefixFold(p, "//?/UNC/"); ok {
			p = "//" + rest
		} else if rest, ok := strings.CutPrefix(p, "//?/"); ok && isDrive(rest) {
			p = rest
		}
	}
	return clean(p, win)
}

func clean(p string, win bool) string {
	if p == "" {
		return ""
	}
	vol, rest := split(p, win)
	switch {
	case vol == "":
		return path.Clean(p)
	case rest == "" && strings.HasPrefix(vol, "//"):
		return vol + "/" // a share's root
	case rest == "":
		return vol // "C:": the drive's current directory, not its root
	}
	return vol + path.Clean(rest)
}

func join(abs string, win bool, elem ...string) string {
	vol, rest := split(clean(abs, win), win)
	if vol == "" {
		return path.Join(append([]string{abs}, elem...)...)
	}
	r := path.Join(append([]string{rest}, elem...)...)
	if r == "." {
		r = ""
	}
	return clean(vol+r, win)
}

func dir(p string, win bool) string {
	vol, rest := split(clean(p, win), win)
	if vol == "" {
		return path.Dir(p)
	}
	if !strings.HasPrefix(rest, "/") {
		return vol // a drive's current directory: no parent to name
	}
	return vol + path.Dir(rest)
}

func rootOf(p string, win bool) (root, rel string) {
	p = clean(p, win)
	vol, rest := split(p, win)
	switch {
	case vol == "" && strings.HasPrefix(p, "/"):
		return "/", strings.TrimPrefix(p, "/")
	case vol == "", !strings.HasPrefix(rest, "/"):
		return "", p // relative, or relative to a drive's current directory
	}
	return vol + "/", strings.TrimPrefix(rest, "/")
}

func relOf(dir, p string, win bool) (string, bool) {
	dr, drel := rootOf(dir, win)
	pr, prel := rootOf(p, win)
	switch {
	case dr == "" || dr != pr:
		return "", false
	case drel == prel:
		return ".", true
	case drel == "":
		return prel, true
	case strings.HasPrefix(prel, drel+"/"):
		return prel[len(drel)+1:], true
	}
	return "", false
}

// split is p's volume and the rest, p in the slash form: a drive ("C:"), a share
// ("//server/share") or a device path's prefix ("//?/Volume{…}", "//./pipe") on Windows; "" for
// none.
func split(p string, win bool) (vol, rest string) {
	if !win {
		return "", p
	}
	if isDrive(p) {
		return p[:2], p[2:]
	}
	if !strings.HasPrefix(p, "//") {
		return "", p
	}
	if strings.HasPrefix(p, "//?/") || strings.HasPrefix(p, "//./") {
		if i := strings.IndexByte(p[4:], '/'); i >= 0 {
			return p[:4+i], p[4+i:]
		}
		return p, ""
	}
	server, after, ok := strings.Cut(p[2:], "/")
	if !ok || server == "" {
		return "", p // no share: not a volume
	}
	share, _, _ := strings.Cut(after, "/")
	if share == "" {
		return "", p
	}
	n := 2 + len(server) + 1 + len(share)
	return p[:n], p[n:]
}

func isDrive(p string) bool {
	return len(p) >= 2 && p[1] == ':' && (p[0] >= 'a' && p[0] <= 'z' || p[0] >= 'A' && p[0] <= 'Z')
}

func cutPrefixFold(s, prefix string) (string, bool) {
	if len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix) {
		return s[len(prefix):], true
	}
	return s, false
}

// ErrNoHome is a "~" path expanded with no home directory to put in its place.
var ErrNoHome = errs.Sentinel(errs.ErrInvalidArgument, "paths: no home directory")

// ExpandHome is p with a leading "~" or "~/" made the user's home directory. Any other path is
// returned as it is.
func ExpandHome(p string) (string, error) {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("%w: %q needs one", ErrNoHome, p)
	}
	return filepath.Join(home, strings.TrimPrefix(p, "~")), nil
}
