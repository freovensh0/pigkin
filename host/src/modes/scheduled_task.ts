import { spawn } from "node:child_process";
import { HostError, piEntry } from "../env.js";
import type { JsonObject } from "../framing.js";
import type { ModeContext } from "../mode.js";
import { termuxSessionEnv } from "../termux-session.js";
import { commandFailed } from "./ensure.js";

/** Longest a one-off run gets before it is killed, matching the Go host's five minutes. */
const TIMEOUT_MS = 5 * 60 * 1000;
/** Pi's stderr is kept only for a bounded failure diagnostic. */
const STDERR_LIMIT = 4096;

function prompt(params: JsonObject): string {
  const value = params.prompt;
  if (typeof value !== "string" || value.trim() === "") {
    throw new HostError("bad_request", "A prompt is required", { key: "scheduled_task_prompt_required" });
  }
  return value;
}

/** Runs a prompt once with the bundled Pi and returns its final stdout. Mirrors backend/internal/modes/scheduled_task.go. */
export async function scheduledTaskRun(ctx: ModeContext): Promise<JsonObject> {
  const text = prompt(ctx.params);
  const args = [await piEntry(ctx.env.root), "--print", text];
  const env = {
    ...(await termuxSessionEnv(ctx.env)),
    PIGKIN_ROOT: ctx.env.root,
    HOME: ctx.env.home,
    PREFIX: ctx.env.prefix,
  };
  return { result: await runPi(ctx, args, env) };
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
