package relay

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestResponsesToolAdjacency_ParallelEightRepro 复刻线上 messages.4 400:
//   messages.4: `tool_use` ids were found without `tool_result` blocks immediately after:
//   call_1785650561662060100_27, call_..._635, ... 8 个 id。
//
// 线上日志角色序列(5 条):
//   [user[TTTT] model[TFC] user[FR TT] model[T FC×8] user[FR×8]]
//
// 关键: 第 3 条 user[FR TT] —— 上一轮的 1 个 tool_result 后跟 2 个 text。
// 第 4 条 model[T FC×8] —— 本轮助手回合含 text + 8 个并行 functionCall。
// 第 5 条 user[FR×8] —— 本轮 8 个并行 functionResponse。
//
// Anthropic(Vertex 重译)要求 messages.4(user[FR×8])里的 8 个 tool_result 各自 id
// 能匹配 messages.3(model[T FC×8])里的 8 个 tool_use id。本测试锁:
//   (a) 每条 FC.ID 与对应 FR.ID 是否相等(id 透传链路完整性);
//   (b) 8 个 FC id 互不相同、8 个 FR id 互不相同(防 id 退化成空串或重复);
//   (c) 跨消息邻接不变式仍成立;
//   (d) model[T FC×8] 经规整后 tool_use 仍处于末尾(典范顺序)。
func TestResponsesToolAdjacency_ParallelEightRepro(t *testing.T) {
	SetGlobalEnableThinkingMode(true)
	defer SetGlobalEnableThinkingMode(true)

	// 8 个并行 call_id(对齐线上日志里 8 个 id 的形态:call_<纳秒>_<rand>)。
	callIDs := []string{
		"call_1785650561662060100_27",
		"call_1785650562550788700_635",
		"call_1785650563442746000_884",
		"call_1785650564571352800_760",
		"call_1785650565212142800_578",
		"call_1785650566120749500_527",
		"call_1785650566991023000_107",
		"call_1785650568031196100_581",
	}

	// 构造 Codex /v1/responses input:复刻「上轮 1 工具 + 本轮 8 并行工具」的历史。
	var inputItems []string
	inputItems = append(inputItems, `{"type":"message","role":"user","content":"TTTT"}`)
	// 上轮:1 个 function_call + 1 个 output + 后续 2 条 user 文本(产出 user[FR TT])。
	inputItems = append(inputItems, `{"type":"function_call","call_id":"call_prev_1","name":"shell_command","arguments":"{\"command\":\"ls\"}"}`)
	inputItems = append(inputItems, `{"type":"function_call_output","call_id":"call_prev_1","output":"file1"}`)
	inputItems = append(inputItems, `{"type":"message","role":"user","content":"分析结果"}`)
	inputItems = append(inputItems, `{"type":"message","role":"user","content":"继续"}`)
	// 本轮:8 个并行 function_call(全部 flush 成一条 assistant,8 个 ToolCalls),
	// 然后 8 个 function_call_output(8 个 tool 消息)。
	for _, id := range callIDs {
		inputItems = append(inputItems, `{"type":"function_call","call_id":"`+id+`","name":"shell_command","arguments":"{\"command\":\"echo `+id+`\"}"}`)
	}
	for _, id := range callIDs {
		inputItems = append(inputItems, `{"type":"function_call_output","call_id":"`+id+`","output":"out-`+id+`"}`)
	}

	cmdJSON := `{
		"model": "claude-opus-4-6-thinking",
		"stream": true,
		"input": [` + strings.Join(inputItems, ",") + `]
	}`

	openReq, err := ParseUnifiedOpenAIRequest([]byte(cmdJSON))
	if err != nil {
		t.Fatalf("ParseUnifiedOpenAIRequest: %v", err)
	}

	// 1. parseResponsesInput 产出核查:本轮 assistant 应含 8 ToolCalls,8 个 tool 消息 ToolCallID 与 call_id 一致。
	t.Logf("parseResponsesInput 产出 %d 条 OpenAIMessage:", len(openReq.Messages))
	for i, m := range openReq.Messages {
		tcCount := len(m.ToolCalls)
		t.Logf("  [%d] role=%s content=%q toolCalls=%d toolCallID=%q", i, m.Role, truncLog(m.Content, 30), tcCount, m.ToolCallID)
	}

	gem := TranslateOpenAIToGemini(openReq)

	// 2. 最终 Gemini Contents 结构与 id 透传核查。
	t.Logf("TranslateOpenAIToGemini 产出 %d 条 GeminiContent:", len(gem.Contents))
	// 仅统计"最后一条 model"与"最后一条 user"的 id,避免跨多条消息累积上轮的 FC/FR。
	var lastModelFCIDs, lastUserFRIDs []string
	var lastModelIdx, lastUserIdx = -1, -1
	for i, c := range gem.Contents {
		var desc []string
		for _, p := range c.Parts {
			switch {
			case p.FunctionCall != nil:
				desc = append(desc, "FC(id="+p.FunctionCall.ID+")")
			case p.FunctionResponse != nil:
				desc = append(desc, "FR(id="+p.FunctionResponse.ID+")")
			case p.Text != "":
				desc = append(desc, "T("+truncLog(p.Text, 24)+")")
			}
		}
		t.Logf("  [%d] role=%s parts=[%s]", i, c.Role, joinLog(desc, " "))
		if c.Role == "model" {
			lastModelIdx = i
		} else if c.Role == "user" {
			lastUserIdx = i
		}
	}
	// 仅从最后一条 model / user 收集 id,避免上轮 FC/FR 混入计数。
	if lastModelIdx >= 0 {
		for _, p := range gem.Contents[lastModelIdx].Parts {
			if p.FunctionCall != nil {
				lastModelFCIDs = collectID(lastModelFCIDs, p.FunctionCall.ID)
			}
		}
	}
	if lastUserIdx >= 0 {
		for _, p := range gem.Contents[lastUserIdx].Parts {
			if p.FunctionResponse != nil {
				lastUserFRIDs = collectID(lastUserFRIDs, p.FunctionResponse.ID)
			}
		}
	}

	// (a)+(b) 最后一条 model 的 8 个 FC id 与最后一条 user 的 8 个 FR id 必须一一对应且互不重复。
	if len(lastModelFCIDs) != 8 {
		t.Errorf("最后一条 model 的 FC 数=%d, want 8", len(lastModelFCIDs))
	} else {
		if dup := firstDup(lastModelFCIDs); dup != "" {
			t.Errorf("最后一条 model 的 FC id 重复: %q(8 个理应互异)", dup)
		}
		if empty := containsEmpty(lastModelFCIDs); empty {
			t.Errorf("最后一条 model 的 FC id 含空串(会与所有 FR 失配)")
		}
	}
	if len(lastUserFRIDs) != 8 {
		t.Errorf("最后一条 user 的 FR 数=%d, want 8", len(lastUserFRIDs))
	} else {
		if dup := firstDup(lastUserFRIDs); dup != "" {
			t.Errorf("最后一条 user 的 FR id 重复: %q", dup)
		}
		if empty := containsEmpty(lastUserFRIDs); empty {
			t.Errorf("最后一条 user 的 FR id 含空串(不能匹配任何 FC id)")
		}
	}
	// 一一对应:每个 FC id 必须在 FR id 集合里出现。
	frSet := map[string]bool{}
	for _, id := range lastUserFRIDs {
		frSet[id] = true
	}
	for _, id := range lastModelFCIDs {
		if !frSet[id] {
			t.Errorf("id 失配: FC id=%q 在最后一条 user 的 8 个 FR id 里没有对应 tool_result", id)
		}
	}

	// (c) 跨消息邻接不变式。
	if err := assertToolAdiacency(gem.Contents); err != nil {
		t.Errorf("邻接不变式违反: %v", err)
	} else {
		t.Logf("✓ 邻接不变式通过")
	}

	// (d) 典范顺序:model 消息里 Text 不得出现在 FunctionCall 之后。
	if bad := assertAnthropicCanonicalPartOrder(gem.Contents); bad != "" {
		t.Errorf("典范顺序违反: %s", bad)
	} else {
		t.Logf("✓ 典范顺序通过")
	}
}

