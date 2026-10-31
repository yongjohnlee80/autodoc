// AutoDoc's crew plugin for OpenCode (docs/decisions/crew.md#wakers-and-the-wake-scheduler).
// AutoDoc writes it (`autodoc crew plugin --opencode`) and its host loads it through
// $OPENCODE_CONFIG_CONTENT; it writes nothing into the project. In the agent's own opencode process
// it:
//   - registers the session it chats in with AutoDoc (`autodoc crew endpoint --opencode <session>`),
//     at each new top-level session it chats in (a subagent's is not one); a fresh opencode, one not
//     started to resume a session, has none until its first message, so the plugin makes one at load,
//     shows it in the TUI and registers it, and the agent is reachable before anyone types;
//   - raises the session's attention in AutoDoc when a registration fails, so the window shows why
//     the agent can't be woken;
//   - reports the agent's permission prompts: one showing raises the session's permission attention
//     in AutoDoc, saying what it asks, and the last one answered clears it; and says it is alive
//     every 5 s, so a window presses Enter into the agent only while it reports, and no prompt is
//     there to approve;
//   - listens on a unix socket, named by its pid, in a folder of its own that no one else can reach;
//     asked to wake its session, it shows it in the TUI and starts a turn with the fixed notice below
//     (promptAsync: a busy session takes it in at its next step), and answers one line.
// It never wakes with text it is sent, nor for any session but its own: any process of the user can
// connect, and the worst it can do is a spurious "mail is waiting". It does nothing outside an
// AutoDoc crew session ($AUTODOC_CREW_CONTEXT), nor in an opencode started under one that does
// ($AUTODOC_CREW_WAKER, which it sets for every process it starts): one plugin per session wakes it.
import { chmodSync, lstatSync, mkdirSync, unlinkSync } from "node:fs";

// NOTICE is crew.WakeNotice: a cell holds the two the same.
const NOTICE = "AutoDoc mail is waiting for you. Run: autodoc mail receive";
const MAX_REQUEST = 1024;

const alive = (pid) => {
  try {
    process.kill(pid, 0);
    return true;
  } catch (e) {
    return e.code === "EPERM";
  }
};

// socketDir is the plugin's folder, made 0700, or null when it is not one only this user can reach.
const socketDir = () => {
  const uid = process.getuid();
  let dir = `/tmp/autodoc-opencode-${uid}`;
  try {
    if (lstatSync(`/run/user/${uid}`).isDirectory()) dir = `/run/user/${uid}/autodoc-opencode`;
  } catch {}
  try {
    mkdirSync(dir, { mode: 0o700 });
  } catch (e) {
    if (e.code !== "EEXIST") return null;
  }
  const st = lstatSync(dir);
  if (!st.isDirectory() || st.uid !== uid || (st.mode & 0o077) !== 0) return null;
  return dir;
};

// REGISTER_MS bounds one registration attempt, the session check and the CLI's run together: a
// daemon that takes the connection and never answers must not hold registration forever.
const REGISTER_MS = 15000;

// aborted rejects once signal aborts, so an await on something that ignores signal still ends.
const aborted = (signal) =>
  new Promise((_, reject) => {
    if (signal.aborted) return reject(new Error("aborted"));
    signal.addEventListener("abort", () => reject(new Error("aborted")), { once: true });
  });

// RESUMES are opencode's flags that start it on a session it already has: that one is the agent's, and
// the plugin makes none at load.
const RESUMES = new Set(["-c", "--continue", "-s", "--session", "--fork"]);
const resumed = (argv) => argv.some((a) => RESUMES.has(a.split("=")[0]));

// cliModel is the model opencode was started with (`--model provider/model`, `--model=…`, `-m …`), as
// a prompt names one, or null for none. A wake names it: a session the plugin made at load has had no
// message, and a prompt that names no model runs on the configured default, which may not be the one
// the agent was started on (or one that can answer at all).
const cliModel = (argv) => {
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    let v = null;
    if (a === "--model" || a === "-m") v = argv[i + 1];
    else if (a.startsWith("--model=")) v = a.slice("--model=".length);
    else if (a.startsWith("-m=")) v = a.slice("-m=".length);
    if (typeof v !== "string") continue;
    const slash = v.indexOf("/");
    if (slash > 0 && slash < v.length - 1) return { providerID: v.slice(0, slash), modelID: v.slice(slash + 1) };
  }
  return null;
};

