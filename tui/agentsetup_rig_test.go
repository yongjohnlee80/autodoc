package tui

import (
	"bufio"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/term"

	"github.com/yongjohnlee80/autodoc/internal/procenv"
	"github.com/yongjohnlee80/autodoc/internal/usershell"
	"github.com/yongjohnlee80/autodoc/tui/agents"
)

// The agent setup cells run on every OS. Their stand-ins (a claude on PATH, the build an agent's
// hooks call) are this test binary, copied in under the name it stands for (name.exe on Windows,
// where PATH is searched with PATHEXT), which init recognises before any test runs. Each keeps its
// file beside itself, so nothing has to reach it through the environment the agent is started in.

func init() {
	switch strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe") {
	case "claude":
		os.Exit(fakeClaudeMain())
	case "autodoc-build":
		os.Exit(fakeBuildMain())
	case "hearing-agent":
		os.Exit(hearingAgentMain())
	case "codex":
		os.Exit(fakeCodexMain())
	}
}

// fakeCodexMain is the stand-in codex a Codex agent's setup asks (crew.SetupFor): a version, and a
// --help that offers --no-daemon.
func fakeCodexMain() int {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--version":
			fmt.Println("codex-cli 0.0.0-test")
		case "--help":
			fmt.Println("      --no-daemon   Run without the shared background server")
		}
	}
	return 0
}

// exeName is name as a program file on this OS.
func exeName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// standIn copies this test binary into dir as the program name, and answers its path.
func standIn(t *testing.T, dir, name string) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, exeName(name))
	if err := copyFile(exe, path); err != nil {
		t.Fatal(err)
	}
	return path
}

// besideMe is file in the folder of the running program.
func besideMe(file string) string {
	exe, err := os.Executable()
	if err != nil {
		return file
	}
	return filepath.Join(filepath.Dir(exe), file)
}

