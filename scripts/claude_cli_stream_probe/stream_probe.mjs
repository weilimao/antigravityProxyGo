// stream_probe.mjs — 模拟 Claude Code CLI 发起的 Anthropic /v1/messages 流式请求,
// 逐 chunk 打印到达时间戳,判定"整条一次性出现"还是"逐字流出"。
// 用法: node stream_probe.mjs [model]
const model = process.argv[2] || "nvidia/z-ai/glm-5.2";
const BASE = process.env.ANTHROPIC_BASE_URL || "http://localhost:18444/route";
const url = BASE.replace(/\/+$/, "") + "/v1/messages";
const apiKey = process.env.ANTHROPIC_API_KEY || "sk-ant-probe";

const body = JSON.stringify({
  model,
  max_tokens: 256,
  stream: true,
  messages: [
    { role: "user", content: "从1数到20,每个数字单独一行,只输出数字不要额外解释。" },
  ],
});

const t0 = Date.now();
console.log(`[probe] POST ${url}  model=${model}  stream=true  @T+0ms`);

const resp = await fetch(url, {
  method: "POST",
  headers: {
    "content-type": "application/json",
    "x-api-key": apiKey,
    "authorization": `Bearer ${apiKey}`,
    "anthropic-version": "2023-06-01",
    "accept": "text/event-stream",
    "user-agent": "claude-cli/stream-probe",
  },
  body,
});

console.log(`[probe] HTTP ${resp.status}  ct=${resp.headers.get("content-type")}  x-accel=${resp.headers.get("x-accel-buffering")}  @T+${Date.now()-t0}ms`);

if (!resp.ok || !resp.body) {
  const txt = await resp.text().catch(() => "<no body>");
  console.log(`[probe] non-stream/error body:\n${txt.slice(0,1200)}`);
  process.exit(0);
}

const dec = new TextDecoder();
let firstChunkAt = null, chunks = 0, totalBytes = 0, firstTextDeltaAt = null;
let buf = "";
const reader = resp.body.getReader();
while (true) {
  const { value, done } = await reader.read();
  if (done) break;
  if (!firstChunkAt) firstChunkAt = Date.now() - t0;
  chunks++; totalBytes += value.length;
  const text = dec.decode(value, { stream: true });
  buf += text;
  // first text_delta time
  if (firstTextDeltaAt === null && buf.includes('"text_delta"')) {
    firstTextDeltaAt = Date.now() - t0;
  }
  // print chunk arrival with relative ms + preview (truncate)
  const preview = text.replace(/\n/g, " ").slice(0, 120);
  console.log(`[chunk ${String(chunks).padStart(3)}] T+${String(Date.now()-t0).padStart(6)}ms  len=${String(value.length).padStart(5)}  ${preview}`);
  // flush complete SSE events out of buf to keep it bounded
  buf = buf.split("\n\n").pop();
}
console.log(`\n[probe] DONE  totalChunks=${chunks}  totalBytes=${totalBytes}  firstChunkAt=T+${firstChunkAt}ms  firstTextDeltaAt=${firstTextDeltaAt===null?'never':`T+${firstTextDeltaAt}ms`}  streamEnd=T+${Date.now()-t0}ms`);
