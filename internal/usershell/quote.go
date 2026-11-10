package usershell

import "strings"

// quotePOSIX is s in single quotes, each ' written '\”: nothing inside is the shell's.
func quotePOSIX(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// quotePowerShell is s in single quotes, each ' (and its typographic forms, which PowerShell reads
// as quotes too) doubled: a verbatim string, with no $ expansion or subexpression inside.
func quotePowerShell(s string) string {
	var b strings.Builder
	b.WriteByte('\'')
	for _, r := range s {
		switch r {
		case '\'', '\u2018', '\u2019', '\u201a', '\u201b':
			b.WriteRune(r)
		}
		b.WriteRune(r)
	}
	b.WriteByte('\'')
	return b.String()
}

// quoteCmd is s as one argument of a program cmd.exe starts: in double quotes, a " inside doubled
// (""), which the program's own parsing reads as one quote and which leaves cmd's own idea of being
// inside quotes as it was, so & | < > ^ stay inert; each % is written outside the quotes as ^%, the
// one way cmd leaves a %NAME% unexpanded; backslashes before any quote are doubled, so it is never
// read as escaped.
func quoteCmd(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	slashes := 0 // backslashes not yet written: doubled when a quote follows them
	for _, r := range s {
		switch r {
		case '\\':
			slashes++
			continue
		case '"':
			b.WriteString(strings.Repeat(`\`, 2*slashes) + `""`)
		case '%':
			b.WriteString(strings.Repeat(`\`, 2*slashes) + `"^%"`) // out of the quotes, the % escaped, back in
		default:
			b.WriteString(strings.Repeat(`\`, slashes))
			b.WriteRune(r)
		}
		slashes = 0
	}
	b.WriteString(strings.Repeat(`\`, 2*slashes))
	b.WriteByte('"')
	return b.String()
}
