package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// claudeAncestors bounds the walk up from this process to the Claude Code process that runs its
// hook (a shell or two between them).
const claudeAncestors = 8

// claudeLocator is the calling Claude Code session's endpoint (§4.7), for `crew endpoint --claude`,
// which its SessionStart hook runs: the inbox socket, the process holding it, that process's
// reference, and the session's id. The session id is the hook's input (session_id, on stdin), else
// $CLAUDE_CODE_SESSION_ID. The socket is $CLAUDE_CODE_MESSAGING_SOCKET when the hook has it (its pid
// is the file's name), else the first ancestor of this process with a socket in Claude's folders.
func claudeLocator(stdin io.Reader, getenv func(string) string, uid int) (string, error) {
	session := getenv("CLAUDE_CODE_SESSION_ID")
	if stdin != nil {
		var hook struct {
			SessionID string `json:"session_id"`
		}
		if b, err := io.ReadAll(io.LimitReader(stdin, 1<<20)); err == nil && json.Unmarshal(b, &hook) == nil && hook.SessionID != "" {
			session = hook.SessionID
		}
	}
	if session == "" {
		return "", errors.New("no Claude session id: run this from Claude Code's SessionStart hook, which gives it on stdin")
	}
	sock, pid, err := claudeSocket(getenv, uid)
	if err != nil {
		return "", err
	}
	start, err := crew.ProcRef(pid)
	if err != nil {
		return "", fmt.Errorf("reading Claude's process %d: %w", pid, err)
	}
	b, err := json.Marshal(map[string]any{"socket": sock, "pid": pid, "start": start, "session": session})
	return string(b), err
}

// claudeSocket is the inbox socket of the Claude Code process this one runs under, and its pid.
func claudeSocket(getenv func(string) string, uid int) (string, int, error) {
	if sock := getenv("CLAUDE_CODE_MESSAGING_SOCKET"); sock != "" {
		pid, err := strconv.Atoi(strings.TrimSuffix(filepath.Base(sock), ".sock"))
		if err != nil || pid <= 0 {
			return "", 0, fmt.Errorf("CLAUDE_CODE_MESSAGING_SOCKET %s is not named <pid>.sock", sock)
		}
		return sock, pid, nil
	}
	roots := crew.ClaudeSocketDirs(uid)
	pid := os.Getppid()
	for range claudeAncestors {
		for _, r := range roots {
			sock := filepath.Join(r, strconv.Itoa(pid)+".sock")
			if fi, err := os.Stat(sock); err == nil && fi.Mode()&os.ModeSocket != 0 {
				return sock, pid, nil
			}
		}
		next, err := parentOf(pid)
		if err != nil || next <= 1 {
			break
		}
		pid = next
	}
	return "", 0, errors.New("no Claude Code inbox socket among this process's ancestors: run this from Claude Code's SessionStart hook")
}
