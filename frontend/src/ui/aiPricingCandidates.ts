/**
 * aiPricingCandidates.ts —— 「AI 一键生成计费」候选模型清单的纯函数计算。
 *
 * 数据源:
 *   - state.statsData.models (模型统计实时表,键可能是带前缀的全名,如 z-ai/glm-5.2)
 *   - state.pricingConfig (已登记单价的计费配置,键已 Lower-case)
 *
 * 清洗规则(逐项,与后端 pricing.GetPricingForModel 的基名降级语义对齐):
 *   1. 跳过 "unknown"(兜底模型,不应被登记为可优先生成的候选)。
 *   2. 取最后一个 "/" 后的基名(deepseek-ai/deepseek-v4-flash → deepseek-v4-flash),
 *      与计费匹配引擎的斜杠后缀降级同口径,保证登记键与匹配键语义一致。
 *   3. trim + Lower-case 后与已登记集合比对,命中则跳过(已配置的不重复生成)。
 *   4. Set 去重(a/b/x 与 c/b/x 合并为单个 x),字典序排序。
 *
 * 纯函数无副作用、无 DOM 依赖,独立成文件便于 aiPricingCandidates.test.mjs 单测,
 * 避免把逻辑塞进 aiPricingController 致 controller 膨胀。
 */

/** stats 表里一条模型的形态(只用到键名,值字段不强约束)。 */
interface StatsModelEntry {
    [k: string]: any;
}

/**
 * computeAiPricingCandidates 计算需要 AI 生成单价的候选模型基名清单。
 *
 * @param statsModels 模型统计表的对象形态 {modelName: {...}},可为 null/undefined
 * @param existingPricing 已登记计费配置 {modelNameLower: {input,...}},可为 null/undefined
 * @returns 去重排序后的候选模型基名数组(小写,无前缀),空则返回 []
 */
export function computeAiPricingCandidates(
    statsModels: Record<string, StatsModelEntry> | null | undefined,
    existingPricing: Record<string, any> | null | undefined
): string[] {
    if (!statsModels || typeof statsModels !== 'object') {
        return [];
    }

    // 已登记键集合(小写比对)。pricingConfig 的键已由后端 strings.ToLower 归一,
    // 这里防御性再小写一次,兼容手动新增可能输入的大小写漂移。
    const existing = new Set<string>();
    if (existingPricing && typeof existingPricing === 'object') {
        for (const k of Object.keys(existingPricing)) {
            const norm = String(k).trim().toLowerCase();
            if (norm) existing.add(norm);
        }
    }

    const out = new Set<string>();
    for (const key of Object.keys(statsModels)) {
        if (!key) continue;
        let base = key;
        const slash = key.lastIndexOf('/');
        if (slash >= 0) {
            base = key.slice(slash + 1);
        }
        base = base.trim().toLowerCase();
        if (!base) continue;
        if (base === 'unknown') continue;
        if (existing.has(base)) continue;
        out.add(base);
    }

    const arr = Array.from(out);
    arr.sort();
    return arr;
}
