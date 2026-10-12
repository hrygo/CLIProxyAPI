/** Probe the exact external CodeMode source with explicit limits; no Provider traffic. */
import { createHash } from "node:crypto";
import { readFile, writeFile } from "node:fs/promises";
import { createRequire } from "node:module";
import { resolve, join } from "node:path";
import { pathToFileURL } from "node:url";

const args = process.argv.slice(2).filter((value) => value !== "");
const option = (name) => {
  const index = args.indexOf(name);
  if (index < 0 || !args[index + 1]) throw new Error(`Missing ${name}`);
  return resolve(args[index + 1]);
};
const root = option("--sdk-root");
const output = option("--output");
const pin = JSON.parse(await readFile(new URL("./opencode-codemode-source.json", import.meta.url), "utf8"));
const sourceDigests = {};
for (const [file, expected] of Object.entries(pin.files)) {
  sourceDigests[file] = createHash("sha256").update(await readFile(join(root, file))).digest("hex");
  if (sourceDigests[file] !== expected) throw new Error(`source digest mismatch: ${file}`);
}
const dependencyVersions = {};
for (const [name, expected] of Object.entries(pin.dependencies)) {
  dependencyVersions[name] = JSON.parse(await readFile(join(root, "node_modules", name, "package.json"), "utf8")).version;
  if (dependencyVersions[name] !== expected) throw new Error(`dependency version mismatch: ${name}`);
}
const require = createRequire(join(root, "package.json"));
const { Effect } = await import(pathToFileURL(require.resolve("effect")).href);
const { CodeMode, Tool } = await import(pathToFileURL(join(root, "src/index.ts")).href);
const cases = [];
const input = { type: "object", properties: {}, additionalProperties: false };
const record = (name, result, expected, extra = {}) => {
  const observation = { name, result, passed: expected, ...extra };
  cases.push(observation);
  console.log(JSON.stringify(observation));
};

let started = performance.now();
let result = await Effect.runPromise(CodeMode.make({ limits: { timeoutMs: 100 } }).execute("while (true) {}"));
record("cpu-loop-timeout", result, !result.ok && result.error.kind === "TimeoutExceeded", { elapsed_ms: performance.now() - started });

let calls = 0;
result = await Effect.runPromise(CodeMode.make({
  limits: { timeoutMs: 500, maxToolCalls: 2 },
  tools: { fixture: { read: Tool.make({ description: "Read a synthetic record.", input, run: () => Effect.sync(() => { calls++; return { value: 1 }; }) }) } },
}).execute("for(let i=0;i<3;i++){await tools.fixture.read({});} return 1;"));
record("runtime-call-budget", result, !result.ok && result.error.kind === "ToolCallLimitExceeded" && calls === 2, { actual_calls: calls });

let active = 0, released = 0;
result = await Effect.runPromise(CodeMode.make({
  limits: { timeoutMs: 100 },
  tools: { fixture: { wait: Tool.make({ description: "Wait until interrupted.", input, run: () => Effect.sync(() => { active++; }).pipe(
    Effect.andThen(Effect.never), Effect.ensuring(Effect.sync(() => { active--; released++; })),
  ) }) } },
}).execute("return await tools.fixture.wait({});"));
record("timeout-releases-tool", result, !result.ok && result.error.kind === "TimeoutExceeded" && active === 0 && released === 1, { active, released });

const outputLimit = 256;
result = await Effect.runPromise(CodeMode.make({ limits: { maxOutputBytes: outputLimit } }).execute('return "x".repeat(1024);'));
const serializedValueBytes = Buffer.byteLength(JSON.stringify(result.value ?? null));
record("serialized-output-budget", result, serializedValueBytes <= outputLimit, { requested_bytes: outputLimit, serialized_value_bytes: serializedValueBytes });

const summary = { scope: "external-codemode-sdk-with-explicit-limits", source_tag: pin.source_tag,
  source_url: pin.source_url, source_digests: sourceDigests,
  runtime: { bun: process.versions.bun, dependencies: dependencyVersions },
  cases, gate_status: "unverified", native_client_verified: false, provider_usage: null };
await writeFile(output, JSON.stringify(summary, null, 2), { flag: "wx", mode: 0o600 });
if (cases.some((item) => !item.passed)) process.exitCode = 1;
