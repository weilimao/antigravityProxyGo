package relay

// ocr_capability.go —— 多模态模型能力感知:OCR 自愈降级闸的"是否跳过降级"统一判据。
//
// 背景:OCR 自愈降级(image→文本)的初衷是给"不支持多模态的上游模型"兜底,避免 image 块
// 直送触发 400 / 内容丢失。但原实现三处入口各有启发式,且没有一个真正回答"这个模型支持多模态吗":
//   - Gemini 直连入口(compat_dispatch.go):名字含 "gemini" 即判多模态(粒度粗,gemini-embedding 也命中);
//   - NVIDIA 号池入口(nvidia.go):一律视为非多模态(只看 X-Antigravity-OCR-Self 守卫,不判模型);
//   - Other 号池入口(passthrough_forwarder.go):按上游协议格式判(openai→降,anthropic→不降)。
// 结果:多模态模型(qwen-vl / gpt-4o / kimi-k2 / 具名 vision 上游)被误降级,白烧本地 Gemini OCR
// 配额 + 丢失原生视觉理解;非多模态模型则靠"NVIDIA 一律降 / openai 格式一律降"恰好蒙对。
//
// 本文件把"模型是否多模态"收口为一份判据 OCRService.modelSupportsImage,三处入口共用:
//   1. 配置优先:RelayModelMapping 里该模型映射项的 Multimodal 声明位(用户显式开/关覆盖启发式);
//   2. 启发式兜底:配置缺省(nil)或未命中映射时,按模型名前缀白名单判定。
// 缺省方向保持旧行为不变(不致突然把图直送给原本配好的非多模态上游触发 400)。

import (
	"strings"
)

// mappingResolver 闭包:按入站/上游模型名查 RelayModelMapping 的多模态声明位。
// 由 APICompatHandler 注入(闭包捕获其 settingsMgr.GetRelayModelMapping),与 SetRouteResolver
// 同款解耦:OCRService 不持有整个 handler、不直接依赖 settings 包,只依赖纯函数契约;nil = 走启发式。
// 返回 (declaredPtr, found):
//   - declaredPtr=nil, found=false:未命中映射,走启发式兜底;
//   - declaredPtr=nil, found=true :命中但未显式声明 Multimodal(默认项即此态),走启发式兜底;
//   - declaredPtr=&true            :显式多模态,跳过降级;
//   - declaredPtr=&false          :显式非多模态,强制降级(否决冷门启发式误判)。
type mappingResolver func(model string) (declaredPtr *bool, found bool)

// multimodalModelPrefixes 是启发式白名单:模型名(转小写)含任一子串即判多模态。
// 仅收录业界高置信度的多模态模型族前缀,冷门模型走显式配置位(Multimodal 字段)兜底,
// 避免把名字撞上前缀但实为文本模型的上游误判成多模态而触发 400。
//
// 维护准则:新增前缀需确证该族至少有一个 vision 变体;纯文本族(如 o1-mini/gpt-3.5/deepseek-chat)
// 严禁收录。gpt-4o/gpt-4-turbo/gpt-4.1 系均原生支持 image,glm-4v/qwen-vl/internvl/
// llama-3.2-vision/gemma-3 同理;gemini 全系(含 1.5/2.0/2.5/3.x/pro/flash)均原生多模态。
var multimodalModelPrefixes = []string{
	"gemini",            // Google Gemini 全系(1.5/2.0/2.5/3.x/pro/flash,均原生多模态)
	"gpt-4o",            // OpenAI gpt-4o / gpt-4o-mini(Vision)
	"gpt-4.1",           // OpenAI gpt-4.1 / gpt-4.1-mini(Vision)
	"gpt-4-turbo",       // OpenAI gpt-4-turbo(Vision)
	"qwen-vl",           // 阿里通义千问 VL 视觉系(qwen-vl-plus/max/next)
	"qwen2-vl",          // Qwen2-VL
	"qwen2.5-vl",        // Qwen2.5-VL
	"glm-4v",            // 智谱 GLM-4V 视觉系
	"glm-4.5v",          // 智谱 GLM-4.5V
	"internvl",          // InternVL 视觉系
	"llama-3.2-vision",  // Meta Llama 3.2 Vision
	"llama-3.3-vision",  // Meta Llama 3.3 Vision 兼容命名
	"minicpm-v",         // 面壁 MiniCPM-V 视觉系
	"gemma-3",           // Google Gemma 3(含视觉)
	"claude-3.5-sonnet", // Anthropic Claude 3.5 Sonnet(Vision;2.0 同款,但前缀不与 text-only 族相撞)
	"claude-3-5-sonnet", // Anthropic Claude 3.5 Sonnet 旧写法(连字符变体)
	"claude-3.7-sonnet", // Anthropic Claude 3.7 Sonnet(Vision)
	"claude-sonnet-4",   // Claude Sonnet 4 / 4.5(Vision)
	"claude-opus-4",     // Claude Opus 4 / 4.5(Vision)
	"claude-3-opus",     // Anthropic Claude 3 Opus(Vision)
	"kimi-k2",           // Moonshot Kimi K2 视觉系(k2 多模态)
	"step-1v",           // 阶跃星辰 Step-1V
	"step-1.5v",         // 阶跃星辰 Step-1.5V
	"yi-vl",             // 零一万物 Yi-VL
	"deepseek-vl",       // DeepSeek VL 视觉系
	"o4-mini",           // OpenAI o4-mini(多模态推理)
}

