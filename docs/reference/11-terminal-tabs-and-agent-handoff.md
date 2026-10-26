---
title: "Chapter 11: Embedded Terminal Tabs and One-Key Agent Task Handoff"
type: reference
status: active
created: 2026-10-17
tags: [autodoc, reference-book, terminal, agents, handoff, todos, tui]
abstract: "Comprehensive guide to configuring embedded terminal tabs and CLI coding agents in AutoDoc, operating the TerminalTabs and Agent drawers, and executing the one-key Agent Task Handoff workflow (SPC x -> A) from the Todos Panel."
---

# Chapter 11: Embedded Terminal Tabs and One-Key Agent Task Handoff

Modern technical authoring and software architecture rarely happen in isolation from execution. While reading an architecture decision record (ADR) or triaging a backlog in AutoDoc, you frequently need to run a shell command, inspect `git log`, or delegate an implementation task to an interactive CLI coding agent such as Claude Code, Gemini CLI, OpenAI Codex, or Aider.

AutoDoc embeds full pseudo-terminal (PTY) multiplexers directly inside both the terminal UI (`--ui`) and the native GUI (`--gui`). Rather than treating shells and AI agents as opaque external windows, AutoDoc wires its `terminals` and `agents` features (`tui/terminals.go`, `tui/agenttabs.go`, `tui/agentinstances.go`) into the narrow `Core` host interface and connects them directly to the built-in Todo Task Engine (`tui/todos_assign.go`). With a single keystroke (`A`) inside the Todos Panel (`SPC x`), you can assign a task, transition its status to `in-progress`, launch or focus the target agent's PTY tab, and paste a structured task brief straight into its prompt.

---

## 1. Configuring Terminal Tabs and Agent Profiles

AutoDoc organizes embedded PTYs into two distinct, dockable `TerminalTabs` drawers contributed to the program's panel registry:

1. **The Shell Terminal Panel (`terminalView`)**: General-purpose interactive shells (`bash`, `zsh`, `fish`) for running builds, tests, and Git commands.
2. **The Dedicated Agent Panel (`agentView`, `SPC g`)**: Long-lived CLI coding agents, each started from an agent profile, identified by a persistent `codename` and scoped to the active workspace.

Agent profiles live in AutoDoc's preferences and are managed in **System › Agent profiles…** (`SPC G`); there is no config-file section for them. The dialog lists each profile (`●` marks the default, `▶` a running one) with **Use**, **Add…**, **Edit…**, **Make default** and **Remove**. A profile has three fields:

- **name**: the display label, for example `Claude Code`;
- **codename**: its routing identity, derived from the name when left empty (lower-case letters or digits, separated by hyphens);
- **command**: the command line that starts the agent, run through `/bin/sh -c`, for example `claude --model opus` or `codex`. `{root}` and `{workspace}` in it become the workspace's root folder and name, each quoted for the shell.

The first profile added becomes the default, which `SPC g` starts; **Make default** chooses another. The shell terminal's tabs need no profile: `+` starts your login shell.

### Delivery Identity: Display `label` vs. Immutable `codename`

In `tui/agentinstances.go`, AutoDoc strictly separates an agent's human-facing display `label` (`Name`) from its routing identity (`Codename`):

- **Display Labels May Repeat**: You can rename `"Claude Code"` to `"Claude (Sonnet 4.5)"` at any time without breaking existing task assignments or running tabs.
- **Persistent `codename` Identity**: When a profile is first saved without an explicit `codename`, AutoDoc derives a lowercase, hyphen-separated slug from the name (allocating deterministic numeric suffixes on collision) and persists it once.
- **Incarnation Tracking (`AgentRef`)**: Every running agent PTY is stamped with a monotonic incarnation token (`agent-1`, `agent-2`, …) via `agentIncarnation.Add(1)`. If a profile is deleted while its PTY is still running, the running instance is retained as an `Orphan` entry in `AgentEntries()` and keeps its `codename` reserved until the process exits and cleanup completes (`awaitInstanceRetirement`).

---

## 2. Operating the Terminal and Agent Drawers

