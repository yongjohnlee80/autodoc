//go:build windows

package fsbounds

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// junction makes a junction at link to the folder target (mklink /J: no privilege needed).
func junction(t *testing.T, link, target string) {
	t.Helper()
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Fatalf("mklink /J %s %s: %v %s", link, target, err, out)
	}
}

// shortName is p's 8.3 short name (GetShortPathName), "" when the volume keeps none for it.
func shortName(t *testing.T, p string) string {
	t.Helper()
	long, err := windows.UTF16PtrFromString(p)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n, err := windows.GetShortPathName(long, &buf[0], uint32(len(buf)))
	if err != nil || n == 0 {
		return ""
	}
	s := windows.UTF16ToString(buf[:n])
	if strings.EqualFold(s, p) {
		return ""
	}
	return s
}

// A root and a path are one place however Windows lets them be spelled: a drive letter or a name in
// another case, or a folder by its 8.3 short name (the runner's temp folder is C:\Users\RUNNER~1).
func TestOnePlaceSpelledTwoWaysIsInTheWorkspace(t *testing.T) {
	tr := newTree(t)
	file := filepath.Join(tr.root, "notes", "a.md")
	for _, spelled := range []struct{ what, root, path string }{
		{"a root in upper case", strings.ToUpper(tr.root), file},
		{"a path in lower case", tr.root, strings.ToLower(file)},
		{"a drive letter in the other case", tr.root, swapDrive(file)},
	} {
		tr.g.SetServed(func() []Root { return []Root{{Name: "kb", Dir: spelled.root}} })
		if ws, name, err := tr.g.Locate(spelled.path); err != nil || ws.Name != "kb" || !strings.EqualFold(name, "notes/a.md") {
			t.Errorf("%s: Locate(%s) = %v %q, %v", spelled.what, spelled.path, ws, name, err)
		}
		if err := tr.g.check(Root{Name: "kb", Dir: spelled.root}, "notes/a.md", writeKB, opWrite); err != nil {
			t.Errorf("%s: a write of notes/a.md: %v", spelled.what, err)
		}
	}
	short := shortName(t, tr.root)
	if short == "" {
		long := filepath.Join(filepath.Dir(tr.root), "a folder with a long name")
		mustMkdir(t, filepath.Join(long, "notes"))
		mustWrite(t, filepath.Join(long, "notes", "a.md"), "a")
		if short = shortName(t, long); short == "" {
			t.Skip("this volume keeps no 8.3 short names")
		}
		tr.root, file = long, filepath.Join(long, "notes", "a.md")
	}
	for _, spelled := range []struct{ what, root, path string }{
		{"a root by its short name", short, file},
		{"a path by its short name", tr.root, filepath.Join(short, "notes", "a.md")},
	} {
		tr.g.SetServed(func() []Root { return []Root{{Name: "kb", Dir: spelled.root}} })
		if ws, name, err := tr.g.Locate(spelled.path); err != nil || ws.Name != "kb" || name != "notes/a.md" {
			t.Errorf("%s: Locate(%s) = %v %q, %v", spelled.what, spelled.path, ws, name, err)
		}
	}
}