// heuristicModelSupportsImage 按模型名前缀白名单启发式判定是否多模态。
// 纯函数,不依赖外部状态,供 modelSupportsImage 在配置缺省时兜底,也可独立单测。
// 剥离 [1M] 等上下文窗口后缀后再匹配(与 ResolveNvidiaModel 同款口径),避免
// "gemini-2.5-flash[1M]" 因 [1M] 后缀导致 "gemini" 前缀误判失败。
func heuristicModelSupportsImage(model string) bool {
	name := strings.ToLower(strings.TrimSpace(model))
	if name == "" {
		return false
	}
	// 剥离 [1M] 等显式上下文窗口后缀(与 account.ResolveNvidiaModel 一致)。
	if i := strings.Index(name, "["); i > 0 {
		name = strings.TrimSpace(name[:i])
	}
	for _, p := range multimodalModelPrefixes {
		if strings.Contains(name, p) {
			return true
		}
	}
	return false
}

// modelSupportsImage 是 OCR 自愈降级闸的唯一判据:返回目标模型是否原生支持多模态。
// 返回 true 时调用方应跳过 image→文本降级,把 image 块原样透传给上游(省 OCR 配额 +
// 保留原生视觉理解);返回 false 时调用方应继续降级。
//
// 判定优先级(与设计一致):
//  1. 账号级自定义映射(OpticNvidiaModel / Other Tailwind 时不改)优先:暂不接入,
//     走全局 RelayModelMapping 即可(同号池自定义映射后续按需扩展);
//  2. 全局 RelayModelMapping 的 Multimodal 声明位(配置优先):
//     - 显式 true  → 跳过降级(强制多模态);
//     - 显式 false → 强制降级(否决启发式误判);
//     - nil / 未命中 → 走启发式兜底;
//  3. 启发式 heuristicModelSupportsImage(模型名前缀白名单)兜底。
//
// mappingResolver 未注入(nil)时直接走启发式(单测/未注入场景的旧行为兼容)。
func (s *OCRService) modelSupportsImage(model string) bool {
	if s == nil {
		return false
	}
	// 映射表查询通过注入的 mappingResolver 闭包,避免 OCRService 直接依赖 settings.ManagerInterface
	// 的 GetRelayModelMapping(settingsMgr 虽在字段内,但 relay 单测常注入 nil manager,
	// 故统一走闭包,与 routeResolver 同款解耦)。闭包未注入 → 跳过配置层,直接启发式。
	if s.mappingResolver != nil {
		declared, found := s.mappingResolver(model)
		if found && declared != nil {
			// 显式声明:无论 true/false 都以配置为准,覆盖启发式。
			if *declared {
				s.logf("模型 %q 判定为多模态(显式配置),跳过 image→文本降级", model)
				return true
			}
			s.logf("模型 %q 判定为非多模态(显式配置),强制走 image→文本降级", model)
			return false
		}
		// found=false 或 declared=nil:走启发式兜底(保持旧行为)。
	}
	supports := heuristicModelSupportsImage(model)
	if supports {
		s.logf("模型 %q 判定为多模态(启发式),跳过 image→文本降级", model)
	}
	return supports
}

// SetMappingResolver 注入模型映射查询闭包(闭包捕获 APICompatHandler.getRelayModelMappingSafe)。
// 与 SetRouteResolver 同款解耦:nil = 旧行为(纯启发式,不查配置),用于 relay 单测未注入场景。
func (s *OCRService) SetMappingResolver(fn mappingResolver) {
	if s != nil {
		s.mappingResolver = fn
	}
}
