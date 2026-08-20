// capture_server.mjs — 一口式本地捕获服务:监听本端口,把 claude CLI 发来的
// /v1/messages 请求头 + body 原样落盘,并回一条最简合法非流式应答让 CLI 收尾。
// 用法: node capture_server.mjs [port]
import http from "node:http";
import fs from "node:fs";

const port = parseInt(process.argv[2] || "19111", 10);
const outDir = "./_cap";
fs.mkdirSync(outDir, { recursive: true });

const srv = http.createServer((req, res) => {
  const chunks = [];
  req.on("data", (c) => chunks.push(c));
  req.on("end", () => {
    const body = Buffer.concat(chunks).toString("utf8");
    const ts = new Date().toISOString().replace(/[:.]/g, "-");
    let parsed = null;
    try { parsed = JSON.parse(body); } catch {}
    const rec = {
      time: ts,
      method: req.method,
      url: req.url,
      headers: req.headers,
      streamField: parsed?.stream,
      model: parsed?.model,
      hasMaxTokens: parsed?.max_tokens != null,
      hasThinking: parsed?.thinking != null,
      bodyKeys: parsed ? Object.keys(parsed) : null,
      bodyPreview: body.slice(0, 1500),
    };
    const fn = `${outDir}/req_${ts}.json`;
    fs.writeFileSync(fn, JSON.stringify(rec, null, 2));
    console.log(`[${ts}] ${req.method} ${req.url}  stream=${parsed?.stream}  model=${parsed?.model}  -> ${fn}`);
    process.stdout.write(""); // flush

    // Return a minimal valid Anthropic /v1/messages non-streaming JSON response so claude exits clean.
    if (req.url.includes("/v1/messages") || req.url.includes("messages")) {
      const respJson = {
        id: "msg_cap_" + Date.now(),
        type: "message",
        role: "assistant",
        model: parsed?.model || "captured",
        content: [{ type: "text", text: "captured" }],
        stop_reason: "end_turn",
        stop_sequence: null,
        usage: { input_tokens: 1, output_tokens: 1 },
      };
      const payload = JSON.stringify(respJson);
      res.writeHead(200, { "content-type": "application/json" });
      res.end(payload);
    } else if (req.url.includes("/v1/models") || req.method === "GET") {
      res.writeHead(200, { "content-type": "application/json" });
      res.end(JSON.stringify({ object: "list", data: [{ id: "glm-5.2", object: "model", owned_by: "cap" }] }));
    } else {
      res.writeHead(404); res.end("not found");
    }
  });
});
srv.listen(port, "127.0.0.1", () => console.log(`[capture] listening http://127.0.0.1:${port}  -> ${outDir}/`));
// Keep alive, flush stdout
process.stdout.write("");
setInterval(() => {}, 1000);
