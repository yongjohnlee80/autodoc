package paths

import (
	"strings"

	"github.com/yongjohnlee80/golib/errs"
	gpaths "github.com/yongjohnlee80/golib/paths"
)

// ErrNotName is an element Name does not write on this OS: not a name from data, or one cut and
// hashed, too long to keep whole.
var ErrNotName = errs.Sentinel(errs.ErrInvalidArgument, "paths: not an element Name writes")

// NAMES FROM DATA. A name from data (a crew scope, a codename) becomes one file or folder name only
// through Name, and is read back only through ParseName. The bytes it escapes are golib paths.Encode's:
// every byte but a lowercase letter, a digit, '.', '_' and '-', so two names differing only by case
// stay apart on a case-blind disk, a separator or a name Windows reserves never reaches the file
// system, and a name too long for an element is cut and hashed. Each escape is written '@' and two
// uppercase hex digits, never '%': a program that percent-decodes its working directory (as opencode
// does) or reads a name again through cmd.exe (a .cmd shim, where %3 is an argument) finds the name
// as it is. Per OS, through this one function:
//
//   - On Linux and macOS a ':' is kept as it is: "autodoc:1f2e…".
//   - On Windows, which cannot hold one, it is escaped: "autodoc@3A1f2e…".
//
// Encode never writes an '@' of its own (it escapes one as %40), so writing its '%' as '@' is one to
// one, and ParseName accepts only what Name writes on that OS: two names never share an element.

// nameMark is a name's escape mark, in place of Encode's '%'; nameHash and nameEmpty are Encode's
// "%~" (a long name's hash follows) and "%_" (the empty name) in it.
const (
	nameMark  = "@"
	nameHash  = "@@"
	nameEmpty = "@_"
	colonEsc  = "@3A"
)

var (
	toName   = strings.NewReplacer("%~", nameHash, "%_", nameEmpty, "%", nameMark)
	fromName = strings.NewReplacer(nameHash, "%~", nameEmpty, "%_", nameMark, "%")
)

// Name is name as one path element on this OS (see NAMES FROM DATA).
func Name(name string) string { return NameFor(name, win) }

// ParseName reverses Name on this OS: ErrNotName for an element Name does not write here, and for a
// long name Name cut and hashed.
func ParseName(element string) (string, error) { return ParseNameFor(element, win) }

// NameFor is Name by Windows' rule (windows) or Linux's and macOS's.
func NameFor(name string, windows bool) string {
	e := toName.Replace(gpaths.Encode(name))
	if !windows {
		e = strings.ReplaceAll(e, colonEsc, ":")
	}
	return e
}

// ParseNameFor is ParseName by Windows' rule (windows) or Linux's and macOS's.
func ParseNameFor(element string, windows bool) (string, error) {
	if strings.Contains(element, "%") {
		return "", ErrNotName
	}
	e := element
	if !windows {
		if strings.Contains(e, colonEsc) {
			return "", ErrNotName // a colon is written as itself here
		}
		e = strings.ReplaceAll(e, ":", colonEsc)
	}
	name, err := gpaths.Decode(fromName.Replace(e))
	if err != nil || NameFor(name, windows) != element {
		return "", ErrNotName
	}
	return name, nil
}

// ParseOldName reads an element an earlier build wrote for a name from data: golib paths.Encode's
// own form ("autodoc%3A1f2e…"), which this build writes as Name does instead. It is for renaming
// such an element once; ok is false for an element that is not in that form.
func ParseOldName(element string) (name string, ok bool) {
	if !strings.Contains(element, "%") {
		return "", false
	}
	name, err := gpaths.Decode(element)
	return name, err == nil
}
