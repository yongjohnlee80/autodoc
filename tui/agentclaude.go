package tui

import (
	"path/filepath"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// What a crew Claude agent's Claude Code knows (docs/decisions/crew.md#wake-acceptance-not-tool-approvals):
// its settings name only the hook events it knows, read from its binary (crew.ClaudeKnows). Finding
// the binary (a mise shim runs mise) and its first read take a moment, so both run off the loop: when
// the agents start (Started) and again after each spawn, for the next one. The spawn waits only for
// an answer already on its way, and asks crew.ClaudeKnows again, which a binary unchanged since
// answers from what it kept and an updated one reads anew.

// claudeAnswer is the Claude Code one program runs, and whether it could be read, on its way until
// done closes.
type claudeAnswer struct {
	done chan struct{}
	path string
	err  error
}

// warmClaude starts finding and reading, off the loop, the Claude Code that p's command runs, unless
// that is already on its way or answered for its program and PATH: found on p's PATH (agentPath),
// which is the agent's but for its first folder (its own autodoc alone), the first file there that
// reads as a Claude Code, a mise shim or a wrapper followed to the CLI it runs (crew.ClaudeOf); for a
// program of another name that is none (a profile set to Claude Code, agentKinds), the claude there.
// On the loop.
func (a *agents) warmClaude(p agentProfile) *claudeAnswer {
	program, path := agentProgram(p.Command), p.agentPath()
	key := program + "\x00" + path
	if f := a.claudeAnswers[key]; f != nil {
		return f
	}
	if a.claudeAnswers == nil {
		a.claudeAnswers = map[string]*claudeAnswer{}
	}
	f := &claudeAnswer{done: make(chan struct{})}
	a.claudeAnswers[key] = f
	dir := a.agentDir
	go func() {
		defer close(f.done)
		f.path, _, f.err = crew.ClaudeOf(program, path, dir, "") // read now, so the spawn finds it kept
		if f.err != nil && filepath.Base(program) != crew.KindClaude {
			// a profile set to Claude Code under another name (a wrapper, a script): the claude it runs
			if bin, _, err := crew.ClaudeOf(crew.KindClaude, path, dir, ""); err == nil {
				f.path, f.err = bin, nil
			}
		}
	}()
	return f
}

// claudeHooks is what the Claude Code p's command runs knows, for its spawn: the warmed binary, waited
// for only while it is found, asked again, and the next spawn's warm-up started. One that can't be
// found or read is taken to know crew.ClaudeFloor alone (its prompts unreported, so a message typed
// to it is never sent with Enter), told once per program: a notice, never a failure, since the
// agent still starts and is woken.
func (a *agents) claudeHooks(p agentProfile) crew.ClaudeHooks {
	program, name := agentProgram(p.Command), p.Name
	f := a.warmClaude(p)
	<-f.done
	delete(a.claudeAnswers, program+"\x00"+p.agentPath())
	a.warmClaude(p) // found again, off the loop: a shim may name another version by the next spawn
	err := f.err
	if err == nil {
		var k crew.ClaudeHooks
		if k, err = crew.ClaudeKnows(f.path); err == nil {
			return k
		}
	}
	if !a.claudeWarned[program] {
		if a.claudeWarned == nil {
			a.claudeWarned = map[string]bool{}
		}
		a.claudeWarned[program] = true
		a.c.Notify(a.c.Trf("autodoc.crew.claude_prompts_unreported", name, err.Error()))
	}
	return crew.ClaudeFloor()
}
