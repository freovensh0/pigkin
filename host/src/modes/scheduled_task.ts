import { spawn } from "node:child_process";
import { readdir } from "node:fs/promises";
import { join } from "node:path";
import { HostError, piEntry } from "../env.js";
import type { JsonObject } from "../framing.js";
import type { ModeContext } from "../mode.js";
import { termuxSessionEnv } from "../termux-session.js";
import { commandFailed } from "./ensure.js";

/** Longest a one-off run gets before it is killed, matching the Go host's five minutes. */
const TIMEOUT_MS = 5 * 60 * 1000;
/** Pi's stderr is kept only for a bounded failure diagnostic. */
const STDERR_LIMIT = 4096;
/** The characters Pi accepts in a --session-id. */
const SESSION_ID = /^[A-Za-z0-9._-]+$/;

function prompt(params: JsonObject): string {
  const value = params.prompt;
  if (typeof value !== "string" || value.trim() === "") {
    throw new HostError("bad_request", "A prompt is required", { key: "scheduled_task_prompt_required" });
  }
  return value;
}

/** An optional string param, trimmed; blank and non-strings are omitted. */
function optionalText(params: JsonObject, key: string): string | undefined {
  const value = params[key];
  if (typeof value !== "string") return undefined;
  const text = value.trim();
  return text === "" ? undefined : text;
}

/** Where a task's runs keep their session, in a folder of their own so they don't mix with the user's chats. */
function sessionsDir(ctx: ModeContext): string {
  return join(ctx.env.configDir, "scheduled");
}

/**
 * The session file of the task [id] in [dir], as Pi names it `<timestamp>_<id>.jsonl`; undefined before its first run.
 */
async function findSession(dir: string, id: string): Promise<string | undefined> {
  const names = await readdir(dir).catch(() => []);
  const name = names.find((entry) => entry.endsWith(`_${id}.jsonl`));
  return name === undefined ? undefined : join(dir, name);
}

/**
 * Runs a prompt once with the bundled Pi and returns its final stdout. Each task keeps one session that its runs
 * append to, so the app can open it as the task's chat; [sessionPath] reopens it and [id] starts it on the first run.
 * Mirrors backend/internal/modes/scheduled_task.go.
 */
export async function scheduledTaskRun(ctx: ModeContext): Promise<JsonObject> {
  const text = prompt(ctx.params);
  const model = optionalText(ctx.params, "model");
  const thinking = optionalText(ctx.params, "thinking");
  const id = optionalText(ctx.params, "id");
  const saved = optionalText(ctx.params, "sessionPath");
  const dir = sessionsDir(ctx);
  const args = [await piEntry(ctx.env.root)];
  if (model) args.push("--model", model);
  if (thinking) args.push("--thinking", thinking);
  if (saved) {
    args.push("--session", saved);
  } else if (id !== undefined && SESSION_ID.test(id)) {
    args.push("--session-dir", dir, "--session-id", id);
  }
  args.push("--print", text);
  const env = {
    ...(await termuxSessionEnv(ctx.env)),
    PIGKIN_ROOT: ctx.env.root,
    HOME: ctx.env.home,
    PREFIX: ctx.env.prefix,
  };
  const result = await runPi(ctx, args, env);
  const sessionPath = saved ?? (id === undefined ? undefined : await findSession(dir, id));
  return { result, sessionPath, cwd: ctx.env.home };
}

async function runPi(ctx: ModeContext, args: string[], env: NodeJS.ProcessEnv): Promise<string> {
  const child = spawn(process.execPath, args, { cwd: ctx.env.home, env, stdio: ["ignore", "pipe", "pipe"] });
  let stdout = "";
  let stderr = "";
  let timedOut = false;
  const timer = setTimeout(() => {
    timedOut = true;
    child.kill("SIGKILL");
  }, TIMEOUT_MS);
  child.stdout.setEncoding("utf8");
  child.stdout.on("data", (chunk: string) => {
    stdout += chunk;
  });
  child.stderr.setEncoding("utf8");
  child.stderr.on("data", (chunk: string) => {
    stderr = (stderr + chunk).slice(-STDERR_LIMIT);
  });
  try {
    const code = await new Promise<number | null>((resolve, reject) => {
      child.on("error", (error) => reject(new HostError("scheduled_task_failed", error.message)));
      child.on("close", resolve);
    });
    if (timedOut) {
      throw new HostError("scheduled_task_failed", "The task took longer than five minutes", { key: "scheduled_task_timed_out" });
    }
    if (code !== 0) throw commandFailed("scheduled_task_failed", "pi --print", code);
    return stdout.trim();
  } finally {
    clearTimeout(timer);
  }
}
