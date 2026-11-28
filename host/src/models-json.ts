import { randomUUID } from "node:crypto";
import { mkdir, readFile, rename, unlink, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { HostError } from "./env.js";
import { isObject, type JsonObject } from "./framing.js";

export const MODELS_INVALID = "models.json is not valid JSON. Ask the agent to fix it.";

/**
 * The `apiKey` Pigkin stores for an endpoint that needs none, such as a local server. Pi requires a non-empty key
 * for a `models.json` provider and sends it in chat, so a keyless provider gets this one, as Pi's docs suggest for
 * Ollama; the server ignores it. The connection check sends it too, so it tests what chat will send.
 */
export const NO_API_KEY = "none";

export interface ModelsJson {
  /** The whole document; unknown keys are preserved on write. */
  root: JsonObject;
  providers: JsonObject;
}

/** The verbatim file, so a save that fails later can put it back; undefined when it never existed. */
export async function readModelsJsonText(configDir: string): Promise<string | undefined> {
  try {
    return await readFile(join(configDir, "models.json"), "utf8");
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code === "ENOENT") return undefined;
    throw error;
  }
}

/** Reads `models.json`; a missing file is `{}`. Never returns a document it could not parse. */
export async function readModelsJson(configDir: string): Promise<ModelsJson> {
  const text = await readModelsJsonText(configDir);
  if (text === undefined) return { root: {}, providers: {} };
  let root: unknown;
  try {
    root = JSON.parse(text.replace(/^\uFEFF/, ""));
  } catch {
    throw new HostError("models_invalid", MODELS_INVALID, { key: "models_invalid" });
  }
  if (!isObject(root)) throw new HostError("models_invalid", MODELS_INVALID, { key: "models_invalid" });
  const providers = root.providers ?? {};
  if (!isObject(providers)) throw new HostError("models_invalid", MODELS_INVALID, { key: "models_invalid" });
  return { root, providers };
}

// Save order is queued: two saves must not race to publish the same file, and one failure must not block the next.
let writes: Promise<void> = Promise.resolve();

/** Replaces `models.json` atomically so Pi never reads a half-written file; undefined restores a missing file. */
export async function writeModelsJsonText(configDir: string, text: string | undefined): Promise<void> {
  const write = writes.then(async () => {
    const path = join(configDir, "models.json");
    if (text === undefined) {
      await unlink(path).catch((error) => {
        if ((error as NodeJS.ErrnoException).code !== "ENOENT") throw error;
      });
      return;
    }
    await mkdir(configDir, { recursive: true });
    const temp = `${path}.${process.pid}.${randomUUID()}.tmp`;
    await writeFile(temp, text);
    await rename(temp, path);
  });
  writes = write.catch(() => undefined);
  return write;
}

/** Replaces `models.json` atomically so Pi never reads a half-written file. */
export async function writeModelsJson(configDir: string, models: ModelsJson): Promise<void> {
  await writeModelsJsonText(configDir, `${JSON.stringify({ ...models.root, providers: models.providers }, null, 2)}\n`);
}