Both the shell terminal drawer (`` SPC ` `` / `:terminal`) and the agent drawer (`SPC g`) share a unified tab strip controller (`tui/tabmenus.go` over `widget.TerminalPanel`) while remaining independent dock panels that can be placed on any window edge (`left`, `right`, `top`, `bottom`) or floated freely (`Alt`+drag or `SPC L`).

### Tab Strip Controls and Lifecycle

- **Adding Tabs (`+`)**:
  - In the **Terminal Panel**, clicking `+` (or selecting **New** from the strip menu `≡`) spawns a fresh login shell in the active workspace's root directory.
  - In the **Agent Panel**, clicking `+` opens a menu of all configured agent profiles (`addRows` in `tui/agenttabs.go`), badging currently running profiles with `▶`. Selecting a running agent switches to its tab; selecting a stopped profile spawns it in a new tab named by its `codename`.
- **The Strip Menu (`≡`, Right-Click, or `Enter` on the Strip)**:
  - **Go to ›** — Jump directly to any open tab by label.
  - **Restart / Close** — In the Agent Panel, **Restart** (`restartAgent`) starts a stopped agent or prompts for confirmation (`&Yes, restart it`) before terminating a running agent and respawning its updated command in a fresh tab. **Close** (`closeAgent`) stops the process and removes the tab.
  - **Profiles…** — Opens the **Agent profiles…** dialog to add, edit, or set the default agent.
  - **Resume** — Sends the tab's program a SIGCONT, waking one that a suspend stopped. `Ctrl+Z` itself never reaches a program here; `panel.resume` (the command) and `autodoc.agents.resume` (Lua) do the same.
  - **Theme and font…** — Customizes the terminal color palette and (in `--gui`) the monospace terminal font independently of the editor's prose font.
  - **Maximize / Restore (`F11`)** — Expands the drawer to fill the entire dock area or restores its saved percentage geometry.
  - **Hide** — Closes the drawer while leaving every child shell and agent process running in the background.

### Live Input vs. PTY Normal Mode (`Ctrl+T`)

When focus is inside a terminal or agent PTY, **every keystroke—including `Esc`—is sent directly to the child process** so interactive TUI agents (such as Claude Code, Vim inside a shell, or `htop`) behave naturally.

To scroll back through history, search terminal output, or copy text without reaching for the mouse:

1. Press `Ctrl+T` to switch the PTY into **Normal Mode**.
2. Navigate history using standard Vim motions (`h`, `j`, `k`, `l`, `gg`, `G`, `Ctrl+U`, `Ctrl+D`), search scrollback with `/`, `n`, and `N`, or select and yank text with `v` and `y`.
3. Press `Ctrl+T` again (or `i`) to return to live PTY input mode, or press `Esc` while in PTY Normal Mode to **hide** the panel and return keyboard focus to the document editor (`RestoreKeyboardPane`).

---

## 3. The One-Key Agent Task Handoff Workflow (`SPC x` -> `A`)

Without integrated task handoff, delegating a backlog item to an AI coding agent requires a tedious, error-prone sequence: finding the task file path, manually editing its YAML frontmatter to set `status: in-progress` and `assignee:`, moving the file from `.todo-list/open/` to `.todo-list/in-progress/`, copying the task title and linked ADR paths, switching to the agent terminal, and pasting a prompt.

AutoDoc collapses this entire workflow into a single keystroke inside the Todos Panel (`tui/todos_assign.go`):

![End-to-End Sequence of the One-Key Agent Task Handoff (SPC x -> A)](agent-handoff-sequence.mmd)

### Step-by-Step Handoff Execution

1. **Open the Todos Panel (`SPC x`) and Select a Task**: Move the cursor to any task in an active bucket (`Open`, `In progress`, `Deferred`, or `Completed`) within a claimed, writable store.
2. **Press `A` (`Shift+A`, `App.todoAssign`)**:
   - AutoDoc queries `Core.AgentEntries()` and filters them via `assignable()`—excluding any instance currently shutting down (`Closing`) and any stopped `Orphan` whose profile was removed.
   - It opens the **Agent Assign Picker** (`agentAssignPicker`), displaying each agent's display name, `codename`, and live state (`running` or `stopped`), alongside a live right-hand preview pane showing the exact command and `autodoc:<codename>` assignee string.
3. **Choose the Target Agent and Enter Optional Instructions**:
   - Press `Enter` on an agent (for example, `Claude Code (claude)`).
   - AutoDoc opens the **Instructions** prompt (`todoAssign`). Type a custom directive (single line; newlines are rejected by `ErrAgentTask`) or press `Enter` on an empty line to use the default instruction: `"Task assigned to you."` (Pressing `Esc` cancels before any store write occurs.)
4. **Atomic Store Mutation (`todos.assign`)**:
   - AutoDoc records a `TicketWorkspace` guard (`ws: t.c.Ticket(nil, TicketWorkspace)`) and invokes `todos.assign(at, id, rev, "autodoc:<codename>", instruction)`.
   - Inside the daemon (`core/todo`), `todos.assign` verifies the task's SHA-256 `rev` token. If the task is currently `open`, Decision D1 atomically transitions its status to `in-progress`, stamps `status_changed` and `updated`, moves the Markdown file into `.todo-list/in-progress/`, and commits the SQLite journal row.
   - In the Todos Panel, `followTask` automatically switches the active tab to **In progress** and places the selection cursor on the moved task.
5. **PTY Spawn/Focus and Bracketed Paste Delivery (`Core.HandTask`)**:
   - Once `todos.assign` succeeds and the `TicketWorkspace` guard confirms the user has not switched workspaces mid-flight, `handOver` constructs a `TaskRef{Title, Path, ID}` using the **post-move** file path in `in-progress/` and calls `Core.HandTask(AgentRef{Codename}, ref, instruction, done)`.
   - If the chosen agent is **stopped**, AutoDoc prompts: *"Start <Agent Name> to hand over this task?"* Upon confirmation, `startInstance` opens the `agent` drawer, writes the workspace's agent guide (`writeAgentGuide`: AGENTS.md, CLAUDE.md and `bin/autodoc`), spawns the PTY in AutoDoc's own folder for the workspace (`<state dir>/agent/<workspace>`), not the workspace root, and selects its tab.
   - `HandTask` formats the canonical 4-line payload:
     ```text
     Task: Implement SQLite crash-recoverable todo journal
     File: /abs/path/to/workspace/.todo-list/in-progress/2026-10-17-implement-sqlite-todo-journal.md
     ID: 2026-10-17-implement-sqlite-todo-journal
     Instruction: Task assigned to you.
     ```
   - It dispatches the payload via `panel.SendBracketedPaste(tab, payload)`: one bracketed paste (`\x1b[200~...\x1b[201~`), so the multi-line text lands intact at the prompt. A program that hasn't enabled bracketed paste gets nothing, never keystrokes, and the toast says why. An agent `HandTask` just started is tried again every `agentReadyPoll` (`100ms`) for up to `agentReadyWait` (`10s`) while its program starts.
   - **AutoDoc never presses Enter** (ADR 1792991980). An Enter can't tell the agent's input from a permission or select prompt it is showing, so it could answer one. Instead the payload waits in the agent's input, and the agent's panel opens with its tab selected and the keyboard in input mode. The toast says *"assigned to Claude Code, and typed into its input: press Enter there to send it"*, and the user's Enter starts the agent on the task file and its linked `$KB_ROOT` ADRs.

![Task Dispatch Latency: Manual Context Copy-Paste vs AutoDoc One-Key Handoff](agent-dispatch-latency.chart.json)

---

## 4. Separation of Concerns: `autodoc:<codename>` vs. Other Tools' Agents

A critical architectural boundary in ADR 1791532020 (§2.7a) is that **AutoDoc manages only its own embedded terminal agents**; an agent another tool runs is that tool's to manage:

- **Assignee Namespace Isolation**: When you assign a task via `A` in AutoDoc's Todos Panel, the `assignee` field is always prefixed with `autodoc:<codename>` (for example, `autodoc:claude` or `autodoc:gemini`). An assignee written by another tool keeps that tool's own form (for example `agent:<name>`), and AutoDoc never delivers to it.
- **Durable Record vs. Best-Effort Terminal Delivery**: The `todos.assign` write in `.todo-list/` is the durable record of ownership; `HandTask` is a best-effort UI delivery side effect. If you decline to start a stopped agent (`ErrAgentCancelled`), or if you switch workspaces before the RPC write returns, the task remains durably assigned to `autodoc:<codename>` in `.todo-list/in-progress/` and AutoDoc displays an actionable toast:
  `"todos: assigned to autodoc:claude, but not delivered (you didn't start it): start the agent, then A on the task to deliver it again"`. Pressing `A` a second time re-delivers the payload cleanly.

![Complete Reference of Terminal Tabs Configuration, Keybindings, and Handoff Conventions](terminal-tabs-config-reference.csv)

---

## Summary

By pairing persistent agent profiles (`tui/agentinstances.go`) with atomic `todos.assign` transitions (`tui/todos_assign.go`), AutoDoc turns task delegation from a 45-second context-gathering chore into a sub-second, crash-safe handoff. Once the task brief lands in the agent's terminal, the agent itself needs a fast, token-efficient way to search the Knowledge Base, inspect linked ADRs, and validate its writes—the exact subject of Chapter 12.

---

[← Previous: Chapter 10: Todo Task Engine](./10-todo-task-engine.md) | [Index](./README.md) | [Next: Chapter 12: AI Agent Integration & CLI RPC →](./12-ai-agent-integration-and-cli-rpc.md)

