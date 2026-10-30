// AutoDoc's crew plugin for OpenCode (ADR 1793271500 §4.7). AutoDoc writes it (`autodoc crew plugin
// --opencode`) and its host loads it through $OPENCODE_CONFIG_CONTENT; it writes nothing into the
// project. In the agent's own opencode process it:
//   - registers the session it chats in with AutoDoc (`autodoc crew endpoint --opencode <session>`),
//     at its first message and at each new top-level session (a subagent's is not one);
//   - listens on a unix socket, named by its pid, in a folder of its own that no one else can reach;
//     asked to wake its session, it starts a turn with the fixed notice below (promptAsync: a busy
//     session takes it in at its next step), and answers one line.
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

export const AutodocCrew = async ({ client }, options = {}) => {
  if (!process.env.AUTODOC_CREW_CONTEXT) return {};
  const holder = Number(process.env.AUTODOC_CREW_WAKER);
  if (holder && holder !== process.pid && alive(holder)) return {}; // started under the session's own opencode
  process.env.AUTODOC_CREW_WAKER = String(process.pid);
  const registerMs = Number(options.registerMs) || REGISTER_MS;
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
  // session the agent chats in (desired), so the last endpoint written is the latest's and session
  // (the one the plugin wakes) is published only for it. Each attempt is bounded, owns its child,
  // and is cancelled by dispose; after a failure, the session's next message tries again.
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
        client.session
          .promptAsync({ path: { id: session }, body: { parts: [{ type: "text", text: NOTICE }] } })
          .then(
            (r) => reply(s, r.error ? { ok: false, error: "promptAsync failed" } : { ok: true }),
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
      p = Bun.spawn(["autodoc", "crew", "endpoint", "--opencode", id], {
        stdin: "ignore",
        stdout: "ignore",
        stderr: "pipe",
      });
      ac.signal.addEventListener("abort", () => p.kill(), { once: true });
      if (ac.signal.aborted) p.kill();
      const [why, code] = await Promise.all([new Response(p.stderr).text().catch(() => ""), p.exited]);
      if (code === 0 && !ac.signal.aborted) return "ok";
      log("warn", `registering the session with AutoDoc failed: ${ac.signal.aborted ? "no answer in time" : why.trim().slice(0, 300)}`);
      return "failed";
    } catch (e) {
      if (p) {
        p.kill();
        await p.exited.catch(() => {});
      }
      log("warn", `registering the session with AutoDoc failed: ${ac.signal.aborted ? "no answer in time" : String(e).slice(0, 300)}`);
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
      if (r === "ok" && desired === target) session = target; // published for the latest alone
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
  return {
    "chat.message": async (input) => {
      const id = input.sessionID;
      if (disposed || !id || id === session || (worker && id === desired)) return; // coalesced into the running one
      want(id); // in the background: the message never waits on AutoDoc
    },
    "shell.env": async (_input, output) => {
      output.env.AUTODOC_CREW_WAKER = String(process.pid);
    },
    dispose: async () => {
      disposed = true;
      attemptAbort?.abort();
      await worker;
      server.stop(true);
      remove();
    },
  };
};
