package todo

import (
	"embed"
	"fmt"
	"maps"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/yongjohnlee80/autodoc/core/schema"
)

// A todo format is a versioned core/schema file, formats/v<N>.yaml, and the rules a schema cannot
// say. A task's version: picks its format; a new task is written in the newest. A format v2 is a
// new schema file, its rules and a migration: nothing here changes.

//go:embed formats/*.yaml
var formatFiles embed.FS

// format is one version of the todo format.
type format struct {
	version int
	schema  *schema.Schema
	order   []string         // the frontmatter's field order, from the schema
	check   func(Task) error // what the schema cannot say
}

var (
	formats = loadFormats()
	newest  = formats[slices.Max(slices.Collect(maps.Keys(formats)))]
)

// loadFormats parses the embedded format files. A file that does not parse is a build defect,
// which every test that decodes a task catches.
func loadFormats() map[int]*format {
	rules := map[int]func(Task) error{1: checkV1}
	out := map[int]*format{}
	for v, check := range rules {
		src, err := formatFiles.ReadFile(fmt.Sprintf("formats/v%d.yaml", v))
		if err != nil {
			panic("todo: format v" + strconv.Itoa(v) + ": " + err.Error())
		}
		s, err := schema.Parse(src)
		if err != nil {
			panic("todo: format v" + strconv.Itoa(v) + ": " + err.Error())
		}
		out[v] = &format{version: v, schema: s, order: s.Order(), check: check}
	}
	return out
}

// formatOf is the format a frontmatter's version: names.
func formatOf(m *node) (*format, error) {
	v := m.get("version")
	if v == nil || v.kind == nNull {
		return nil, malformed("schema: version is required")
	}
	if v.kind != nNumber || v.num != math.Trunc(v.num) || v.num < 1 || math.IsInf(v.num, 0) {
		return nil, malformed("schema: version must be a whole number, not %s", v.kindName())
	}
	f, ok := formats[int(v.num)]
	if !ok {
		if int(v.num) > newest.version {
			return nil, malformed("format version %d is newer than this AutoDoc", int(v.num))
		}
		return nil, malformed("format version %d is not one this AutoDoc reads", int(v.num))
	}
	return f, nil
}

// decode reads a task file: the strict YAML subset, its format's schema, then its format's rules.
// A file that is not a valid task returns a *formatError carrying the reason it is listed with.
func decode(src []byte) (Task, error) {
	fm, body, err := splitFrontmatter(string(src))
	if err != nil {
		return Task{}, malformed("md.decode: %v", err)
	}
	root, err := decodeYAML(fm)
	if err != nil {
		return Task{}, malformed("md.decode: frontmatter yaml.decode: %v", err)
	}
	if err := automation(root); err != nil {
		return Task{}, err
	}
	f, err := formatOf(root)
	if err != nil {
		return Task{}, err
	}
	if r := f.schema.Validate([]byte(fm), true); len(r.Diagnostics) > 0 {
		return Task{}, malformed("schema: %s", r.Diagnostics[0].Message)
	}
	t, err := f.build(root, cleanBody(body))
	if err != nil {
		return Task{}, err
	}
	if err := f.check(t); err != nil {
		return Task{}, err
	}
	return t, nil
}

// automation refuses a file using what ADR 1791532020 removed, before its schema says anything.
func automation(m *node) error {
	for i, k := range m.keys {
		if removedFields[k] && m.vals[i].kind != nNull {
			return malformed("%s: field '%s'", automationRemoved, k)
		}
	}
	if s := m.get("status"); s != nil && s.kind == nString && s.str == "automated" {
		return malformed("%s: status 'automated'", automationRemoved)
	}
	if e := m.get("errors"); e != nil && e.kind == nList {
		for _, entry := range e.items {
			if entry.kind != nMap {
				continue
			}
			if c := entry.get("code"); c != nil && c.kind == nString && strings.HasPrefix(c.str, "automation-") {
				return malformed("%s: error code '%s'", automationRemoved, c.str)
			}
		}
	}
	return nil
}

