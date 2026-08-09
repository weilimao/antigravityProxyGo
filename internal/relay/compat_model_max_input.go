package relay

import "antigravity-proxy/internal/settings"

// compat_model_max_input.go: 裸名 /v1/models(Anthropic 形态)的 max_input_tokens 查询函数。
// 从 compat_models.go 拆出的职责补充:handleModels 按 ModelMappingEntry(ClientModel/TargetModel)
// 显式配置的 MaxInputTokens 为每条暴露模型附加 Anthropic 官方 Models API schema 字段。
//
// 查询函数为 nil 时调用方不附加字段:settingsMgr 为 nil(测试构造或未注入)时,由
// buildExposedModelMap 直接取 entry.MaxInputTokens(默认 0,不附加),行为与旧版等价。

// modelMaxInputTokensResolve 返回「ClientModel(暴露名)→ 上下文窗口」的查询函数。
// 解析规则:
//   - 映射条目显式配置 MaxInputTokens(>0):取该值;
//   - 未配置:0(不附加 max_input_tokens 字段),保持旧行为逐字节等价。
//
// settingsMgr 为 nil 时返回 nil(调用方降级为 entry.MaxInputTokens 直读,等价 0)。
func (h *APICompatHandler) modelMaxInputTokensResolve() func(string) int64 {
	if h.settingsMgr == nil {
		return nil
	}
	byTarget := map[string]int64{}
	for _, entry := range h.settingsMgr.GetRelayModelMappingSafe() {
		if entry.MaxInputTokens == nil || *entry.MaxInputTokens <= 0 {
			continue
		}
		tm := entry.TargetModel
		if tm != "" {
			byTarget[tm] = *entry.MaxInputTokens
		}
	}
	return func(clientModel string) int64 {
		if v, ok := byTarget[clientModel]; ok {
			return v
		}
		return 0
	}
}

// 显式 `var _` 使用 settings 包引用时保证 import 在 gofmt 后不被移除。
// 注:此文件仅引用了 settings.ModelMappingEntry 类型,该类型通过 GetRelayModelMappingSafe
// 返回依旧编译期可达(实现于 settings.ManagerInterface),故无需上述 if 保护。
var _ = settings.ModelMappingEntry{}