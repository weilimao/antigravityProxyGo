package relay

import (
	"encoding/json"
	"io"
)

// sse_payload.go: Anthropic/Responses SSE 事件 payload 构造与帧写入的共享收口 helper。
//
// 复用点2(jsonString):收口各 SSE payload 构造器尾部
//   `b, _ := json.Marshal(m); return string(b)`
//   / `payload, _ := json.Marshal(...); return string(payload)`
//   / `string(marshalJSON(x))`
// 三种发货写法为一处定义。覆盖范围:
//   - nvidia_translate_payload.go 的 9 个 Anthropic SSE payload 构造器;
//   - nvidia_responses.go 的 16 个 Responses API payload 构造器 + responsesCloseReasoning 三件套。
//
// 复用点4(writeSSEFrame):收口 nvidia_translate_buffer.go 四处重复的
//   `w.WriteString("event: " + event + "\n"); w.WriteString("data: " + data + "\n\n")`
// 两行 Anthropic 帧写入样板,保证帧格式一处定义、四处复用。

// jsonString 把任意可序列化值 JSON-marshall 成字符串。
// 收口各 SSE payload 构造器尾部的 marshal+string 两步样板:
//   - 成功:返回紧凑 JSON 文本(与 `string(json.Marshal(...))` 逐字节等价);
//   - 失败:json.Marshal 返回 err 且 b==nil → string(b)=="" —— 与原
//     `payload, _ := json.Marshal(...); return string(payload)`(失败 payload==nil, string(nil)=="")
//     语义逐行等价,零回归。
//
// 不返回 error:所有调用点的构造 map 均为合法可序列化结构,marshal 不可能失败;
// 保留错误吞没语义仅为与原代码逐行等价,避免引入上游需处理的错误返回值改签名。
func jsonString(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// writeSSEFrame 把一帧 Anthropic SSE 写进给定 sink。
// 帧格式严格为:
//
//	event: <event>\n
//	data: <data>\n
//	\n
//
// 收口 nvidia_translate_buffer.go 四处重复的 WriteString 两行样板,保证帧格式一处定义、四处复用。
//
// 接收 io.StringWriter 而非 io.Writer,以保留 *bytes.Buffer / *bufio.Writer 的零拷贝 WriteString
// fast path(两者均实现 WriteString);字节序列与原两行 WriteString 直调逐字节等价:
//
//	"event: " + event + "\n" + "data: " + data + "\n\n"
//
// 调用方需自行持锁(flushWriter.mu / replayWriter.mu):本函数无状态、纯顺序写入,不引入锁也
// 不改变锁边界 —— 在调用方 mu.Lock() 区间内调用,写入顺序与原内联两行 WriteString 完全一致。
func writeSSEFrame(w io.StringWriter, event, data string) {
	w.WriteString("event: " + event + "\n")
	w.WriteString("data: " + data + "\n\n")
}