// build reads a validated frontmatter into a task. null, ~ and an empty value are absent, and a
// scalar in a list field is a list of one.
func (f *format) build(m *node, description string) (Task, error) {
	t := Task{Description: description}
	for i, k := range m.keys {
		v := m.vals[i]
		decl, ok := f.schema.Field(k)
		if !ok || v.kind == nNull || k == "description" { // the body is the description
			continue
		}
		val, err := valueOf(decl, v)
		if err != nil {
			return Task{}, err
		}
		t.set(k, val)
		if val == "" && !decl.Required {
			t.blank = append(t.blank, k)
		}
	}
	return t, nil
}

// valueOf is a node as its declared field's Go value: string, int, []string or a list of maps.
func valueOf(decl schema.Field, v *node) (any, error) {
	bad := func() error { return malformed("schema: %s is %s, not %s", decl.Name, v.kindName(), decl.Type) }
	switch decl.Type {
	case schema.Integer:
		if v.kind != nNumber {
			return nil, bad()
		}
		return int(v.num), nil
	case schema.List:
		items := v.items
		switch v.kind {
		case nList:
		case nString:
			items = []*node{v}
		default:
			return nil, bad()
		}
		if decl.ItemType == schema.Map {
			var out []map[string]string
			for _, it := range items {
				if it.kind != nMap {
					return nil, bad()
				}
				e := map[string]string{}
				for j, k := range it.keys {
					if it.vals[j].kind == nString {
						e[k] = it.vals[j].str
					}
				}
				out = append(out, e)
			}
			return out, nil
		}
		var out []string
		for _, it := range items {
			if it.kind != nString {
				return nil, bad()
			}
			out = append(out, it.str)
		}
		return out, nil
	}
	if v.kind != nString {
		return nil, bad()
	}
	return v.str, nil
}

// encode renders t in its format (the newest for a new task) as auto-core's md.encode does, byte
// for byte: the schema's field order, empty fields omitted, the managed header, `# title`, the
// description, one trailing newline.
func encode(t Task) []byte {
	f := formats[t.Version]
	if f == nil {
		f = newest
	}
	return f.encode(t)
}

func (f *format) encode(t Task) []byte {
	t.Version = f.version
	var b strings.Builder
	b.WriteString("---\n")
	for _, name := range f.order {
		decl, _ := f.schema.Field(name)
		switch v := t.get(name).(type) {
		case string:
			if v != "" || decl.Required || slices.Contains(t.blank, name) {
				b.WriteString(name + ": " + yamlString(v, "") + "\n")
			}
		case int:
			b.WriteString(name + ": " + strconv.Itoa(v) + "\n")
		case []string:
			if len(v) == 0 {
				continue
			}
			b.WriteString(name + ":")
			for _, s := range v {
				b.WriteString("\n  - " + yamlString(s, "    "))
			}
			b.WriteString("\n")
		case []map[string]string:
			if len(v) == 0 {
				continue
			}
			b.WriteString(name + ":")
			for _, e := range v {
				keys := slices.Sorted(maps.Keys(e)) // yaml.lua writes a mapping's keys sorted
				for j, k := range keys {
					lead := "\n    "
					if j == 0 {
						lead = "\n  - "
					}
					b.WriteString(lead + k + ": " + yamlString(e[k], "    "))
				}
			}
			b.WriteString("\n")
		}
	}
	b.WriteString("---\n\n")
	b.WriteString(header)
	b.WriteString("\n\n# " + t.Title)
	if desc := strings.TrimRight(t.Description, "\n"); desc != "" {
		b.WriteString("\n\n" + desc)
	}
	b.WriteString("\n")
	return []byte(b.String())
}

// get is a field's value by name: string, int, []string or []map[string]string; nil when the
// task has no such field.
func (t *Task) get(name string) any {
	switch name {
	case "id":
		return t.ID
	case "version":
		return t.Version
	case "status":
		return t.Status
	case "title":
		return t.Title
	case "description":
		return t.Description
	case "due":
		return t.Due
	case "priority":
		return t.Priority
	case "assignee":
		return t.Assignee
	case "tags":
		return t.Tags
	case "adr":
		return t.ADR
	case "review":
		return t.Review
	case "blocked":
		return t.Blocked
	case "created":
		return t.Created
	case "updated":
		return t.Updated
	case "status_changed":
		return t.StatusChanged
	case "completed_at":
		return t.CompletedAt
	case "archived_at":
		return t.ArchivedAt
	case "errors":
		var out []map[string]string
		for _, e := range t.Errors {
			out = append(out, map[string]string{"field": e.Field, "code": e.Code, "message": e.Message, "detected": e.Detected})
		}
		return out
	}
	return t.Extra[name]
}