func collectID(cur []string, id string) []string { return append(cur, id) }

func firstDup(ids []string) string {
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			return id
		}
		seen[id] = true
	}
	return ""
}

func containsEmpty(ids []string) bool {
	for _, id := range ids {
		if id == "" {
			return true
		}
	}
	return false
}

// TestResponsesToolAdjacency_ParallelEightRawJSON 用 json.Marshal 直接打回 Gemini JSON,
// 确认 functionResponse.id 字段在 omitempty 下是否被丢(空 id 会被 omitempty 抹掉,从而
// daily-cloudcode-pa 重译时拿不到 tool_result 的对应 tool_use_id)。
func TestResponsesToolAdjacency_ParallelEightRawJSON(t *testing.T) {
	SetGlobalEnableThinkingMode(true)
	defer SetGlobalEnableThinkingMode(true)

	callIDs := []string{"c1", "c2", "c3", "c4", "c5", "c6", "c7", "c8"}
	var inputItems []string
	inputItems = append(inputItems, `{"type":"message","role":"user","content":"TTTT"}`)
	for _, id := range callIDs {
		inputItems = append(inputItems, `{"type":"function_call","call_id":"`+id+`","name":"shell_command","arguments":"{}"}`)
	}
	for _, id := range callIDs {
		inputItems = append(inputItems, `{"type":"function_call_output","call_id":"`+id+`","output":"ok"}`)
	}
	cmdJSON := `{"model":"claude-opus-4-6-thinking","stream":true,"input":[` + strings.Join(inputItems, ",") + `]}`
	openReq, err := ParseUnifiedOpenAIRequest([]byte(cmdJSON))
	if err != nil {
		t.Fatalf("ParseUnifiedOpenAIRequest: %v", err)
	}
	gem := TranslateOpenAIToGemini(openReq)

	out, err := json.Marshal(gem)
	if err != nil {
		t.Fatalf("json.Marshal gem: %v", err)
	}
	// 统计 functionResponse.id 在最终 JSON 里出现的次数(应 == 8)。
	frIDCount := strings.Count(string(out), `"functionResponse"`)
	t.Logf("最终 Gemini JSON 中 functionResponse 出现次数 = %d(期望 8)", frIDCount)
	if frIDCount != 8 {
		t.Errorf("functionResponse 数=%d, want 8", frIDCount)
	}

	// 反序列化回 map,逐一检查每个 functionResponse 是否带 id 且 id 非空。
	var raw map[string]interface{}
	if err := json.Unmarshal(out, &raw); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	contents, _ := raw["contents"].([]interface{})
	emptyFRID := 0
	frIDsSeen := map[string]bool{}
	for _, ci := range contents {
		c, _ := ci.(map[string]interface{})
		parts, _ := c["parts"].([]interface{})
		for _, pi := range parts {
			p, _ := pi.(map[string]interface{})
			if fr, ok := p["functionResponse"].(map[string]interface{}); ok {
				id, _ := fr["id"].(string)
				if id == "" {
					emptyFRID++
				} else {
					frIDsSeen[id] = true
				}
			}
		}
	}
	if emptyFRID > 0 {
		t.Errorf("发现 %d 个 functionResponse.id 为空(omitempty 会抹掉,导致 daily-cloudcode-pa 重译拿不到 tool_use_id)", emptyFRID)
	}
	if len(frIDsSeen) != 8 {
		t.Errorf("非空 functionResponse.id 数=%d, want 8", len(frIDsSeen))
	} else {
		t.Logf("✓ 8 个 functionResponse.id 全部非空且互异: %v", frIDsSeen)
	}
}
