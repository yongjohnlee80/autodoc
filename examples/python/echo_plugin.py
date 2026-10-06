#!/usr/bin/env python3
"""An AutoDoc plugin in Python, with no AutoDoc code: plugin protocol 1 and 2 (docs/plugins.md).

It is a card that echoes what it is sent: its keys, its commands, its focus, and the note it is
fed. The protocol is msgpack-RPC notifications, [2, method, [params]], both ways, over stdin and
stdout; stdout is the protocol's, so anything else goes to stderr, AutoDoc's log of the plugin.

    pip install msgpack

Its manifest, beside it as plugin.toml:

    name     = "py-echo"
    title    = "Python echo"
    kind     = "dialog"
    protocol = 2
    command  = ["python3", "echo_plugin.py"]

    [dialog]
    modal = false

    [feed]
    document = true

    [[commands]]
    id    = "clear"
    title = "Clear"
    key   = "c"
"""

import os
import sys

import msgpack

NOTIFICATION = 2


class Plugin:
    def __init__(self, out):
        self.out = out
        self.width, self.height = 40, 20
        self.lines = []

    def send(self, method, params):
        self.out.write(msgpack.packb([NOTIFICATION, method, [params]], use_bin_type=True))
        self.out.flush()

    def say(self, line):
        """Adds a line, and sends the whole dialog: the latest frame is all AutoDoc paints."""
        print("said: " + line, file=sys.stderr, flush=True)
        self.lines.append(line)
        rows = [[{"t": text[: self.width]}] for text in self.lines[-self.height :]]
        rows[0][0]["b"] = True  # runs carry their style: here, the oldest line in bold
        self.send("host.frame", {"rows": rows})

    def handle(self, method, p):
        """One notification; False once the plugin is to stop."""
        if method == "plugin.open":
            # answer with the protocol it was opened at: 1 or 2, whichever its manifest says
            self.send("host.ready", {"protocol": p["protocol"]})
            self.width, self.height = p.get("width", 0), p.get("height", 0)
            self.say("open %dx%d %s p%d" % (self.width, self.height, p["theme"]["name"], p["protocol"]))
        elif method == "plugin.key":
            if p["key"] == "q":
                self.send("host.close", {})
            else:
                self.say("key " + p["key"])
        elif method == "plugin.resize":
            self.width, self.height = p["width"], p["height"]
        elif method == "plugin.focus":
            self.say("focus %s" % p["focused"])
        elif method == "plugin.command":
            if p["id"] == "clear":
                self.lines = []
            self.say("command " + p["id"])
        elif method == "plugin.document":
            if p.get("too_large"):
                self.say("doc %s v%d too large" % (p["path"] or "draft", p["version"]))
            else:
                words = len(p["text"].split())
                cur = p["cursor"]
                self.say("doc %s v%d %d words at %d:%d" % (p["path"] or "draft", p["version"], words, cur["line"], cur["col"]))
        elif method == "plugin.close":
            return False
        # anything else (plugin.theme, plugin.hide, a later protocol's) is not this plugin's
        return True


def main():
    stdin, stdout = sys.stdin.buffer, sys.stdout.buffer
    plugin = Plugin(stdout)
    unpacker = msgpack.Unpacker(raw=False)
    while True:
        chunk = os.read(stdin.fileno(), 65536)
        if not chunk:
            return 0  # AutoDoc is gone
        unpacker.feed(chunk)
        for message in unpacker:
            if not isinstance(message, list) or len(message) != 3 or message[0] != NOTIFICATION:
                print("not a notification: %r" % (message,), file=sys.stderr, flush=True)
                continue
            _, method, params = message
            if not plugin.handle(method, params[0] if params else {}):
                return 0


if __name__ == "__main__":
    sys.exit(main())
