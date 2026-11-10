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

// legacyNative is s as Windows PowerShell 5.1 must be given it so a program it starts reads s: that
// PowerShell puts an argument holding a space in double quotes but writes a " inside as it is, so a
// " is given as \" with the backslashes before it doubled, and, where the argument will be quoted,
// a last run of backslashes is doubled so the closing quote isn't read as escaped.
func legacyNative(s string) string {
	var b strings.Builder
	slashes := 0
	for _, r := range s {
		switch r {
		case '\\':
			slashes++
			continue
		case '"':
			b.WriteString(strings.Repeat(`\`, 2*slashes+1))
		default:
			b.WriteString(strings.Repeat(`\`, slashes))
		}
		slashes = 0
		b.WriteRune(r)
	}
	if strings.ContainsAny(s, " \t") {
		slashes *= 2
	}
	b.WriteString(strings.Repeat(`\`, slashes))
	return b.String()
}
