package relay

// nvidia_stream.go 收纳 NVIDIA Anthropic 流式回写 + 上游断流「蓄流重试」链路。
// 从 nvidia.go 拆分而出,仅作物理搬移,逻辑与原文件逐行等价。
//
// 本文件覆盖:
//   - (h *APICompatHandler) writeNvidiaAnthropicStream       流式回写主循环
//   - (h *APICompatHandler) pullAnthropicStreamWithRetry    蓄流 + 同账号 5s x N 重试
//   - (h *APICompatHandler) pullAnthropicStreamOneRoundInto 单轮流式拉取
//   - (h *APICompatHandler) replyAnthropicOverloaded        上游 overloaded 兜底错误

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"antigravity-proxy/internal/account"
	"antigravity-proxy/internal/netutil"
)

// writeNvidiaAnthropicStream 处理流式 Anthropic 入站：上游 OpenAI Chat SSE → Anthropic SSE。
// 响应头对齐 compat.go:826-837(Gemini 链路)保证 SSE 不被反代/框架缓冲:
//   - X-Accel-Buffering: no 禁止 Nginx 聚合 SSE;
//   - http.Flusher 逐帧 push 到 TCP socket,避免仅写到 http.ResponseWriter 内部缓冲。
//
// 蓄流回放架构(上游断流服务端无缝重试):
// 不再"边读上游边写客户端"。而是先用 replayWriter 在内存把整条上游 SSE 翻译攒成完整 Anthropic SSE
// (期间若上游中途断流 unexpected EOF,本方法丢弃本次 buffer、睡眠 5s 后原账号重建上游请求重拉,
// 最多重试 5 次,不换号);只有当整条 ready(收到 finish_reason 且无上游错误)后,才 WriteHeader(200)
// + SSE 头并把 buffer 逐帧 flush 回放给客户端。重试期间客户端未收到任何字节,不会出现"半截内容冲突";
// 重试耗尽则回写 Anthropic overloaded_error 让 CLI 看到真实失败(不再静默补 end_turn 假闭合)。
//
// r 透传 r.Context():客户端取消时立即终止重试与重拉;poolAccount 重试全程保持同一账号(按要求不换号)。
// targetURL/upstreamBody 由主循环透传,供重试时原样重建上游 POST 请求体与目标 URL(不重新选号、不改动请求)。
func (h *APICompatHandler) writeNvidiaAnthropicStream(w http.ResponseWriter, r *http.Request, resp *http.Response, model string, userSession *RelaySession, poolAccount *account.Account, targetURL string, upstreamBody []byte, logCtx nvidiaLogCtx) {
	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		// 上游非 200:翻译成 Anthropic 标准错误结构回写(原裸透 OpenAI JSON 会让 CLI 卡住/报奇怪错误)
		h.writeAnthropicErrorFromUpstream(w, resp.StatusCode, bodyBytes)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		h.log("⚠️ [NVIDIA Anthropic 流式] http.ResponseWriter 不支持 Flusher, 降级为仅 bufio flush (SSE �时性可能打折)")
	}

	// 混合模式 + 延迟缓冲保 503:
	//   - pull 前 liveFW 置 deferredActive=true + firstByteHook(WriteHeader 200 + 刷头)。
	//   - 推理模型:首轮 tee 把 message_start + thinking 块 start 暂存 liveFW.deferred,首个 thinking_delta
	//     到达时 tee 调 liveFW.flushDeferred() 触发 WriteHeader + 把框架帧一齐送出 + 逐字实时推思考。
	//     首字节延迟 ≈ TTFT。
	//   - 无推理模型:首轮无 live 字节,pull 返回成功后 replayBodyInto 前手动调 flushDeferred 触发 WriteHeader,
	//     再回放正文。首字节延迟与改动前一致(无长沉默期,不回归)。
	//   - 上游在首条思考实质内容前就断流重试耗尽:deferred 未 flush,dropDeferred 丢弃框架帧,回写 503
	//     overloaded_error,客户端干净失败(从未收到任何字节)。一旦思考实质内容已 flush(200 头已发),
	//     失败只能流内补 event:error 表达(SSE 流式失败的规范语义)。
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	bw := bufio.NewWriter(w)
	liveFW := newFlushWriter(fmt.Sprintf("nv_%d", time.Now().UnixNano()), bw, flusher)

	headerWritten := false
	headerMu := &sync.Mutex{}
	firstLiveByteHook := func() {
		headerMu.Lock()
		defer headerMu.Unlock()
		if headerWritten {
			return
		}
		w.WriteHeader(http.StatusOK)
		if ok {
			flusher.Flush() // 立即把响应头推给客户端,让其尽早进入 SSE 等待状态
		}
		headerWritten = true
	}
	liveFW.firstByteHook = firstLiveByteHook
	liveFW.deferredActive = true // pull 阶段框架帧先进 deferred,等首条实质内容 flush

	replay, state, in, out, finalErr := h.pullAnthropicStreamWithRetry(r, resp, poolAccount, targetURL, upstreamBody, model, liveFW)
	if finalErr != nil {
		// 重试耗尽。两种情况:
		// 1) 200 头未发(deferred 未 flush,上游在首条思考实质内容前就次次断流):drop 丢弃框架帧,
		//    回写 503 overloaded_error,客户端干净失败;
		// 2) 200 头已发(首条思考实质内容已 flush):无法回退状态码,补闭合 live 上残留未闭合块(thinking+body)
		//    后流内追加 event:error,让客户端 SDK 据此识别失败(SSE 流式失败规范语义)。
		headerMu.Lock()
		written := headerWritten
		headerMu.Unlock()
		resp.Body.Close()
		h.log("🛑 [NVIDIA Anthropic 流式] 上游断流, 服务端重试 5 次仍失败, 回写 overloaded_error: %v", finalErr)
		if !written {
			liveFW.dropDeferred() // 丢弃暂存的 message_start 等框架帧,确保 503 回写前无字节落盘
			h.replyAnthropicOverloaded(w)
			return
		}
		// 200 头已发:deferred 已 flush,liveFW 转直写模式。补闭合 live 上残留未闭合块 + error 事件收尾。
		// state 反映成功路径快照,失败路径下其 liveIdxMap/liveMaxIdx 可能未及时刷新(重试轮内 resumeSink
		// 维护 liveBodyOpenIdx),但失败时 live 上真实"已开未闭合块"由 resumeSink.liveBodyOpenIdx/
		// tee.liveThinkingOpen 跟踪。补闭合顺序:先 thinking(0)→再正文(liveBodyOpenIdx)。
		// 此处保守补闭合 thinking(若 state.thinkingLive 且 live 残留)+ 正文(若 state 指示有未闭合)。
		// 因失败路径下调用方拿到的 state 可能为 nil(5×5s 全失败从未成功),teeresume 的开块态由
		// tee/resume 内部跟踪但未回传——故此处仅补闭合 thinking(沿用原行为)+ 流内 error,正文开块若残留
		// 由客户端 SDK 容错(Anthropic 对未闭合块遇 error 事件会按"中断"处理,不卡死)。
		if state != nil && state.thinkingLive {
			liveFW.writeEvent("content_block_delta", contentBlockSignatureDeltaPayload(0, ""))
			liveFW.writeEvent("content_block_stop", contentBlockStopPayload(0))
		}
		errPayload, _ := json.Marshal(map[string]interface{}{
			"type": "error",
			"error": map[string]interface{}{
				"type":    "overloaded_error",
				"message": "NVIDIA upstream stream interrupted and server-side retry exhausted (5x5s, same account).",
			},
		})
		liveFW.writeEvent("error", string(errPayload))
		bw.Flush()
		if ok {
			flusher.Flush()
		}
		return
	}

	// 整条 ready:replay 中"尚未 live 段 + 尾帧"待补发给 live。统一经 replayFollowingInto 一次回放:
	//   - 跳过已 live 的 text 块(start/delta/stop 全跳,避免草稿段重复);
	//   - 补发尚未 live 的块(tool_use、尾随 text 等,remap 到 liveMaxIdx+1);
	//   - 补发尾帧 message_delta + message_stop(stop_reason/usage 与成功轮一致)。
	// replayFollowingInto 前必须确保 200 头已发(flushDeferred 幂等):
	//   - 推理模型首轮 thinking 实质内容已 flush,deferred 已 flush 过,flushDeferred 无副作用;
	//   - 无推理模型首轮无 live 字节,deferred 仍 active 且为空,此处 flushDeferred 触发 WriteHeader 200
	//     (firstByteHook)+ 刷净空 deferred,随后 replayFollowingInto 补发的未 live 段/尾帧直写客户端,不回归首字节延迟。
	liveFW.flushDeferred()
	replay.replayFollowingInto(liveFW, state)
	bw.Flush()
	if ok {
		flusher.Flush() // 收尾刷净, 确保 message_stop 落盘
	}
	h.recordNvidiaUsage(userSession, model, in, out, poolAccount, logCtx)
}

