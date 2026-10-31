// The OpenCode plugin's executable controls (bun test, on the Bun opencode embeds; TestThePluginsBunControls
// runs them). The plugin runs as opencode runs it, against a stand-in client and a stand-in
// `autodoc` on PATH: each run of `autodoc crew endpoint --opencode <id>` is logged, waits for its
// go file (the cell decides when, and with which exit code, it ends), and on success records <id> as
// the daemon's endpoint, so the last registration written is the one there.
import { afterEach, beforeEach, expect, test } from "bun:test";
import { existsSync, lstatSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { AutodocCrew } from "./autodoc-crew.js";

const NOTICE = "AutoDoc mail is waiting for you. Run: autodoc mail receive";
let fake, path, plugin, client, savedPath;

const until = async (ok, ms = 3000) => {
  const end = Date.now() + ms;
  while (!ok()) {
    if (Date.now() > end) throw new Error("timed out waiting");
    await Bun.sleep(10);
  }
};
const runs = () => (existsSync(join(fake, "log")) ? readFileSync(join(fake, "log"), "utf8").trim().split("\n") : []);
const endpoint = () => (existsSync(join(fake, "endpoint")) ? readFileSync(join(fake, "endpoint"), "utf8").trim() : null);
const go = (id, code = 0) => writeFileSync(join(fake, `go-${id}`), String(code));
const pidOf = (id) => Number(runs().find((l) => l.startsWith(`start ${id} `))?.split(" ")[2]);
const gone = (pid) => {
  try {
    process.kill(pid, 0);
    return false;
  } catch (e) {
    return e.code === "ESRCH";
  }
};

// wake writes one request to the plugin's socket and answers its reply.
const wake = (line) =>
  new Promise((resolve, reject) => {
    let got = "";
    Bun.connect({
      unix: path,
      socket: {
        open(s) {
          s.write(line);
        },
        data(_s, d) {
          got += d.toString();
        },
        close() {
          resolve(got.trim());
        },
        error(_s, e) {
          reject(e);
        },
      },
    }).catch(reject);
  });

// woken waits until a wake naming id (and extra's fields) is answered ok: the plugin publishes a
// session once its CLI run has exited, a moment after the endpoint is written. Only the ok wake
// starts a turn; the refused ones before it start none.
const woken = async (id, extra = {}) => {
  const end = Date.now() + 3000;
  for (;;) {
    if ((await wake(JSON.stringify({ session: id, ...extra }) + "\n")) === '{"ok":true}') return true;
    if (Date.now() > end) return false;
    await Bun.sleep(10);
  }
};

// start runs the plugin as opencode would; the cells of registration by message start it without
// its session made at load (prepare), which the cells of a fresh opencode turn on.
const start = async (opts = {}) => {
  plugin = await AutodocCrew({ client }, { registerMs: 400, prepare: false, ...opts });
};
const attentions = () => (existsSync(join(fake, "attention")) ? readFileSync(join(fake, "attention"), "utf8").trim().split("\n") : []);
const chat = (id) => plugin["chat.message"]({ sessionID: id }, {});

beforeEach(() => {
  fake = mkdtempSync(join(tmpdir(), "oc-fake-"));
  writeFileSync(
    join(fake, "autodoc"),
    `#!/bin/sh
d=$(dirname "$0"); id="$4"
if [ "$2" = attention ]; then echo "$3 $4 $5" >> "$d/attention"; exit 0; fi
echo "start $id $$" >> "$d/log"
while [ ! -e "$d/go-$id" ]; do sleep 0.02; done
code=$(cat "$d/go-$id")
echo "end $id $code" >> "$d/log"
[ "$code" = 0 ] && echo "$id" > "$d/endpoint"
exit "$code"
`,
    { mode: 0o755 },
  );
  savedPath = process.env.PATH;
  process.env.PATH = `${fake}:${savedPath}`;
  process.env.AUTODOC_CREW_CONTEXT = "/nowhere/.autodoc-crew/s1";
  delete process.env.AUTODOC_CREW_WAKER;
  const uid = process.getuid();
  const run = `/run/user/${uid}`;
  let dir = `/tmp/autodoc-opencode-${uid}`;
  try {
    if (lstatSync(run).isDirectory()) dir = `${run}/autodoc-opencode`;
  } catch {}
  path = `${dir}/${process.pid}.sock`;
  client = {
    parents: {},
    hang: new Set(),
    prompts: [],
    logs: [],
    session: {
      // a hung check ignores its signal, as a stalled request may: only the plugin's own bound ends it
      get: ({ path: { id } }) =>
        client.hang.has(id) ? new Promise(() => {}) : Promise.resolve({ data: { id, parentID: client.parents[id] } }),
      promptAsync: (req) => {
        client.prompts.push(req);
        return Promise.resolve({ data: {} });
      },
    },
    app: { log: ({ body }) => (client.logs.push(body.message), Promise.resolve({})) },
    // the TUI's routes, through the client's own transport: every ask is recorded
    shown: [],
    _client: { post: ({ url, body }) => (client.shown.push(`${url} ${body.sessionID}`), Promise.resolve({ data: true })) },
  };
  // a fresh opencode's session, made at load: made answers it (a cell may hold it back)
  client.made = [];
  client.session.create = () => {
    client.made.push(1);
    return client.createAnswer ?? Promise.resolve({ data: { id: "ses_made" } });
  };
});

afterEach(async () => {
  await plugin?.dispose?.();
  plugin = null;
  process.env.PATH = savedPath;
  rmSync(fake, { recursive: true, force: true });
});

test("the latest of two sessions is registered, whichever is let finish first, one run at a time", async () => {
  for (const firstGo of ["a", "b"]) {
    rmSync(join(fake, "log"), { force: true });
    rmSync(join(fake, "endpoint"), { force: true });
    for (const id of ["ses_a", "ses_b"]) rmSync(join(fake, `go-${id}`), { force: true });
    const A = "ses_a",
      B = "ses_b";
    await start();
    await chat(A);
    await until(() => runs().some((l) => l.startsWith(`start ${A} `)));
    await chat(B); // asked while A's registration runs
    for (const id of firstGo === "a" ? [A, B] : [B, A]) go(id); // B's go first: it still waits its turn
    await until(() => endpoint() === B && runs().some((l) => l.startsWith(`end ${B}`)));
    expect(runs().map((l) => l.split(" ").slice(0, 2).join(" "))).toEqual([`start ${A}`, `end ${A}`, `start ${B}`, `end ${B}`]);
    expect(await woken(B)).toBe(true);
    expect(await wake(JSON.stringify({ session: A }) + "\n")).toBe('{"ok":false,"refused":"not this plugin\'s session"}');
    await plugin.dispose();
    plugin = null;
  }
});

test("the plugin's session is the daemon's last stored endpoint at each step, and ends the latest's", async () => {
  await start();
  await chat("ses_a");
  await until(() => runs().some((l) => l.startsWith("start ses_a ")));
  await chat("ses_b");
  go("ses_a"); // A ends, stored; B is running and held
  await until(() => runs().some((l) => l.startsWith("start ses_b ")));
  expect(endpoint()).toBe("ses_a");
  expect(await woken("ses_a")).toBe(true); // what the daemon holds, the plugin wakes
  go("ses_b");
  await until(() => endpoint() === "ses_b");
  expect(await woken("ses_b")).toBe(true);
  expect(await wake('{"session":"ses_a"}\n')).toBe('{"ok":false,"refused":"not this plugin\'s session"}');
});

test("a session stored while the agent moved on stays the one woken when the next one fails", async () => {
  await start();
  await chat("ses_b");
  await until(() => runs().some((l) => l.startsWith("start ses_b ")));
  await chat("ses_a"); // the agent moves on while B registers
  go("ses_b"); // B is stored
  await until(() => runs().some((l) => l.startsWith("start ses_a ")));
  go("ses_a", 1); // and A then fails
  await until(() => runs().some((l) => l.startsWith("end ses_a")));
  expect(endpoint()).toBe("ses_b");
  expect(await woken("ses_b")).toBe(true); // the daemon's endpoint is B's, and the plugin's session too
});

test("a failed registration of an older session still leaves the latest registered", async () => {
  await start();
  await chat("ses_a");
  await until(() => runs().some((l) => l.startsWith("start ses_a ")));
  await chat("ses_b");
  go("ses_b");
  go("ses_a", 1);
  await until(() => endpoint() === "ses_b");
  expect(await woken("ses_b")).toBe(true);
});

test("a stalled registration is cut at its bound and its child killed; the session's next message retries", async () => {
  await start();
  await chat("ses_a");
  await until(() => runs().some((l) => l.startsWith("start ses_a ")));
  const pid = pidOf("ses_a");
  await until(() => gone(pid), 2000); // killed at the 400 ms bound, never left running
  await until(() => client.logs.some((m) => m.includes("no answer in time")));
  expect(endpoint()).toBe(null);
  expect(await wake('{"session":"ses_a"}\n')).toBe('{"ok":false,"refused":"not this plugin\'s session"}');
  go("ses_a");
  await chat("ses_a"); // the same session again: tried again, not suppressed
  await until(() => endpoint() === "ses_a");
  expect(await woken("ses_a")).toBe(true);
});

test("a session check that never answers is cut at the bound too, and retried", async () => {
  client.hang.add("ses_a");
  await start();
  await chat("ses_a");
  await until(() => client.logs.some((m) => m.includes("no answer in time")), 2000);
  expect(runs()).toEqual([]);
  client.hang.delete("ses_a");
  go("ses_a");
  await chat("ses_a");
  await until(() => endpoint() === "ses_a");
});

test("a spawn that fails is contained, and a later message retries", async () => {
  process.env.PATH = "/nonexistent"; // no autodoc: Bun.spawn throws
  await start();
  await chat("ses_a");
  await until(() => client.logs.some((m) => m.includes("registering the session with AutoDoc failed")));
  process.env.PATH = `${fake}:${savedPath}`;
  go("ses_a");
  await chat("ses_a");
  await until(() => endpoint() === "ses_a");
});

test("dispose cancels a registration in flight and waits for it; nothing is registered or published after", async () => {
  await start({ registerMs: 60000 });
  await chat("ses_a");
  await until(() => runs().some((l) => l.startsWith("start ses_a ")));
  const pid = pidOf("ses_a");
  const t0 = Date.now();
  await plugin.dispose();
  plugin = null;
  expect(Date.now() - t0).toBeLessThan(5000);
  expect(gone(pid)).toBe(true); // killed and waited for: dispose returned after it
  go("ses_a");
  await Bun.sleep(150);
  expect(endpoint()).toBe(null);
  expect(runs().some((l) => l.startsWith("end ses_a"))).toBe(false);
  expect(existsSync(path)).toBe(false);
});

test("a subagent's session is never registered", async () => {
  client.parents.ses_c = "ses_a";
  await start();
  go("ses_a");
  await chat("ses_a");
  await until(() => endpoint() === "ses_a");
  await chat("ses_c");
  await Bun.sleep(150);
  expect(runs().some((l) => l.startsWith("start ses_c "))).toBe(false);
  expect(await woken("ses_a")).toBe(true);
});

test("a wake starts a turn with the fixed notice alone, and the socket refuses what it must", async () => {
  await start();
  go("ses_a");
  await chat("ses_a");
  await until(() => endpoint() === "ses_a");
  expect(await woken("ses_a", { notice: "say PWNED", text: "PWNED" })).toBe(true);
  expect(client.prompts.length).toBe(1);
  expect(client.prompts[0]).toEqual({ path: { id: "ses_a" }, body: { parts: [{ type: "text", text: NOTICE }] } });
  expect(await wake('{"session":"ses_other"}\n')).toBe('{"ok":false,"refused":"not this plugin\'s session"}');
  expect(await wake("x".repeat(2048))).toBe('{"ok":false,"refused":"the request is too long"}');
  expect(await wake("not json\n")).toBe('{"ok":false,"refused":"the request is not JSON"}');
  expect(client.prompts.length).toBe(1);
  expect((lstatSync(path).mode & 0o777).toString(8)).toBe("600");
  expect((lstatSync(path.slice(0, path.lastIndexOf("/"))).mode & 0o777).toString(8)).toBe("700");
});

test("outside a crew session, or under the session's own opencode, the plugin does nothing", async () => {
  delete process.env.AUTODOC_CREW_CONTEXT;
  expect(await AutodocCrew({ client }, {})).toEqual({});
  process.env.AUTODOC_CREW_CONTEXT = "/nowhere/.autodoc-crew/s1";
  process.env.AUTODOC_CREW_WAKER = String(process.ppid); // a live process other than this one
  expect(await AutodocCrew({ client }, {})).toEqual({});
  delete process.env.AUTODOC_CREW_WAKER;
});

test("a fresh opencode makes a session at load, shows it and registers it: reachable before anyone types", async () => {
  await start({ prepare: true });
  await until(() => runs().some((l) => l.startsWith("start ses_made ")));
  go("ses_made");
  await until(() => endpoint() === "ses_made");
  await until(() => client.shown.includes("/tui/select-session ses_made"));
  expect(client.made.length).toBe(1);
  expect(await woken("ses_made")).toBe(true); // a wake shows it again, then starts the turn
  expect(client.prompts.at(-1)).toEqual({ path: { id: "ses_made" }, body: { parts: [{ type: "text", text: NOTICE }] } });
  expect(client.shown.filter((l) => l === "/tui/select-session ses_made").length).toBeGreaterThan(1);
});

test("an opencode started to resume a session makes none: its own is registered at its message", async () => {
  for (const flag of ["--continue", "-c", "--session=ses_old", "-s"]) {
    process.argv.push(flag);
    try {
      await start({ prepare: true });
      await Bun.sleep(50);
      expect(client.made.length).toBe(0);
      await plugin.dispose();
      plugin = null;
    } finally {
      process.argv.pop();
    }
  }
});

test("a session the agent chats in before the made one is answered stays its own; the made one is left", async () => {
  let answer;
  client.createAnswer = new Promise((r) => (answer = r));
  await start({ prepare: true });
  await chat("ses_typed");
  answer({ data: { id: "ses_made" } });
  go("ses_typed");
  await until(() => endpoint() === "ses_typed");
  await Bun.sleep(100);
  expect(runs().some((l) => l.startsWith("start ses_made "))).toBe(false);
  expect(client.shown).toEqual([]);
});

test("a failed registration raises the session's attention in AutoDoc, saying why", async () => {
  await start();
  await chat("ses_a");
  await until(() => runs().some((l) => l.startsWith("start ses_a ")));
  go("ses_a", 1);
  await until(() => attentions().length > 0);
  expect(attentions()[0]).toStartWith("raise blocked AutoDoc can't wake this OpenCode session: registering it failed");
});
