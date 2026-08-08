package relay

// ocr_prompt_test.go —— 锁定 OCR 保真条款(转写铁律 / 不确定标注)与单图/批量
// prompt 的单一信息源契约。新增铁律此前不被任何测试断言,删改亦不会显红,本文件补上。
//
// 覆盖:
//   - buildSingleOcrPrompt:靶向模板(有 promptCtx)与通用模板(空)各含铁律核心句、
//     不确定标注、空间结构条款;且两条模板的铁律核心句与 buildBatchOcrPrompt/batchMarkerRule
//     消费同一常量(单图/批量口径一致,单一信息源不漂移)。
//   - buildBatchOcrPrompt:header 与 batchMarkerRule 各含铁律核心句、不确定标注。
//   - 不确定标注前缀「[此段因不清晰未完全确认,原文如下]」与批量拆分标记「[[图k]]」
//     不碰撞(串级子串断言,锚定 splitBatchOcrText 不会被不确定标注误判为段边界)。

import (
	"strings"
	"testing"
)

// assertOcrFidelityClauses 断言一段 OCR prompt 文案同时含铁律核心句与不确定标注条款
// 的两个固定锚点子串。集中断言避免每个测试函数重复同一串 Contains 模板。
func assertOcrFidelityClauses(t *testing.T, label, prompt string) {
	t.Helper()
	// 铁律核心句:严禁改写字形 + 给出 l/I、0/O、1/l 典型示例(抑制 OCR 模型按经验重写)。
	for _, want := range []string{"逐字符可见", "严禁改写字形", "`l` 写成 `I`", "`0` 写成 `O`", "`1` 写成 `l`"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("%s 缺少铁律核心句子串 %q:\n%s", label, want, prompt)
		}
	}
	// 不确定标注条款:固定前缀 + 严禁编造剩余字符。
	for _, want := range []string{ocrMarkerUncertaintyPrefix, "严禁编造剩余字符"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("%s 缺少不确定标注子串 %q:\n%s", label, want, prompt)
		}
	}
}

func TestBuildSingleOcrPrompt_TargetedTemplateContainsFidelityClauses(t *testing.T) {
	p := buildSingleOcrPrompt("帮我看看这个报错")
	// 靶向模板必含用户提问上下文锚点(绑定靶向分支)。
	if !strings.Contains(p, "帮我看看这个报错") {
		t.Errorf("靶向模板应注入 promptCtx: %s", p)
	}
	if !strings.Contains(p, "[重点靶向分析]") {
		t.Errorf("靶向模板应含靶向分析条: %s", p)
	}
	assertOcrFidelityClauses(t, "单图靶向模板", p)
	// 空间结构条。
	if !strings.Contains(p, "[空间与结构]") {
		t.Errorf("单图靶向模板应含空间结构条: %s", p)
	}
}

func TestBuildSingleOcrPrompt_GenericTemplateContainsFidelityClauses(t *testing.T) {
	p := buildSingleOcrPrompt("")
	// 通用模板不含用户提问上下文段。
	if strings.Contains(p, "用户提问上下文") {
		t.Errorf("通用模板不应含用户提问上下文段: %s", p)
	}
	if !strings.Contains(p, "[图像总体概览]") {
		t.Errorf("通用模板应含图像总体概览条: %s", p)
	}
	if !strings.Contains(p, "[视觉布局与逻辑关系]") {
		t.Errorf("通用模板应含视觉布局条: %s", p)
	}
	assertOcrFidelityClauses(t, "单图通用模板", p)
}

func TestBuildBatchOcrPrompt_ContainsFidelityClauses(t *testing.T) {
	p := buildBatchOcrPrompt("帮我看看这个报错", 3)
	// header 内含铁律核心句。
	assertOcrFidelityClauses(t, "批量 header", p)
	// batchMarkerRule 内铁律第 0 条 + 不确定标注也来自同常量,整体应只各出现一次核心词锚点
	// 不强求次数(铁律核心句在 header 与 rule0 各一次属刻意强调),只确认两段都消费了同常量。
	if strings.Count(p, "严禁改写字形") < 1 {
		t.Errorf("批量 prompt 应含铁律: %s", p)
	}
}

