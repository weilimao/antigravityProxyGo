"""Measure Claude Code CLI stdout arrival cadence.

Runs `claude -p ... --output-format stream-json` and timestamps every line
as it arrives at this Python reader. Then prints elapsed-ms per line so we
can see whether output dribbles (streaming) or arrives in one burst.
"""
import os, sys, time, subprocess, json, shutil

# Windows CreateProcess can't resolve a bare "claude"; use claude.cmd from PATH or the exe directly.
exe = shutil.which("claude.cmd") or shutil.which("claude")
if not exe:
    # fall back to the well-known nvm4w location
    exe = r"B:\nvm4w\nodejs\claude.cmd"
print(f"[probe] using {exe}", flush=True)

T0 = time.time()
proc = subprocess.Popen(
    [exe, "-p",
     "从1数到20,每个数字单独一行,只输出数字",
     "--output-format", "stream-json"],
    stdout=subprocess.PIPE, stderr=subprocess.PIPE,
    bufsize=1, text=True, encoding="utf-8",
    shell=True,
)
lines = []
try:
    while True:
        line = proc.stdout.readline()
        if not line:
            break
        el = int((time.time() - T0) * 1000)
        lines.append((el, line.rstrip("\n")))
        print(f"T+{el:>6}ms | {line.rstrip()[:180]}", flush=True)
finally:
    proc.wait(timeout=20)

# cadence summary
times = [t for t,_ in lines]
if len(times) >= 2:
    deltas = [times[i+1]-times[i] for i in range(len(times)-1)]
    print(f"\n[summary] lines={len(times)} first=T+{times[0]}ms last=T+{times[-1]}ms "
          f"span={times[-1]-times[0]}ms  max_gap={max(deltas)}ms  mean_gap={sum(deltas)//len(deltas)}ms")
