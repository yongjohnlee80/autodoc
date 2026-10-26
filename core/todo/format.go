package todo

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// The on-disk format is schema v1: YAML frontmatter, the managed header comment, an H1 of the
// title and the description. Encode renders it field for field as the format's first writer did,
// so a file written before AutoDoc owned the store and one AutoDoc writes differ only in the
// header's wording, which a read accepts either way.

// header is the managed comment block written above the H1.
const header = "<!-- ─── autodoc.todo schema v1 — managed file ───\n" +
	"     HAND-EDIT FREELY in this body (the description) or the\n" +
	"       hand-editable frontmatter fields: title, status, due,\n" +
	"       priority, assignee, tags, adr, review, blocked.\n" +
	"       Direct `status` edits to a valid enum value are honored —\n" +
	"       refresh reconciles the file's directory. BUT AutoDoc logs\n" +
	"       an edit made outside it only as an update, when it next\n" +
	"       reads the store: a move or an assignment is announced as\n" +
	"       one only through AutoDoc (its todo view, or `autodoc\n" +
	"       --call todos.status` / `todos.assign`). Prefer that when\n" +
	"       others need to know.\n" +
	"     DO NOT HAND-EDIT frontmatter: id, version, created, updated,\n" +
	"       status_changed, completed_at, archived_at, errors.\n" +
	"         • id + created are frozen at creation.\n" +
	"         • version / updated / lifecycle timestamps / errors are\n" +
	"           written by refresh + API calls. Manual edits silently\n" +
	"           break archive timing and cross-references.\n" +
	"     ─────────────────────────────────────────────── -->"

// Statuses, in bucket scan order. Each is also its bucket directory's name.
var Statuses = []string{"open", "in-progress", "deferred", "completed", "archived"}

// activeBuckets are the flat buckets, in auto-core's scan order; archived/ is partitioned by month.
var activeBuckets = []string{"open", "in-progress", "deferred", "completed"}

func validStatus(s string) bool { return slices.Contains(Statuses, s) }

// automationRemoved is the reason a file using removed automation is malformed.
const automationRemoved = "automation was removed (ADR 1791532020)"

var removedFields = map[string]bool{"condition": true, "execute": true, "origin": true, "last_fired_at": true, "exit_code": true}

var (
	// headerRe is the managed header as either writer worded it: AutoDoc's, or the one files written
	// before AutoDoc owned the store carry. A read strips either; a write writes AutoDoc's.
	headerRe = regexp.MustCompile(`(?s)<!-- ─── (?:autodoc|auto-core)\.todo schema v1.*?─── -->[ \t\n\v\f\r]*\n?`)
	h1Re     = regexp.MustCompile(`^[ \t\n\v\f\r]*#[ \t\n\v\f\r]+[^\n]*\n?`)
)

// splitFrontmatter is md.lua's split_frontmatter: the text between the opening `---` and the
// first `---` line after it, and everything after that line.
func splitFrontmatter(src string) (fm, body string, err error) {
	if !strings.HasPrefix(src, "---\n") && !strings.HasPrefix(src, "---\r") {
		return "", "", errors.New("missing opening `---` frontmatter delimiter")
	}
	for _, delim := range []string{"\n---\n", "\n---\r\n"} {
		if i := strings.Index(src[3:], delim); i >= 0 {
			i += 3
			return src[4:i], src[i+len(delim):], nil
		}
	}
	if strings.HasSuffix(src, "\n---") && len(src) >= 7 {
		return src[4 : len(src)-4], "", nil
	}
	return "", "", errors.New("missing closing `---` frontmatter delimiter")
}

// cleanBody is md.lua's clean_body: the description is the body without the managed header, a
// leading H1, and the blank lines around it.
func cleanBody(body string) string {
	if loc := headerRe.FindStringIndex(body); loc != nil {
		body = body[:loc[0]] + body[loc[1]:]
	}
	if loc := h1Re.FindStringIndex(body); loc != nil {
		body = body[loc[1]:]
	}
	return strings.TrimRight(strings.TrimLeft(body, "\n"), "\n")
}

// formatError is why a file is not a valid task.
type formatError struct{ reason string }

func (e *formatError) Error() string { return e.reason }

func malformed(format string, args ...any) error {
	return &formatError{reason: fmt.Sprintf(format, args...)}
}

// yamlString is yaml.lua's encode_value for a string at indent: a `|` block when it spans lines,
// double-quoted when a plain scalar would read back differently, else plain.
func yamlString(s, indent string) string {
	if strings.Contains(s, "\n") {
		body := indent + "  "
		lines := strings.Split(s, "\n")
		var b strings.Builder
		b.WriteString("|")
		for i, l := range lines {
			if i == len(lines)-1 && l == "" {
				break // the final newline ends the block; it is not an empty line
			}
			b.WriteString("\n" + body + l)
		}
		return b.String()
	}
	if needsQuoting(s) {
		r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\t", `\t`, "\r", `\r`)
		return `"` + r.Replace(s) + `"`
	}
	return s
}

var (
	luaIntRe   = regexp.MustCompile(`^-?[0-9]+$`)
	luaFloatRe = regexp.MustCompile(`^-?[0-9]*\.[0-9]+$`)
	datePrefix = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}`)
)

// needsQuoting is yaml.lua's needs_quoting.
func needsQuoting(s string) bool {
	if s == "" || luaSpace(s[0]) || luaSpace(s[len(s)-1]) {
		return true
	}
	if strings.IndexByte("-?:[]{},&*!|>'\"%@`#", s[0]) >= 0 {
		return true
	}
	switch s {
	case "true", "false", "null", "yes", "no", "True", "False", "Null", "Yes", "No",
		"TRUE", "FALSE", "NULL", "YES", "NO", "~":
		return true
	}
	if luaIntRe.MatchString(s) || luaFloatRe.MatchString(s) || datePrefix.MatchString(s) {
		return true
	}
	return strings.Contains(s, ": ") || strings.Contains(s, " #") || strings.ContainsAny(s, "\":'#")
}