// fakeClaudeMain is the test binary as fakeClaude's claude: it says ready, then writes every line
// typed to it into "heard" beside itself, a line only once its Enter arrives.
func fakeClaudeMain() int {
	fmt.Println("ready")
	in := bufio.NewScanner(os.Stdin)
	for in.Scan() {
		f, err := os.OpenFile(besideMe("heard"), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
		if err != nil {
			fmt.Fprintln(os.Stderr, "fake claude:", err)
			return 1
		}
		fmt.Fprintln(f, strings.TrimRight(in.Text(), "\r"))
		f.Close()
	}
	return 0
}

// hearingAgentMain is the test binary as hearingAgent's agent, named by its first argument: its
// terminal raw (on Windows the console's, its input as VT sequences, as an agent CLI reads it), it
// asks for bracketed paste (mode 2004), says it started, then adds every byte it is sent to
// heard-<name> in the folder that "heard-dir" beside itself names.
func hearingAgentMain() int {
	name := "agent"
	if len(os.Args) > 1 {
		name = os.Args[1]
	}
	dir, err := os.ReadFile(besideMe("heard-dir"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "hearing agent:", err)
		return 1
	}
	if _, err := term.MakeRaw(int(os.Stdin.Fd())); err != nil {
		fmt.Fprintln(os.Stderr, "hearing agent: raw input:", err)
		return 1
	}
	fmt.Printf("\x1b[?2004hstarted %s\r\n", name)
	heard := filepath.Join(string(dir), "heard-"+name)
	buf := make([]byte, 4096)
	for {
		n, err := os.Stdin.Read(buf)
		if n > 0 { // the file is open only while it is written: nothing holds it between reads
			f, ferr := os.OpenFile(heard, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
			if ferr != nil {
				fmt.Fprintln(os.Stderr, "hearing agent:", ferr)
				return 1
			}
			_, _ = f.Write(buf[:n])
			f.Close()
		}
		if err != nil {
			return 0
		}
	}
}

// agentCall is the command line, as a profile's, that runs program with args (bare words): on
// Windows an agent's command runs in PowerShell, which runs a quoted program only after &, and the
// PowerShell is the one runSetupTUI's PATH (programsPath) finds.
func agentCall(program string, args ...string) string {
	call := agents.ShellQuote(program)
	if runtime.GOOS == "windows" {
		call = "& " + usershell.Commands(procenv.Env(nil).With("PATH="+programsPath())).Quote(program)
	}
	return strings.Join(append([]string{call}, args...), " ")
}

// fakeBuildMain is the test binary as the build an agent's hooks call: it adds its arguments, one
// line a call, to "calls" beside itself.
func fakeBuildMain() int {
	f, err := os.OpenFile(besideMe("calls"), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fake build:", err)
		return 1
	}
	defer f.Close()
	fmt.Fprintln(f, strings.Join(os.Args[1:], " "))
	return 0
}

// systemPath is a PATH with the OS's own programs and none of the user's: /usr/bin and /bin, or on
// Windows its System32 and Windows PowerShell's folder (an agent's command runs in PowerShell).
func systemPath() string {
	if runtime.GOOS != "windows" {
		return "/usr/bin:/bin"
	}
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	sys := filepath.Join(root, "System32")
	return strings.Join([]string{sys, root, filepath.Join(sys, "WindowsPowerShell", "v1.0")}, string(os.PathListSeparator))
}

// programsPath is runSetupTUI's PATH: systemPath, and the folders of git and of the sh it runs its
// transports through, where the system's own don't hold them (on Unix they are /usr/bin's and
// /bin's): a plugin the program adds is cloned with git. On Windows Git's sh is in its usr\bin,
// which its launcher puts on the PATH only outside its own shell's environment (MSYSTEM unset),
// and the hosted runner's steps run inside it.
func programsPath() string {
	path := systemPath()
	for _, prog := range []string{"git", "sh"} {
		if p, err := exec.LookPath(prog); err == nil && !slices.Contains(filepath.SplitList(path), filepath.Dir(p)) {
			path += string(os.PathListSeparator) + filepath.Dir(p)
		}
	}
	return path
}

// agentStartWait bounds an agent's start: programs started (on Windows PowerShell, then the agent's,
// this test binary), not a frame's change, which decltest's WaitTimeout bounds. The clock only turns
// a start that never comes into a failure rather than a hang.
var agentStartWait = 10 * time.Second * raceFactor

// waitAgentStarted waits for hearingAgent's agent name to say it started.
func (r *running) waitAgentStarted(t *testing.T, name string) {
	t.Helper()
	r.waitWithin(t, "the agent's \"started "+name+"\"", agentStartWait, func() bool { return strings.Contains(r.s.String(), "started "+name) })
}

// agentsFolder is the agent folder for runSetupTUI: an agent runs in its workspace's folder there,
// and on Windows a process's working folder can't be removed while it exits (shellTempDir).
func agentsFolder(t *testing.T) string {
	t.Helper()
	return shellTempDir(t, "autodoc-agents-")
}

// runSetupTUI is runAgentTUIWith for the cells that run on every OS (the setup's, an agent's): the
// program has a home and a PATH of its own (the system's programs only), and its agents call a
// stand-in build that records each call in calls.
func runSetupTUI(t *testing.T, dir string, profiles []agents.AgentProfile, def string, more map[string]string, opt Options) (d *daemon, r *running, calls string) {
	t.Helper()
	return runSetupTUIIn(t, dir, profiles, def, more, opt, map[string][]string{"kb": {"a.md", "a\n"}}) // runAgentTUIWith's workspace
}

// runSetupTUIIn is runSetupTUI over the daemon's workspaces files, kb's entered first.
func runSetupTUIIn(t *testing.T, dir string, profiles []agents.AgentProfile, def string, more map[string]string, opt Options, files map[string][]string) (d *daemon, r *running, calls string) {
	t.Helper()
	home := t.TempDir()
	opt.seams = seamsWithEnv(t, opt.seams, "HOME="+home, "USERPROFILE="+home, "PATH="+programsPath())
	bins := filepath.Join(t.TempDir(), "a build's dir") // a path the shell must have quoted
	if err := os.Mkdir(bins, 0o700); err != nil {
		t.Fatal(err)
	}
	calls = filepath.Join(bins, "calls")
	bin := standIn(t, bins, "autodoc-build")
	prefs := map[string]string{agents.PrefAgentProfiles: agents.AgentProfilesJSON(profiles)}
	if def != "" {
		prefs[prefAgentDefault] = def
	}
	maps.Copy(prefs, more)
	d = startDaemonWith(t, "", files, daemonOpts{prefs: prefs})
	opt.AgentDir, opt.ConfigPath = dir, agentConfig
	opt.seams.agentBin = bin
	opt.seams.agentPromptsReported = func(string, bool) bool { return true }
	r = runTUI(t, NewSession(d.sock, nil), opt)
	r.connected(t)
	r.s.WaitFor(t, "the notes listed", func(string) bool { return len(r.listed()) > 0 })
	r.s.WaitFor(t, "the preferences read", func(string) bool {
		return onLoop(r, func() bool {
			return r.h.started && r.h.agents.DefaultProfile() == def && len(r.h.agents.Profiles()) == len(profiles)
		})
	})
	return d, r, calls
}
