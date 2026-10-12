# Filing an input or IME bug

Input methods — Korean Hangul, Japanese, Chinese, the mark-entering methods — sit between the
keyboard and the application, and every platform handles them differently. A bug in that path
(the wrong syllable committed, a preedit that vanishes, a candidate window in the wrong place)
often cannot be reproduced on another machine at all, because it depends on the platform's input
method, its version, and its settings. What travels is a **recording**: `autodoc --debug`
writes everything the input path did to one file, and that file replays as a test on any
machine.

## How to record

```sh
autodoc --ui --debug        # the TUI: the event stream, to autodoc-debug-<date>.jsonl
autodoc --gui --debug       # the GUI: the same file, plus the window's input path
autodoc --ui --debug=r.jsonl
```

Start it, reproduce the behaviour (type the composition that misbehaves, and note roughly when),
quit, and send the file it names. `--debug` is refused without `--ui` or `--gui`, and the first
line of the run says where the recording is going.

**Privacy: the file contains everything typed, passwords included.** It is written only where
the flag says, never anywhere by default. If the recording caught a password, delete the file
and record again, or edit the line out — every line is one self-contained JSON object.

## What is in the file

One JSON object per line, each a record of one thing that happened, in order. The `Stage` says
where along the path it was taken:

| Stage | Holds |
|---|---|
| `in` | one event arrived from the windowing toolkit, before the backend applied it |
| `ime` | the input method buffer's state after an event: the text, the composing range, the selection, the committed pieces |
| `out` | the key events committed text was delivered to the application as |
| `cmd` | a command sent back to the toolkit: the snippet answers, the selection and caret tells, the focus handshake |
| `frame` | where a frame's input began and ended — delivery happens at frame ends |
| `draw` | the preedit the frame drew at the caret |
| `tui` | the TUI's own trace: focus moves, mounts, key consumption, scopes, actions, pointer captures |
| `meta` | the recorder's own notes — so far, only the records it had to drop under load |

The window's stages (`in`–`draw`, `frame`) are recorded by golib's `gui` backend; the `tui`
stage by the TUI. A terminal recording holds the `tui` records alone; a GUI recording holds the
whole path, platform → IME → TUI, in one file, in order.

## How a recording becomes a test

golib's replay harness turns a recording into a unit test on any machine: it decodes the
records, drives a fresh input path with them, and checks every frame's emissions against what
the recording says happened. The harness, with its rules and its three-line recipe, is
[gui/replay_test.go](https://github.com/yongjohnlee80/golib/blob/main/gui/replay_test.go) in
golib; in short:

1. Copy the recording into golib's `gui/testdata/<name>.jsonl`, verbatim.
2. Write a test next to the others that replays it:

   ```go
   func TestThe<name>RecordingReplays(t *testing.T) {
       data, err := os.ReadFile("testdata/<name>.jsonl")
       if err != nil {
           t.Fatal(err)
       }
       replayRecording(t, data)
   }
   ```

A recording that the current build's input path cannot reproduce fails the test, naming the
frame, the record and the difference — that failure, on a machine with no input method at all,
is the bug report.

## When a recording cannot be used

The replay needs a complete trace, and refuses one it cannot trust, naming the record that
shows the problem:

- **Dropped records.** A recording that lost records under load says so with a
  `meta`/`dropped` line. What it holds is not what happened; re-record.
- **Unpaired frames.** Every frame's `begin` must have its `end`, and the frame numbers must
  march forward. A `begin` without its `end` (or the reverse) is a truncated or damaged
  recording.
- **Records outside a frame.** Input-shaped records between frames mean a frame marker was
  lost. (A frame's own tail — its caret tell, its draws, and the TUI's trace — may sit there
  legitimately.)

A recording from a newer build, with a stage or kind this one does not know, is also refused,
loudly, rather than passed by being misunderstood.