// set is get's inverse. A field the task has no typed slot for goes to Extra. Setting a field
// forgets that the file wrote it as "".
func (t *Task) set(name string, v any) {
	if i := slices.Index(t.blank, name); i >= 0 {
		t.blank = slices.Delete(slices.Clone(t.blank), i, i+1)
	}
	s, _ := v.(string)
	l, _ := v.([]string)
	switch name {
	case "id":
		t.ID = s
	case "version":
		t.Version, _ = v.(int)
	case "status":
		t.Status = s
	case "title":
		t.Title = s
	case "description":
		t.Description = s
	case "due":
		t.Due = s
	case "priority":
		t.Priority = s
	case "assignee":
		t.Assignee = s
	case "tags":
		t.Tags = l
	case "adr":
		t.ADR = l
	case "review":
		t.Review = l
	case "blocked":
		t.Blocked = l
	case "created":
		t.Created = s
	case "updated":
		t.Updated = s
	case "status_changed":
		t.StatusChanged = s
	case "completed_at":
		t.CompletedAt = s
	case "archived_at":
		t.ArchivedAt = s
	case "errors":
		m, _ := v.([]map[string]string)
		t.Errors = nil
		for _, e := range m {
			t.Errors = append(t.Errors, ErrorEntry{Field: e["field"], Code: e["code"], Message: e["message"], Detected: e["detected"]})
		}
	default:
		if t.Extra == nil {
			t.Extra = map[string]any{}
		}
		t.Extra[name] = v
	}
}

// The v1 rules: what schema.lua checks beyond its fields' types.

var (
	datetimeRe = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}[.0-9]*[Z+-]`)
	dateRe     = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)
	safeIDRe   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)
)

// checkV1 is schema.lua's timestamp shapes and lifecycle_consistency, and a file-safe id.
func checkV1(t Task) error {
	for _, ts := range []struct{ name, v string }{{"created", t.Created}, {"updated", t.Updated},
		{"status_changed", t.StatusChanged}, {"completed_at", t.CompletedAt}, {"archived_at", t.ArchivedAt}} {
		if ts.v != "" && !datetimeRe.MatchString(ts.v) {
			return malformed("schema: %s: string '%s' does not look like an ISO 8601 datetime with offset", ts.name, ts.v)
		}
	}
	for _, e := range t.Errors {
		if !datetimeRe.MatchString(e.Detected) {
			return malformed("schema: errors: detected: string '%s' does not look like an ISO 8601 datetime with offset", e.Detected)
		}
	}
	if t.Due != "" && !dateRe.MatchString(t.Due) {
		return malformed("schema: due: string '%s' is not a bare YYYY-MM-DD date", t.Due)
	}
	if !safeID(t.ID) {
		return malformed("schema: id '%s' is not a safe file name (letters, digits, - and _)", t.ID)
	}
	switch t.Status {
	case "open", "in-progress", "deferred":
		if t.CompletedAt != "" {
			return malformed("schema: completed_at must be nil when status == '%s'", t.Status)
		}
		if t.ArchivedAt != "" {
			return malformed("schema: archived_at must be nil when status == '%s'", t.Status)
		}
	case "completed":
		if t.CompletedAt == "" {
			return malformed("schema: completed_at must be set when status == 'completed'")
		}
		if t.ArchivedAt != "" {
			return malformed("schema: archived_at must be nil when status == 'completed'")
		}
	case "archived":
		if t.ArchivedAt == "" {
			return malformed("schema: archived_at must be set when status == 'archived'")
		}
	}
	return nil
}

func safeID(id string) bool { return len(id) <= 250 && safeIDRe.MatchString(id) }

// Error codes an errors[] entry may carry.
const (
	CodeNotFound   = "not-found"
	CodeUnresolved = "unresolved-variable"
)
