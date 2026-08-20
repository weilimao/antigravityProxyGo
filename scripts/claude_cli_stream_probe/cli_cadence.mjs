// cli_cadence.mjs — spawn `claude -p ... --output-format stream-json`, timestamp
// every stdout chunk as it arrives. Shows whether the CLI dribbles events
// (streams) or emits them in one burst (buffered/non-streaming).
import { spawn } from "node:child_process";

const exe = "claude.cmd"; // resolved via PATH (git-bash spawns .cmd through the shim)
const t0 = Date.now();
const log = (s) => console.log(s);

log(`[probe] spawn ${exe} @ T+0ms`);

const child = spawn(exe, [
  "-p",
  "从1数到20,每个数字单独一行,只输出数字",
  "--output-format", "stream-json",
  "--verbose",
], { stdio: ["ignore", "pipe", "inherit"], shell: true });

let firstAt = null, lastAt = null, chunks = 0, total = 0;
let buf = "";
child.stdout.on("data", (d) => {
  const at = Date.now() - t0;
  if (firstAt === null) firstAt = at;
  lastAt = at; chunks++; total += d.length;
  buf += d.toString("utf8");
  let idx;
  while ((idx = buf.indexOf("\n")) >= 0) {
    const line = buf.slice(0, idx); buf = buf.slice(idx + 1);
    if (line.trim()) log(`T+${String(at).padStart(6)}ms ch${String(chunks).padStart(3)} len=${String(d.length).padStart(4)} | ${line.slice(0,170)}`);
  }
});
child.on("exit", (code) => {
  log(`\n[summary] exit=${code} chunks=${chunks} totalBytes=${total} firstAt=T+${firstAt}ms lastAt=T+${lastAt}ms span=${lastAt-firstAt}ms`);
});