// WAKE_ERROR_MS is how long after a wake a session error is taken for the woken turn's own: the turn
// the notice starts runs at once, or at a busy session's next step.
const WAKE_ERROR_MS = 120000;

// HEARTBEAT_MS is how often the plugin says it is alive: well within AutoDoc's 15 s, past which a
// reporter is taken for gone.
const HEARTBEAT_MS = 5000;

// SHOW_AT are the waits before each ask that the TUI show the session made at load: the TUI may not
// listen yet when the plugin loads, and an ask it misses is lost.
const SHOW_AT = [0, 500, 1000, 2000, 4000];

export const AutodocCrew = async ({ client }, options = {}) => {
  if (!process.env.AUTODOC_CREW_CONTEXT) return {};
  const holder = Number(process.env.AUTODOC_CREW_WAKER);
  if (holder && holder !== process.pid && alive(holder)) return {}; // started under the session's own opencode
  process.env.AUTODOC_CREW_WAKER = String(process.pid);
  const registerMs = Number(options.registerMs) || REGISTER_MS;
  const argv = options.argv ?? process.argv;
  const model = cliModel(argv);
  let wokeAt = 0; // when the last wake's turn was started (onWakeTurn)
  let wakeRaised = false; // whether a woken turn's error raised the session's attention
  const log = (level, message) =>
    Promise.resolve()
      .then(() => client.app.log({ body: { service: "autodoc-crew", level, message } }))
      .catch(() => {});
  const dir = socketDir();
  if (!dir) {
    log("error", "no socket folder only this user can reach: AutoDoc can't wake this session");
    return {};
  }
  const path = `${dir}/${process.pid}.sock`;
  try {
    unlinkSync(path); // this pid's own, left by a process that held it before: never another's
  } catch {}
  // Registration is one owned lifecycle: one worker at a time, always toward the latest top-level
  // session the agent chats in (desired), so the last endpoint written is the latest's. Each
  // registration that succeeds is published (session, the one the plugin wakes): run one at a
  // time, the plugin's session is always the daemon's last stored endpoint, even when a later one
  // then fails. Each attempt is bounded, owns its child, and is cancelled by dispose; after a
  // failure, the session's next message tries again.
  let session = null; // the session registered with AutoDoc, the one a wake may name
  let desired = null; // the latest top-level session asked for
  let worker = null; // the running registration, if any
  let attemptAbort = null; // cancels the running attempt
  let disposed = false;
  const reply = (s, answer) => {
    s.write(JSON.stringify(answer) + "\n");
    s.end();
  };
  const server = Bun.listen({
    unix: path,
    socket: {
      open(s) {
        s.data = { buf: "", done: false };
      },
      data(s, chunk) {
        if (s.data.done) return;
        s.data.buf += chunk.toString();
        if (s.data.buf.length > MAX_REQUEST) {
          s.data.done = true;
          return reply(s, { ok: false, refused: "the request is too long" });
        }
        const nl = s.data.buf.indexOf("\n");
        if (nl < 0) return; // only a whole line is acted on
        s.data.done = true;
        let req;
        try {
          req = JSON.parse(s.data.buf.slice(0, nl));
        } catch {
          return reply(s, { ok: false, refused: "the request is not JSON" });
        }
        if (!session || req?.session !== session) return reply(s, { ok: false, refused: "not this plugin's session" });
        show(session); // the turn the notice starts is the one the user sees
        const body = { parts: [{ type: "text", text: NOTICE }], ...(model ? { model } : {}) };
        client.session.promptAsync({ path: { id: session }, body }).then(
          (r) => {
            if (!r.error) wokeAt = Date.now();
            reply(s, r.error ? { ok: false, error: "promptAsync failed" } : { ok: true });
          },
          () => reply(s, { ok: false, error: "promptAsync failed" }),
        );
      },
      error() {},
    },
  });
  chmodSync(path, 0o600);
  const remove = () => {
    try {
      unlinkSync(path);
    } catch {}
  };
  process.on("exit", remove);
  // show asks opencode's TUI to show session id: POST /tui/select-session on the plugin's own client,
  // the TUI's server being reachable only through it (this client has no method for the route).
  // Without the route the session is still woken; it shows once the user opens it.
  const show = (id) => {
    const http = client._client;
    if (typeof http?.post !== "function") return Promise.resolve(false);
    return Promise.resolve()
      .then(() => http.post({ url: "/tui/select-session", body: { sessionID: id }, headers: { "content-type": "application/json" } }))
      .then((r) => !r?.error && r?.data === true, () => false);
  };
  // crewAttention runs `autodoc crew attention <args>`, one run after another (so a clear never
  // overtakes the raise before it), each bounded as a registration is; a run's failure is only
  // logged.
  let attentionRuns = Promise.resolve();
  const crewAttention = (...args) => {
    attentionRuns = attentionRuns.then(
      () =>
        new Promise((done) => {
          let p;
          try {
            p = Bun.spawn(["autodoc", "crew", "attention", ...args], {
              env: process.env,
              stdin: "ignore",
              stdout: "ignore",
              stderr: "ignore",
            });
          } catch (e) {
            log("warn", `the session's attention (${args[0]}) failed: ${String(e).slice(0, 300)}`);
            return done();
          }
          const timer = setTimeout(() => p.kill(), registerMs);
          p.exited.finally(() => clearTimeout(timer)).then(done, done);
        }),
    );
    return attentionRuns;
  };
  // the reporter's heartbeat: while the plugin runs it says so every heartbeatMs, so AutoDoc trusts
  // this session's prompts as reported (crew.list's prompts_reported) only while it does
  const heartbeatMs = options.heartbeatMs ?? HEARTBEAT_MS;
  const heartbeat = heartbeatMs > 0 ? setInterval(() => crewAttention("alive"), heartbeatMs) : null;
  heartbeat?.unref?.();
  // attention tells AutoDoc's window that this session can't be woken, and why.
  const attention = (why) => crewAttention("raise", "blocked", `AutoDoc can't wake this OpenCode session: ${why}`);
  // asking are the permission prompts the agent shows now, by request: the first raises the
  // session's permission attention, saying what it asks; the last one answered clears it. A window
  // presses Enter into this agent only while none is showing (crew.list's prompts_reported).
  const asking = new Map();
  // a woken turn whose model refused it (no credits, no such model) did nothing: the wake was
  // accepted, so AutoDoc would wait on a receive that never comes. The session's error, within
  // WAKE_ERROR_MS of a wake, raises its attention, saying what the model said; the next turn the
  // model completes clears it, unless a permission prompt shows.
  const onWakeTurn = (event) => {
    const p = event?.properties ?? {};
    if (event?.type === "session.error" && p.sessionID && p.sessionID === session && wokeAt && Date.now() - wokeAt < WAKE_ERROR_MS) {
      wokeAt = 0;
      wakeRaised = true;
      const e = p.error ?? {};
      const said = e.data?.message ?? e.message ?? e.name ?? "an error";
      attention(`the woken session's model refused the wake: ${String(said).slice(0, 200)}`);
    } else if (event?.type === "message.updated" && wakeRaised) {
      const info = p.info ?? {};
      if (info.sessionID === session && info.role === "assistant" && info.time?.completed && !info.error && asking.size === 0) {
        wakeRaised = false;
        crewAttention("clear");
      }
    }
  };
  const onPermission = (event) => {
    const p = event?.properties ?? {};
    if (event?.type === "permission.asked" && p.id && !asking.has(p.id)) {
      asking.set(p.id, true);
      if (asking.size === 1) {
        const what = [p.permission, (p.patterns ?? []).join(" ")].filter(Boolean).join(": ");
        crewAttention("raise", "permission", what.slice(0, 300) || "a permission");
      }
    } else if (event?.type === "permission.replied" && asking.delete(p.requestID) && asking.size === 0) {
      crewAttention("clear");
    }
  };
  // attempt registers id, within registerMs: "ok", "child" (a subagent's session, never the
  // agent's), or "failed". The child it starts is its own: killed at the bound or on dispose, and
  // waited for, so none outlives the attempt.
  const attempt = async (id) => {
    const ac = new AbortController();
    attemptAbort = ac;
    const timer = setTimeout(() => ac.abort(), registerMs);
    let p = null;
    try {
      const get = client.session.get({ path: { id }, signal: ac.signal });
      get.catch(() => {}); // one that loses the race to the bound rejects later, unheard
      const info = await Promise.race([get, aborted(ac.signal)]);
      if (!info?.data) return "failed";
      if (info.data.parentID) return "child";
      // env: the process's environment as it is now ($AUTODOC_CREW_CONTEXT, and PATH as opencode left
      // it), which Bun then resolves autodoc in: without it, Bun looks in the PATH it started with
      p = Bun.spawn(["autodoc", "crew", "endpoint", "--opencode", id], {
        env: process.env,
        stdin: "ignore",
        stdout: "ignore",
        stderr: "pipe",
      });
      ac.signal.addEventListener("abort", () => p.kill(), { once: true });
      if (ac.signal.aborted) p.kill();
      const [why, code] = await Promise.all([new Response(p.stderr).text().catch(() => ""), p.exited]);
      if (code === 0 && !ac.signal.aborted) return "ok";
      if (disposed) return "failed";
      const reason = ac.signal.aborted ? "no answer in time" : why.trim().slice(0, 300);
      log("warn", `registering the session with AutoDoc failed: ${reason}`);
      attention(`registering it failed: ${reason}`);
      return "failed";
    } catch (e) {
      if (p) {
        p.kill();
        await p.exited.catch(() => {});
      }
      if (disposed) return "failed";
      const reason = ac.signal.aborted ? "no answer in time" : String(e).slice(0, 300);
      log("warn", `registering the session with AutoDoc failed: ${reason}`);
      attention(`registering it failed: ${reason}`);
      return "failed";
    } finally {
      clearTimeout(timer);
      if (attemptAbort === ac) attemptAbort = null;
    }
  };
  const run = async () => {
    while (!disposed && desired && desired !== session) {
      const target = desired;
      const r = await attempt(target);
      if (disposed) return;
      if (r === "ok") session = target; // as the daemon now has it: the loop goes on to the latest
      if (r === "child" && desired === target) desired = session;
      if (r === "failed" && desired === target) return; // its next message tries again
    }
  };
  const want = (id) => {
    desired = id;
    if (!worker)
      worker = run()
        .catch((e) => log("warn", `registration stopped: ${String(e).slice(0, 300)}`))
        .finally(() => {
          worker = null;
        });
  };
  // prepare makes the agent reachable before anyone types: a fresh opencode has no session until its
  // first message, so the plugin makes one, wants it registered, and asks the TUI to show it, so the
  // user's own first message lands in it too and no empty session is left. A session the agent chats
  // in first is its own: the one made is then left alone.
  const prepare = async () => {
    try {
      const made = await client.session.create({ body: {} });
      const id = made?.data?.id;
      if (!id) {
        log("warn", "making a session at load failed: AutoDoc can wake this agent once it has a message");
        return;
      }
      if (disposed || desired) return;
      want(id);
      for (const ms of SHOW_AT) {
        await Bun.sleep(ms);
        if (disposed || desired !== id) return;
        await show(id);
      }
    } catch (e) {
      log("warn", `making a session at load failed: ${String(e).slice(0, 300)}`);
    }
  };
  if (options.prepare !== false && !resumed(argv)) prepare();
  return {
    "chat.message": async (input) => {
      const id = input.sessionID;
      if (disposed || !id || id === session || (worker && id === desired)) return; // coalesced into the running one
      want(id); // in the background: the message never waits on AutoDoc
    },
    event: async ({ event }) => {
      if (disposed) return;
      onPermission(event);
      onWakeTurn(event);
    },
    "shell.env": async (_input, output) => {
      output.env.AUTODOC_CREW_WAKER = String(process.pid);
    },
    dispose: async () => {
      disposed = true;
      if (heartbeat) clearInterval(heartbeat);
      attemptAbort?.abort();
      await worker;
      server.stop(true);
      remove();
    },
  };
};