func swapDrive(p string) string {
	if len(p) < 2 || p[1] != ':' {
		return p
	}
	if p[0] >= 'a' {
		return strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.ToLower(p[:1]) + p[1:]
}

// A junction is followed and not trusted, as a symbolic link is: one out of the workspace is
// outside it, one to a hidden folder is hidden, and one that stays in reaches its target.
func TestAJunctionIsFollowedNotTrusted(t *testing.T) {
	tr := newTree(t)
	junction(t, filepath.Join(tr.root, "notes", "out"), tr.outside)
	junction(t, filepath.Join(tr.root, "notes", "keys"), filepath.Join(tr.root, ".ssh"))
	junction(t, filepath.Join(tr.root, "notes", "in"), filepath.Join(tr.root, "visible"))
	refusedFor(t, "a junction out", tr.check("notes/out/secret", readAny, opRead), whyOutside)
	refusedFor(t, "a junction to .ssh", tr.check("notes/keys/id_x", readAny, opRead), whyHidden)
	refusedFor(t, "a junction to the config folder", tr.check("notes/in/cfg/lua/x.lua", writeKB, opWrite), whyOwn)
	if err := tr.check("notes/in/new.md", writeKB, opWrite); err != nil {
		t.Errorf("a junction that stays in the root: %v", err)
	}
}

// A junction whose target is gone leads nowhere, as a dangling link does: refused for that, not
// let through as an empty folder.
func TestADanglingJunctionLeadsNowhere(t *testing.T) {
	tr := newTree(t)
	target := filepath.Join(tr.root, "gone")
	mustMkdir(t, target)
	junction(t, filepath.Join(tr.root, "notes", "gone"), target)
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	refusedFor(t, "a dangling junction", tr.check("notes/gone/x.md", writeKB, opWrite), whyDangling)
}

// A workspace may be a drive's root (C:\), whose spelling already ends in the separator: a path
// below it is in it.
func TestADrivesRootIsAWorkspace(t *testing.T) {
	vol := filepath.VolumeName(os.Getenv("SystemRoot"))
	if vol == "" {
		t.Skip("no SystemRoot to find a drive by")
	}
	tr := newTree(t)
	root := vol + string(filepath.Separator)
	tr.g.SetServed(func() []Root { return []Root{{Name: "drive", Dir: root}} })
	windir := os.Getenv("SystemRoot")
	if ws, name, err := tr.g.Locate(windir); err != nil || ws.Name != "drive" || !strings.EqualFold(name, filepath.Base(windir)) {
		t.Errorf("Locate(%s) under the drive's root = %v %q, %v", windir, ws, name, err)
	}
}

// A name Windows reads as another is refused before anything is opened: a stream, a device, a
// trailing dot or space.
func TestANameWindowsReadsAsAnotherIsRefused(t *testing.T) {
	tr := newTree(t)
	for _, name := range []string{"notes/a.md:hidden", "notes/a.md::$DATA", "CON", "notes/aux.txt", "notes/a.md.", "notes/a.md "} {
		refusedFor(t, name, tr.check(name, readAny, opRead), whyName)
	}
	if _, err := os.Lstat(filepath.Join(tr.root, "notes", "a.md:hidden")); err == nil {
		t.Error("the stream was made")
	}
}

// A second hard link is seen on Windows too (GetFileInformationByHandle's NumberOfLinks).
func TestASecondNameIsSeenOnWindows(t *testing.T) {
	tr := newTree(t)
	if err := os.Link(filepath.Join(tr.outside, "secret"), filepath.Join(tr.root, "notes", "secret.md")); err != nil {
		t.Fatal(err)
	}
	refusedFor(t, "a hard link read", tr.check("notes/secret.md", readAny, opRead), whyLinked)
}

// A root given by a UNC path serves its UNC paths; the same folder by its drive letter is, to the
// lexical comparison, another place, so a call spelled so is outside: it fails closed.
func TestAUNCRootServesItsOwnSpelling(t *testing.T) {
	tr := newTree(t)
	vol := filepath.VolumeName(tr.root)
	if len(vol) != 2 {
		t.Skip("the temp folder is not on a drive letter")
	}
	unc := `\\localhost\` + strings.ToLower(vol[:1]) + `$` + tr.root[len(vol):]
	if _, err := os.Stat(unc); err != nil {
		t.Skipf("the drive's admin share is not reachable here: %v", err)
	}
	tr.g.SetServed(func() []Root { return []Root{{Name: "kb", Dir: unc}} })
	if ws, name, err := tr.g.Locate(filepath.Join(unc, "notes", "a.md")); err != nil || ws.Name != "kb" || name != "notes/a.md" {
		t.Errorf("Locate on the UNC root = %v %q, %v", ws, name, err)
	}
	if _, _, err := tr.g.Locate(filepath.Join(tr.root, "notes", "a.md")); err == nil {
		t.Error("the drive letter's spelling of a UNC root's file was let through")
	}
}

// caseSensitive makes dir case-sensitive (fsutil setCaseSensitiveInfo), or skips where Windows can't.
func caseSensitive(t *testing.T, dir string) {
	t.Helper()
	if out, err := exec.Command("fsutil.exe", "file", "setCaseSensitiveInfo", dir, "enable").CombinedOutput(); err != nil {
		t.Skipf("this Windows makes no case-sensitive folder here: %v %s", err, out)
	}
}

// In a case-sensitive directory kb and KB are two folders: a junction from kb into KB is outside
// kb, a path in KB is not kb's, and kb by another case of its own name still is kb.
func TestACaseSensitiveFoldersSiblingIsOutside(t *testing.T) {
	base := t.TempDir()
	caseSensitive(t, base)
	kb, other := filepath.Join(base, "kb"), filepath.Join(base, "KB")
	mustMkdir(t, filepath.Join(kb, "notes"))
	mustMkdir(t, other)
	mustWrite(t, filepath.Join(kb, "notes", "a.md"), "a")
	mustWrite(t, filepath.Join(other, "secret.md"), "s")
	junction(t, filepath.Join(kb, "notes", "sib"), other)
	g := mustGuard(t)
	g.SetServed(func() []Root { return []Root{{Name: "kb", Dir: kb}} })
	refusedFor(t, "a junction into the case sibling", g.check(Root{Name: "kb", Dir: kb}, "notes/sib/secret.md", readAny, opRead), whyOutside)
	if ws, name, err := g.Locate(filepath.Join(other, "secret.md")); err == nil {
		t.Errorf("Locate in the case sibling = %v %q: want it outside kb", ws, name)
	}
	if ws, name, err := g.Locate(filepath.Join(kb, "notes", "a.md")); err != nil || ws.Name != "kb" || name != "notes/a.md" {
		t.Errorf("Locate in kb itself = %v %q, %v", ws, name, err)
	}
}
