package todo

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// stamp is auto-core's timestamp: local time, second precision, ±HH:MM.
func stamp(t time.Time) string { return t.Format("2006-01-02T15:04:05-07:00") }

// rev is a file's revision: the SHA-256 of its bytes.
func rev(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// makeID is paths.lua's make_id: the creation date, then the title lower-cased with punctuation
// dropped and whitespace runs as single dashes.
func makeID(created, title string) string {
	var b strings.Builder
	for i := 0; i < len(title); i++ {
		c := title[i]
		switch {
		case c >= 'A' && c <= 'Z':
			b.WriteByte(c + 'a' - 'A')
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-':
			b.WriteByte(c)
		case luaSpace(c):
			b.WriteByte(' ')
		}
	}
	slug := strings.Join(strings.Fields(b.String()), "-")
	slug = dashes.ReplaceAllString(slug, "-")
	slug = strings.TrimSuffix(strings.TrimPrefix(slug, "-"), "-")
	if slug == "" {
		slug = "untitled"
	}
	return created[:10] + "-" + slug
}

var dashes = regexp.MustCompile(`-+`)

// bucketOf is the store-relative directory a task with this status lives in; an archived task is
// partitioned by its archived_at year and month.
func bucketOf(t Task) string {
	if t.Status == "archived" {
		if len(t.ArchivedAt) >= 7 {
			return path.Join("archived", t.ArchivedAt[:4], t.ArchivedAt[5:7])
		}
	}
	return t.Status
}

// pathOf is where a task belongs.
func pathOf(t Task) string { return path.Join(bucketOf(t), t.ID+".md") }

// setStatus applies a transition's timestamps, as auto-core's status does: completed stamps
// completed_at; archived stamps archived_at and keeps completed_at only when coming from
// completed; every other status clears both. status_changed is now; updated is not touched.
func setStatus(t *Task, status, now string) {
	old := t.Status
	t.Status = status
	t.StatusChanged = now
	switch status {
	case "completed":
		t.CompletedAt, t.ArchivedAt = now, ""
	case "archived":
		t.ArchivedAt = now
		if old != "completed" {
			t.CompletedAt = ""
		}
	default:
		t.CompletedAt, t.ArchivedAt = "", ""
	}
}

// ArchiveAfterDays is how long a completed task stays before refresh archives it.
const ArchiveAfterDays = 28

// agedOut reports a completed_at more than ArchiveAfterDays calendar days before now. Both are read
// as dates only, so a task completed 28 days ago stays and one completed 29 days ago goes.
func agedOut(completedAt, now string) bool {
	a, ok1 := civilDay(completedAt)
	b, ok2 := civilDay(now)
	return ok1 && ok2 && b-a > ArchiveAfterDays
}

var dayRe = regexp.MustCompile(`^([0-9]+)-([0-9]+)-([0-9]+)`)

// civilDay is the day number of an ISO timestamp's leading date.
func civilDay(s string) (int64, bool) {
	m := dayRe.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	y, _ := strconv.Atoi(m[1])
	mo, _ := strconv.Atoi(m[2])
	d, _ := strconv.Atoi(m[3])
	return time.Date(y, time.Month(mo), d, 0, 0, 0, 0, time.UTC).Unix() / 86400, true
}

// digests hashes each frontmatter field of t's format and the description, so two versions of a
// task can be compared field by field from their snapshot rows alone.
func digests(t Task) map[string]string {
	f := formats[t.Version]
	if f == nil {
		f = newest
	}
	out := map[string]string{}
	for _, name := range append(slices.Clone(f.order), "description") {
		sum := sha256.Sum256(fmt.Appendf(nil, "%q", t.get(name)))
		out[name] = hex.EncodeToString(sum[:8])
	}
	return out
}

// changedFields names the fields whose digests differ, in the format's order.
func changedFields(t Task, a, b map[string]string) []string {
	f := formats[t.Version]
	if f == nil {
		f = newest
	}
	var out []string
	for _, k := range append(slices.Clone(f.order), "description") {
		if a[k] != b[k] {
			out = append(out, k)
		}
	}
	return out
}

func rowOf(t Task) SnapshotRow {
	return SnapshotRow{Path: t.Path, ID: t.ID, Rev: t.Rev, Status: t.Status, Assignee: t.Assignee, Fields: digests(t)}
}

func clone(t Task) Task {
	c := t
	c.Tags = append([]string(nil), t.Tags...)
	c.ADR = append([]string(nil), t.ADR...)
	c.Review = append([]string(nil), t.Review...)
	c.Blocked = append([]string(nil), t.Blocked...)
	c.Errors = append([]ErrorEntry(nil), t.Errors...)
	return c
}
