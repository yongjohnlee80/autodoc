//go:build windows

package userdirs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/internal/procenv"
)

// The contract on Windows: Temp is os.TempDir (GetTempPath) for the same variables and current
// folder, a relative or partial value made full as GetTempPath makes it, the first variable set
// winning whatever its form. Each case states its own answer too, so the os agreeing with a wrong
// expectation fails the case rather than passing it.
func TestTheWindowsTempIsGetTempPaths(t *testing.T) {
	cwd := t.TempDir()
	t.Chdir(cwd)
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	drive := filepath.VolumeName(wd) // C:
	abs := t.TempDir()
	const unset = "\x00"
	for _, tc := range []struct {
		desc, tmp, temp string
		want            func(got string) bool
	}{
		{"a relative TMP", "rel", unset, func(got string) bool { return strings.EqualFold(got, filepath.Join(wd, "rel")) }},
		{"a relative TEMP, TMP not set", unset, "rel2", func(got string) bool { return strings.EqualFold(got, filepath.Join(wd, "rel2")) }},
		{"a relative TMP before an absolute TEMP", "rel", abs, func(got string) bool { return strings.EqualFold(got, filepath.Join(wd, "rel")) }},
		{"an absolute TMP before a relative TEMP", abs, "rel", func(got string) bool { return strings.EqualFold(got, abs) }},
		{"a TMP rooted on the current drive", `\rooted\tmp`, unset, func(got string) bool { return strings.EqualFold(got, drive+`\rooted\tmp`) }},
		{"a TMP with dot folders", abs + `\x\..\y\.`, unset, func(got string) bool { return strings.EqualFold(got, abs+`\y`) }},
		{"a TMP of forward slashes", strings.ReplaceAll(abs, `\`, "/") + "/", unset, func(got string) bool { return strings.EqualFold(got, abs) }},
		{"a TMP with separators doubled at its end", abs + `\\`, unset, func(got string) bool { return strings.EqualFold(got, abs) }},
		{"a drive's root", drive + `\`, unset, func(got string) bool { return strings.EqualFold(got, drive+`\`) }},
		{"a TMP set empty", "", abs, func(got string) bool { return got == "" }},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			for name, v := range map[string]string{"TMP": tc.tmp, "TEMP": tc.temp} {
				t.Setenv(name, "")
				if v == unset {
					if err := os.Unsetenv(name); err != nil {
						t.Fatal(err)
					}
				} else {
					t.Setenv(name, v)
				}
			}
			want := os.TempDir()
			if !tc.want(want) {
				t.Fatalf("os.TempDir() = %q; the case says otherwise (cwd %q)", want, wd)
			}
			if got := Temp(procenv.Env(os.Environ())); got != want {
				t.Errorf("Temp = %q; os.TempDir finds %q", got, want)
			}
		})
	}
}