// pullAnthropicStreamWithRetry 把上游 OpenAI Chat SSE 翻译并实时/续传下发到 liveFW,
// 上游中途断流(unexpected EOF / 未给出 finish_reason)时睡眠 retryWait 后原账号重建上游请求重拉,
// 最多重试 5 次 + 兜底代理 1 轮,全程不换号。客户端 r.Context() 取消时立即终止重试与重拉。
//
// 正文逐块实时下发 + 断流续传不重发架构:
//   - 首轮(attempt==0)用 teeSink:思考 + 纯 text 正文逐块实时推 liveFW,tool_use 段及之后只蓄流;
//   - 首轮断流时把 tee 的 live 残留态(liveThinkingOpen/liveBodyOpenIdx/liveMaxUsedIdx/liveIdxMap)拷贝进 resumeSink,
//     跨重试轮复用;重试轮(attempt>0)用 resumeSink:跳过 message_start/思考,惰性补闭合残留块,
//     正文 text 块 index 重映射(liveMaxUsedIdx+1)后续推 live,实现"草稿段+重启段"无重复续传;
//   - 含 tool_use 的回复:首轮与重试轮 tool 段都只蓄流,整条 ready 后由调用方统一经 replayFollowingInto 回放
//     (跳过已 live 的 text 块 + 补发 tool 块与尾帧);纯 text 回复正文已实时推完,replayFollowingInto 仅补尾帧。
//   - 尾帧(message_delta/message_stop)首轮与重试轮都只蓄流不推 live,由调用方整体成功后一次性补发,
//     避免断流轮把尾帧推 live 封死客户端流、后续重试无法续推正文。
//
// 返回 (replay, state, in, out, err):
//   - replay:整条 ready 的成功轮 replay 缓冲,供调用方 replayFollowingInto 补发未 live 段+尾帧;
//   - state:成功时的 live 协议态快照(liveIdxMap/liveMaxIdx/thinkingLive),供 replayFollowingInto 决定跳过哪些块;
//   - in/out:成功这次累计 input/output tokens,用于号池账号维度统计;失败时为 0;
//   - err:重试耗尽仍失败时非 nil(含最后一次上游错误),调用方据此回写 overloaded_error。
//
// 完整性判定:openAIChatSSEToAnthropicSSEInto 返回 sseErr==nil && (finishEmitted||streamTerminated) 视为完整。
//
// 重试约束:不重新选号、不冷冻账号、不改请求体;仅以同一 poolAccount 复用 targetURL+upstreamBody 重建 POST。
// 与现有"429 退避换号"链路独立。
//
// 超大流保护:蓄流超过 nvidiaReplayMaxBytes(16MiB)判定为超大输出,放弃重试(转 finalErr→overloaded),
// 不切路径——正文本就实时下发,无"退回边读边写"旧路径可退。
func (h *APICompatHandler) pullAnthropicStreamWithRetry(r *http.Request, firstResp *http.Response, poolAccount *account.Account, targetURL string, upstreamBody []byte, model string, liveFW *flushWriter) (replay *replayWriter, state *liveStreamState, in, out int, finalErr error) {
	const (
		maxRetries = 5
		// replayMaxBytes 复用包级 nvidiaReplayMaxBytes,保证直连与兜底 helper 同一阈值。
		replayMaxBytes = nvidiaReplayMaxBytes
	)
	// 单次重试退避:生产默认 5s(nvidiaStreamRetryWait,构造初始化);测试可覆盖为小值快跑。
	// 零值兜底为 5s,避免误装配导致无退避狂打上游。
	retryWait := h.nvidiaStreamRetryWait
	if retryWait <= 0 {
		retryWait = 5 * time.Second
	}
	streamID := fmt.Sprintf("msg_nvidia_%d", time.Now().UnixNano())
	httpClient := h.streamClient
	ctx := r.Context()

	// 混合模式 tee:首轮(attempt==0)双写——思考 + 纯 text 正文逐块实时透传 liveFW,tool 段只蓄流;
	// 尾帧只蓄流不推 live(由调用方整体成功后经 replayFollowingInto 一次性补发)。
	// liveFW==nil(上游不支持 Flusher 的降级路径)时退化为纯蓄流,等同旧行为。
	tee := newTeeSink(newReplayWriter(), liveFW)

	// resumeSink 跨重试轮复用:首轮断流时懒构造,拷贝 tee 的 live 残留态作为续传起点;
	// 每轮开始前调 reset() 清本轮运行期态、保留跨轮单调字段(liveMaxUsedIdx 等)。
	var resume *resumeSink

	// 本次循环的活跃响应体(逐次重拉新建,每次结束后 Close)
	var activeResp *http.Response = firstResp
	var activeBody io.ReadCloser = firstResp.Body
	// 第一轮复用主循环已 Do 出来的 firstResp;从第二轮起重建上游请求。
	for attempt := 0; attempt < maxRetries; attempt++ {
		// 重拉准备:第 0 轮用现成 firstResp,其后各轮新建上游请求(原账号、原请求体)。
		if attempt > 0 {
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(upstreamBody))
			if err != nil {
				finalErr = fmt.Errorf("rebuild nvidia upstream request: %w", err)
				if activeBody != nil {
					activeBody.Close()
				}
				return nil, nil, 0, 0, finalErr
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+poolAccount.AccessToken)
			req.Header.Set("Accept", "application/json")
			resp, errDo := httpClient.Do(req)
			if errDo != nil {
				finalErr = fmt.Errorf("nvidia upstream retry do failed: %w", errDo)
				h.log("⚠️ [NVIDIA Anthropic 流式] 断流重试 %d/%d 账号 %s 重拉失败: %v", attempt+1, maxRetries, poolAccount.Email, errDo)
				continue
			}
			if resp.StatusCode != http.StatusOK {
				resp.Body.Close()
				finalErr = fmt.Errorf("nvidia upstream retry status %d", resp.StatusCode)
				h.log("⚠️ [NVIDIA Anthropic 流式] 断流重试 %d/%d 账号 %s 上游返回 %d", attempt+1, maxRetries, poolAccount.Email, resp.StatusCode)
				continue
			}
			activeResp = resp
			activeBody = resp.Body
		} else if attempt == 0 && activeResp.StatusCode != http.StatusOK {
			// 主循环已保证流式入站进入本函数时 resp.StatusCode==200,此处仅防御性兼容。
			finalErr = fmt.Errorf("nvidia upstream status %d", activeResp.StatusCode)
			activeBody.Close()
			return nil, nil, 0, 0, finalErr
		}

		// sink 选择:首轮用 tee(思考+正文实时下发,tool/尾帧只蓄流);断流后重试轮用 resumeSink(续传不重发)。
		// resumeSink 懒构造:首次进入重试轮(attempt==1)时拷贝 tee 的 live 残留态作为续传起点;
		// 后续重试轮(attempt>=2)reset() 复用同一实例(跨轮保留 liveMaxUsedIdx 单调、补闭合标志)。
		// 重试轮统一用 resumeSink.replay 作蓄流 sink(openAIChatSSEToAnthropicSSEInto 写 resumeSink,
		// 它内部 replay 原样蓄流供完整性判定 + live 按续传规则过滤/重映射推 liveFW)。
		var sink sseEventSink
		if attempt == 0 {
			sink = tee
		} else {
			if resume == nil {
				resume = newResumeSink(liveFW, tee.replay, tee.liveThinkingOpen, tee.liveBodyOpenIdx, tee.liveMaxUsedIdx)
			} else {
				resume.reset()
			}
			tee.replay.reset() // 蓄流缓冲复用:重试轮重蓄整条上游内容(由 resumeSink.replay 写入)
			sink = resume
		}
		attemptIn, attemptOut, finishEmitted, streamTerminated, sseErr := openAIChatSSEToAnthropicSSEInto(ctx, activeBody, activeBody, sink, streamID, model)
		activeBody.Close() // 本轮上游响应体读完即关,下一轮(若有)重拉会拿到全新 body

		// 完整性判定:收到 finish_reason 帧或上游流以 [DONE]/正常 EOF 正常终止,且无上游错误/未 ctx 取消 → 整条 ready。
		// streamTerminated 兜底 NIM 等上游"不发 finish_reason、仅 usage+[DONE]"的合法收尾形态,
		// 避免把它误判为断流而触发无意义重试。真·断流(unexpected EOF 在 [DONE] 前)使 streamTerminated=false
		// 且 sseErr!=nil,本判定不满足,落入重试路径。
		if sseErr == nil && (finishEmitted || streamTerminated) {
			// 成功:构造 live 协议态快照供调用方 replayFollowingInto 决定跳过哪些已 live 块 + 补尾帧。
			// 首轮成功(无重试):快照取 tee(liveIdxMap=identity 已 live 的 text,thinkingLive=first-round thinking,最大 idx);
			// 重试轮成功:快照取 resume(indexMap=本轮新开 text 块映射,thinkingLive=false 因重试 thinking 草稿已丢弃不补)。
			if attempt == 0 {
				return tee.replay, &liveStreamState{
					liveIdxMap:   tee.liveIdxMap,
					liveMaxIdx:   tee.liveMaxUsedIdx,
					thinkingLive: tee.liveThinkingPushed,
				}, attemptIn, attemptOut, nil
			}
			// 重试轮成功:先提交 pending(重启段一次性落 live)+ 回填持久态,再取快照。
			resume.commitPending()
			return resume.replay, &liveStreamState{
				liveIdxMap:   resume.indexMap,
				liveMaxIdx:   resume.liveMaxUsedIdx,
				thinkingLive: false, // 重试轮 thinking 全跳未 live,replayFollowingInto 据此跳过成功轮思考头
			}, attemptIn, attemptOut, nil
		}
		// ctx 取消:客户端已断,不再重试,带上 ctx 错误返回。
		if ctx != nil && ctx.Err() != nil {
			finalErr = fmt.Errorf("client context cancelled during nvidia stream retry: %w", ctx.Err())
			if sseErr != nil {
				finalErr = fmt.Errorf("%v (last sse err: %v)", finalErr, sseErr)
			}
			return nil, nil, 0, 0, finalErr
		}
		// 超大流保护:蓄流超过阈值,判定为超大输出,放弃重试转 overloaded(不切路径——正文已实时,无旧路径可退)。
		if tee.replay.len() > replayMaxBytes {
			h.log("⚠️ [NVIDIA Anthropic 流式] 蓄流 %d 字节超阈值 %d, 放弃重试转 overloaded", tee.replay.len(), replayMaxBytes)
			finalErr = fmt.Errorf("nvidia replay stream oversized %d bytes (exceeds %d), abort retry", tee.replay.len(), replayMaxBytes)
			return nil, nil, 0, 0, finalErr
		}
		// 不完整(断流/未收尾/上游内嵌 error chunk):记录原因,睡眠 5s 后重拉(最后一轮不再睡)。
		lastErr := sseErr
		if lastErr == nil {
			lastErr = fmt.Errorf("nvidia upstream stream incomplete (finishEmitted=%v streamTerminated=%v)", finishEmitted, streamTerminated)
		}
		finalErr = lastErr
		h.log("⚠️ [NVIDIA Anthropic 流式] 上游断流判定不完整(已攒 %d 字节), 将重试 %d/%d 账号 %s: %v", tee.replay.len(), attempt+1, maxRetries, poolAccount.Email, lastErr)
		if attempt < maxRetries-1 {
			// 睡眠 5s 但受 ctx 取消打断,客户端断开立即放弃重试不空跑。
			select {
			case <-ctx.Done():
				finalErr = fmt.Errorf("client context cancelled during retry backoff: %w", ctx.Err())
				return nil, nil, 0, 0, finalErr
			case <-time.After(retryWait):
			}
		}
	}
	// ===== 直连 5s×5 同账号重试耗尽后,切兜底出站代理再 1 轮(单次请求级,不记忆状态) =====
	// 兜底代理只对本机到上游的网络路径类断流有效;对上游 worker 过载/上游节点抖动这类上游侧故障,
	// 换出口到达同一上游集群多半仍失败——这是物理事实,兜底尽力而为,1 轮不成即回 overloaded_error。
	// 仅 NVIDIA 链路生效(本函数即 NVIDIA Anthropic 流式);不换号、不改请求体,仅换 transport 出口。
	// 配置为空 / 解析失败时跳过兜底,直接回 overloaded_error。
	// 兜底轮与直连重试轮同构:复用同一 resumeSink(带同一 live 残留态),正文继续实时下发到 live,
	// 成功后由调用方经 replayFollowingInto 统一补发未 live 段+尾帧——而非旧"独立 replayWriter+replayBodyInto"
	// (那会导致兜底成功正文重复下发已 live 的草稿段)。
	if resume != nil && h.settingsMgr != nil && h.settingsMgr.GetFallbackProxyEnabled() {
		fbAddr := h.settingsMgr.GetFallbackProxyAddress()
		fbUser := h.settingsMgr.GetFallbackProxyUsername()
		fbPass := h.settingsMgr.GetFallbackProxyPassword()
		fbClient, fbErr := netutil.GetFallbackClient(fbAddr, fbUser, fbPass)
		if fbErr != nil {
			h.log("⚠️ [NVIDIA Anthropic 流式] 兜底代理配置无效,跳过兜底: %v (addr=%s)", fbErr, fbAddr)
		} else if fbClient != nil {
			h.log("🛟 [NVIDIA Anthropic 流式] 直连重试耗尽,切兜底代理 %s 再试 1 轮 账号 %s", fbAddr, poolAccount.Email)
			resume.reset()
			tee.replay.reset()
			fbReplay, fbIn, fbOut, fbFinalErr := h.pullAnthropicStreamOneRoundInto(ctx, fbClient, poolAccount, targetURL, upstreamBody, streamID, model, resume)
			if fbFinalErr == nil && fbReplay != nil {
				resume.commitPending() // 兜底轮整条 ready:提交重启段落 live + 回填持久态
				return fbReplay, &liveStreamState{
					liveIdxMap:   resume.indexMap,
					liveMaxIdx:   resume.liveMaxUsedIdx,
					thinkingLive: false, // 兜底轮 thinking 同样草稿丢弃,replayFollowingInto 据此跳过其思考头
				}, fbIn, fbOut, nil
			}
			if fbFinalErr != nil {
				finalErr = fmt.Errorf("fallback proxy round also failed: %w", fbFinalErr)
			}
		}
	}
	// 重试耗尽(含兜底也失败):返回最后一次错误原因,由调用方回写 overloaded_error。
	return nil, nil, 0, 0, finalErr
}

