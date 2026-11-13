package userdirs

import (
	"os"
	"testing"

	"github.com/yongjohnlee80/autodoc/internal/procenv"
)

// The contract, on every OS: for an environment of its own, each folder is the one the os call
// finds when the process has exactly those variables. The process's environment is set for the
// comparison, so these cells run alone (t.Setenv).
func TestEachFolderIsTheOneTheOSFindsForTheSameVariables(t *testing.T) {
	tmp := t.TempDir()
	for _, tc := range []struct {
		name string
		set  map[string]string
	}{
		{"all set", map[string]string{"HOME": "/home/one", "XDG_CACHE_HOME": "/cache/one", "TMPDIR": tmp, "USERPROFILE": `C:\one`, "LocalAppData": `C:\one\cache`, "TMP": tmp, "TEMP": tmp}},
		{"no cache variable", map[string]string{"HOME": "/home/two", "XDG_CACHE_HOME": "", "TMPDIR": "", "LocalAppData": "", "TMP": "", "TEMP": ""}},
		{"a relative cache", map[string]string{"HOME": "/home/three", "XDG_CACHE_HOME": "rel/cache"}},
		{"nothing", map[string]string{"HOME": "", "XDG_CACHE_HOME": "", "TMPDIR": "", "USERPROFILE": "", "LocalAppData": ""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.set {
				t.Setenv(k, v)
			}
			env := procenv.Env(os.Environ())
			wantHome, wantHomeErr := os.UserHomeDir()
			if got, err := Home(env); got != wantHome || (err == nil) != (wantHomeErr == nil) {
				t.Errorf("Home = %q, %v; the os finds %q, %v", got, err, wantHome, wantHomeErr)
			}
			wantCache, wantCacheErr := os.UserCacheDir()
			if got, err := Cache(env); got != wantCache || (err == nil) != (wantCacheErr == nil) {
				t.Errorf("Cache = %q, %v; the os finds %q, %v", got, err, wantCache, wantCacheErr)
			}
			if got, want := Temp(env), os.TempDir(); got != want {
				t.Errorf("Temp = %q; the os finds %q", got, want)
			}
		})
	}
}

// An environment of its own is read, never the process's: two at once each give their own folders,
// and nil gives the os call's.
func TestAnEnvironmentOfItsOwnIsNotTheProcesss(t *testing.T) {
	own := func(n string) procenv.Env { // each OS's variables for the three folders
		d := "/" + n
		return procenv.Env{"HOME=" + d, "USERPROFILE=" + d, "XDG_CACHE_HOME=" + d + "/cache", "LocalAppData=" + d + "/cache",
			"TMPDIR=" + d + "/tmp", "TMP=" + d + "/tmp", "TEMP=" + d + "/tmp"}
	}
	one, two := own("one"), own("two")
	h1, err1 := Home(one)
	h2, err2 := Home(two)
	if err1 != nil || err2 != nil || h1 == h2 {
		t.Errorf("two environments' homes: %q (%v) and %q (%v), want each its own", h1, err1, h2, err2)
	}
	c1, err1 := Cache(one)
	c2, err2 := Cache(two)
	if err1 != nil || err2 != nil || c1 == c2 {
		t.Errorf("two environments' caches: %q (%v) and %q (%v), want each its own", c1, err1, c2, err2)
	}
	if Temp(one) == Temp(two) {
		t.Errorf("two environments' temporary folders are both %q", Temp(one))
	}
	want, wantErr := os.UserHomeDir()
	if got, err := Home(nil); got != want || (err == nil) != (wantErr == nil) {
		t.Errorf("Home(nil) = %q, %v; os.UserHomeDir is %q, %v", got, err, want, wantErr)
	}
	wantCache, wantCacheErr := os.UserCacheDir()
	if got, err := Cache(nil); got != wantCache || (err == nil) != (wantCacheErr == nil) {
		t.Errorf("Cache(nil) = %q, %v; os.UserCacheDir is %q, %v", got, err, wantCache, wantCacheErr)
	}
	if got := Temp(nil); got != os.TempDir() {
		t.Errorf("Temp(nil) = %q; os.TempDir is %q", got, os.TempDir())
	}
}