func TestBuildBatchOcrPrompt_NoPromptCtxStillHasFidelityClauses(t *testing.T) {
	p := buildBatchOcrPrompt("", 2)
	if strings.Contains(p, "用户提问上下文") {
		t.Errorf("空 promptCtx 不应拼用户提问上下文段: %s", p)
	}
	assertOcrFidelityClauses(t, "批量无上下文", p)
}

// TestBuildSingleOcrPrompt_AndBatchShareFidelitySource 锁定单图与批量 prompt 消费
// 同一 ocrFidelityCore / ocrUncertaintyClause 常量:二者分别构造的 prompt 都精确包含
// 该常量原文(而非语义近似的另写),从而"改一处即同步"的单一信息源契约成立。
func TestBuildSingleOcrPrompt_AndBatchShareFidelitySource(t *testing.T) {
	singleTargeted := buildSingleOcrPrompt("ctx")
	singleGeneric := buildSingleOcrPrompt("")
	batchWith := buildBatchOcrPrompt("ctx", 2)
	batchNo := buildBatchOcrPrompt("", 2)
	for _, pmpt := range []struct {
		name, val string
	}{
		{"单图靶向", singleTargeted},
		{"单图通用", singleGeneric},
		{"批量有上下文", batchWith},
		{"批量无上下文", batchNo},
	} {
		if !strings.Contains(pmpt.val, ocrFidelityCore) {
			t.Errorf("%s prompt 未逐字包含 ocrFidelityCore,单图/批量单一信息源破裂:\n%s", pmpt.name, pmpt.val)
		}
		if !strings.Contains(pmpt.val, ocrUncertaintyClause) {
			t.Errorf("%s prompt 未逐字包含 ocrUncertaintyClause,单图/批量单一信息源破裂:\n%s", pmpt.name, pmpt.val)
		}
		if !strings.Contains(pmpt.val, ocrMarkerUncertaintyPrefix) {
			t.Errorf("%s prompt 未含不确定标注固定前缀:\n%s", pmpt.name, pmpt.val)
		}
	}
}

// TestOcrUncertaintyPrefixDoesNotCollideWithBatchMarker 锁定不确定标注前缀
// 「[此段因不清晰未完全确认,原文如下]」(单方括号)与批量拆分标记「[[图k]]」(双方括号)
// 不发生串级误判:splitBatchOcrText 扫描 [[图k]] 时被单方括号前缀干扰不到。
// 构造一段模拟输出:图1段里含不确定标注 + 报错,后接图2,确认能正确按标记拆成 2 段。
func TestOcrUncertaintyPrefixDoesNotCollideWithBatchMarker(t *testing.T) {
	text := `[[图1]]某 IDE 截图。` + ocrMarkerUncertaintyPrefix + `
Traceback (most recent call last):
  File "x.py", line 10
TypeError: foo
[[图2]]第二张图是架构流程图。A → B → C`
	segs, ok := splitBatchOcrText(text, 2)
	if !ok {
		t.Fatalf("含不确定标注的批量输出应能按 [[图k]] 拆为 2 段,ok=false\ntext=%s", text)
	}
	if len(segs) != 2 {
		t.Fatalf("want 2 segs, got %d: %v", len(segs), segs)
	}
	// 图1段应保留不确定标注前缀(未被当段边界吃掉)。
	if !strings.Contains(segs[0], ocrMarkerUncertaintyPrefix) {
		t.Errorf("图1段应含不确定标注前缀, got: %q", segs[0])
	}
	if !strings.Contains(segs[0], "TypeError: foo") {
		t.Errorf("图1段应含报错正文, got: %q", segs[0])
	}
	if !strings.Contains(segs[1], "架构流程图") {
		t.Errorf("图2段应含概览, got: %q", segs[1])
	}
}