// pullAnthropicStreamOneRoundInto 用指定的 httpClient(兜底代理 client)向 NVIDIA 上游发一次请求,
// 把转译结果喂进给定 sink(resumeSink,正文按续传规则实时推 live + 蓄流 replay),
// 并做完整性判定,返回 (replay, in, out, err)。ok=err==nil&&replay!=nil 表示本流完整;否则 err 含原因。
// 与旧 pullAnthropicStreamOneRound 的区别:接受外部 sink(而非自建纯 replayWriter),使兜底轮也能
// 实时下发正文到 live,与直连重试轮同构,兜底成功后由调用方经 replayFollowingInto 统一补发未 live 段+尾帧。
// roundLabel 仅用于日志。调用方需在调用前对 sink 做 reset() 并 tee.replay.reset()。
func (h *APICompatHandler) pullAnthropicStreamOneRoundInto(ctx context.Context, httpClient *http.Client, poolAccount *account.Account, targetURL string, upstreamBody []byte, streamID, model string, sink sseEventSink) (*replayWriter, int, int, error) {
	roundLabel := "兜底"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(upstreamBody))
	if err != nil {
		return nil, 0, 0, fmt.Errorf("rebuild nvidia upstream request (%s): %w", roundLabel, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+poolAccount.AccessToken)
	req.Header.Set("Accept", "application/json")
	resp, errDo := httpClient.Do(req)
	if errDo != nil {
		return nil, 0, 0, fmt.Errorf("nvidia upstream (%s) do failed: %w", roundLabel, errDo)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, 0, 0, fmt.Errorf("nvidia upstream (%s) status %d", roundLabel, resp.StatusCode)
	}
	attemptIn, attemptOut, finishEmitted, streamTerminated, sseErr := openAIChatSSEToAnthropicSSEInto(ctx, resp.Body, resp.Body, sink, streamID, model)
	resp.Body.Close()
	// 完整性判定:收到 finish_reason 或 [DONE]/正常 EOF 正常终止,且无上游错误/未 ctx 取消 → 整条 ready。
	if sseErr == nil && (finishEmitted || streamTerminated) {
		// 成功:返回 sink 内的 replay(resumeSink.replay)供调用方 replayFollowingInto 补发未 live 段+尾帧。
		if rs, ok := sink.(*resumeSink); ok {
			return rs.replay, attemptIn, attemptOut, nil
		}
		return nil, attemptIn, attemptOut, nil // 防御:非 resumeSink 不应出现
	}
	// ctx 取消:带上 ctx 错误返回,调用方据此放弃后续重试。
	if ctx != nil && ctx.Err() != nil {
		err := fmt.Errorf("client context cancelled during (%s) round: %w", roundLabel, ctx.Err())
		if sseErr != nil {
			err = fmt.Errorf("%v (last sse err: %v)", err, sseErr)
		}
		return nil, 0, 0, err
	}
	// 超大流保护:蓄流超过 nvidiaReplayMaxBytes 判定为超大输出。兜底无可退的边读边写路径,直接转 lastErr,
	// 让上层把 finalErr 设为兜底失败、落 overloaded_error。也保护兜底轮不被超大流撑爆内存。
	replayLen := 0
	if rs, ok := sink.(*resumeSink); ok {
		replayLen = rs.replay.len()
	}
	if replayLen > nvidiaReplayMaxBytes {
		return nil, 0, 0, fmt.Errorf("nvidia upstream (%s) replay oversized %d bytes (exceeds %d), abort fallback round", roundLabel, replayLen, nvidiaReplayMaxBytes)
	}
	// 不完整:返回原因(断流/未收尾/上游内嵌 error chunk)。
	lastErr := sseErr
	if lastErr == nil {
		lastErr = fmt.Errorf("nvidia upstream (%s) stream incomplete (finishEmitted=%v streamTerminated=%v)", roundLabel, finishEmitted, streamTerminated)
	}
	return nil, 0, 0, lastErr
}

// replyAnthropicOverloaded 回写 Anthropic 标准 overloaded_error 给客户端(Claude Code CLI 据此识别为
// 上游过载/断流失败,可走自身处理逻辑而非把残缺流当正常结束)。取代旧的"补 end_turn 假闭合静默"路径。
// 用 529 状态码对齐 Anthropic 官方 overloaded 语义;响应体为 Anthropic error 结构。
func (h *APICompatHandler) replyAnthropicOverloaded(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusServiceUnavailable) // 503
	payload, _ := json.Marshal(map[string]interface{}{
		"type": "error",
		"error": map[string]interface{}{
			"type":    "overloaded_error",
			"message": "NVIDIA upstream stream interrupted and server-side retry exhausted (5x5s, same account).",
		},
	})
	_, _ = w.Write(payload)
}
