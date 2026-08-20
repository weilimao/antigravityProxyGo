// cli_tty_cadence.mjs — run `claude -p` (print mode) and record arrival time
// of every stdout data event. Frequent small chunks = streaming; one big
// chunk at the end = non-streaming (text appears all at once).
//
// Note: in pipe (non-TTY) stdout, Claude Code's `-p` print mode renders the
// final answer as a single stdout write after the API stream completes —
// this is normal for -p, NOT a streaming bug. To observe true token cadence
// we must capture in TTY mode via a PTY (see cli_pty_cadence.mjs). This
// pipe version confirms whether the answer arrives and at what latency.


import { spawn } from "node:child_process";

const t0 = Date.now();
const child = spawn("claude.cmd", ["-p",
  "从1数到20,每个数字单独一行,只输出数字不要解释",
], { stdio: ["pipe", "pipe", "inherit"], shell: true });

// send EOF / Enter to stdin since -p doesn't need it
child.stdin.end();

const arrivals = [];
let total = 0, chunks = 0;
child.stdout.on("data", (d) => {
  const at = Date.now() - t0;
  chunks++; total += d.length;
  arrivals.push({ at, len: d.length, text: d.toString("utf8") });
  // emit char-by-char preview with bytes
  const preview = d.toString("utf8").replace(/\n/g, "\\n").slice(0, 100);
  console.log(`T+${String(at).padStart(6)}ms ch${String(chunks).padStart(3)} len=${String(d.length).padStart(4)} | ${preview}`);
});

child.on("exit", (code) => {
  if (arrivals.length >= 2) {
    const span = arrivals[arrivals.length-1].at - arrivals[0].at;
    console.log(`\n[summary] exit=${code} chunks=${chunks} bytes=${total} span=${span}ms`);
    console.log(`         first chunk T+${arrivals[0].at}ms  last chunk T+${arrivals[arrivals.length-1].at}ms`);
  } else {
    console.log(`\n[summary] exit=${code} chunks=${chunks} bytes=${total} (single chunk only)`);
  }
});
