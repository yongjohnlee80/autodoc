package tui

import "strings"

// LINE ENDINGS (docs/decisions/editor.md#line-endings). The editor holds a file's text with LF line
// breaks whatever the file has: golib's text model reads CRLF and a lone CR as LF. AutoDoc writes
// LF, on every platform, except to a file that already uses CRLF: that one is written back with
// CRLF, so editing a Windows user's file changes the lines edited, not every line of it.

// usesCRLF reports whether text, a file's bytes as read, uses CRLF line breaks: most of its breaks
// are CRLF. A file with no line break is LF's.
func usesCRLF(text string) bool {
	crlf := strings.Count(text, "\r\n")
	return crlf > 0 && 2*crlf >= strings.Count(text, "\n")
}

// withEndings is content, the editor's LF text, with the line breaks a file written as crlf has.
func withEndings(content string, crlf bool) string {
	if !crlf {
		return content
	}
	return strings.ReplaceAll(content, "\n", "\r\n")
}

// asEditorText is text, a file's bytes as read, with its line breaks as the editor holds them: LF.
func asEditorText(text string) string {
	if !strings.Contains(text, "\r") {
		return text
	}
	return strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
}
