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

export const AutodocCrew = async ({ client }) => {
  if (!process.env.AUTODOC_CREW_CONTEXT) return {};
  const holder = Number(process.env.AUTODOC_CREW_WAKER);
  if (holder && holder !== process.pid && alive(holder)) return {}; // started under the session's own opencode
  process.env.AUTODOC_CREW_WAKER = String(process.pid);
  const log = (level, message) =>
    client.app.log({ body: { service: "autodoc-crew", level, message } }).catch(() => {});
  const dir = socketDir();
  if (!dir) {
    log("error", "no socket folder only this user can reach: AutoDoc can't wake this session");
    return {};
  }
  const path = `${dir}/${process.pid}.sock`;
  try {
    unlinkSync(path); // this pid's own, left by a process that held it before: never another's
  } catch {}
  let session = null; // the session registered with AutoDoc
  let registering = null;
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
  const register = async (id) => {
    const info = await client.session.get({ path: { id } }).catch(() => null);
    if (!info?.data || info.data.parentID) return; // a subagent's session is not the agent's
    const p = Bun.spawn(["autodoc", "crew", "endpoint", "--opencode", id], {
      stdin: "ignore",
      stdout: "ignore",
      stderr: "pipe",
    });
    const why = (await new Response(p.stderr).text()).trim();
    if ((await p.exited) === 0) session = id;
    else log("warn", `registering the session with AutoDoc failed: ${why.slice(0, 300)}`);
  };
  return {
    "chat.message": async (input) => {
      const id = input.sessionID;
      if (!id || id === session || id === registering) return;
      registering = id; // in the background: the message never waits on AutoDoc
      register(id).finally(() => {
        if (registering === id) registering = null;
      });
    },
    "shell.env": async (_input, output) => {
      output.env.AUTODOC_CREW_WAKER = String(process.pid);
    },
    dispose: async () => {
      server.stop(true);
      remove();
    },
  };
};
