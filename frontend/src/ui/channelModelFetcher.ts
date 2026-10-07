import { ipcRenderer } from '../shared/ipc';

export interface CategorizedModels {
    gemini: string[];
    claude: string[];
    others: string[];
}

export const DEFAULT_FALLBACK_MODELS: string[] = [
    'gemini-3.5-flash',
    'gemini-3.5-flash-low',
    'gemini-3.5-flash-extra-low',
    'gemini-3.1-flash-lite',
    'gemini-3.1-pro-low',
    'gemini-3.1-pro-preview',
    'gemini-3-flash',
    'gemini-3-flash-preview',
    'gemini-3-flash-agent',
    'gemini-pro-agent',
    'gemini-2.5-flash',
    'gemini-2.5-flash-lite',
    'claude-sonnet-4-6',
    'claude-opus-4-6-thinking',
    'gpt-oss-120b-medium',
    'tab_flash_lite_preview',
    'tab_jump_flash_lite_preview',
];

const channelModelsCache: Record<string, string[]> = {};

/**
 * 将模型列表按前缀与关键字动态归入 Gemini、Claude 与 Others 三类
 */
export function categorizeModels(models: string[]): CategorizedModels {
    const gemini: string[] = [];
    const claude: string[] = [];
    const others: string[] = [];
    const seen = new Set<string>();

    for (const raw of models) {
        const m = (raw || '').trim();
        if (!m || seen.has(m)) continue;
        seen.add(m);

        const lower = m.toLowerCase();
        if (lower.startsWith('gemini') || lower.includes('gemini')) {
            gemini.push(m);
        } else if (lower.startsWith('claude') || lower.includes('claude')) {
            claude.push(m);
        } else {
            others.push(m);
        }
    }

    return { gemini, claude, others };
}

/**
 * 合并远端获取的模型与原任务已保存/已选中的模型，防止旧配置在刷新后丢失
 */
export function mergeModelsWithPreserved(fetched: string[], preserved: string[] = []): string[] {
    const seen = new Set<string>();
    const result: string[] = [];

    for (const item of fetched) {
        const trimmed = (item || '').trim();
        if (trimmed && !seen.has(trimmed)) {
            seen.add(trimmed);
            result.push(trimmed);
        }
    }

    for (const item of preserved) {
        const trimmed = (item || '').trim();
        if (trimmed && !seen.has(trimmed)) {
            seen.add(trimmed);
            result.push(trimmed);
        }
    }

    return result;
}

/**
 * 获取指定渠道的已缓存模型（若未拉取过则返回 null）
 */
export function getCachedModels(channel?: string): string[] | null {
    const key = (channel || 'google').trim().toLowerCase();
    return channelModelsCache[key] || null;
}

/**
 * 调用 relay:fetch-channel-models 获取指定渠道的最新模型全集
 */
export async function fetchChannelModels(channel?: string): Promise<{ success: boolean; models: string[]; error?: string }> {
    const ch = (channel || 'google').trim().toLowerCase();
    try {
        const res = await ipcRenderer.invoke('relay:fetch-channel-models', ch);
        if (res && res.success && Array.isArray(res.models) && res.models.length > 0) {
            channelModelsCache[ch] = res.models;
            return { success: true, models: res.models };
        }
        const fallback = channelModelsCache[ch] || DEFAULT_FALLBACK_MODELS;
        return {
            success: false,
            models: fallback,
            error: res?.error || '上游未返回可用模型列表',
        };
    } catch (e: any) {
        const fallback = channelModelsCache[ch] || DEFAULT_FALLBACK_MODELS;
        return {
            success: false,
            models: fallback,
            error: e?.message || '网络请求异常',
        };
    }
}

/**
 * 跨弹窗通用的分类复选框渲染器
 */
export interface ModelRendererOptions {
    geminiContainer: HTMLDivElement | null;
    claudeContainer: HTMLDivElement | null;
    othersContainer: HTMLDivElement | null;
    checkboxClass: string;
    checkboxName?: string;
    idPrefix: string;
    emptyTexts?: {
        gemini?: string;
        claude?: string;
        others?: string;
    };
}

export function renderCategorizedCheckboxes(
    models: string[],
    selectedSet: Set<string>,
    options: ModelRendererOptions
): void {
    const { geminiContainer, claudeContainer, othersContainer, checkboxClass, checkboxName, idPrefix, emptyTexts } = options;
    if (!geminiContainer || !claudeContainer || !othersContainer) return;

    const categorized = categorizeModels(models);

    const renderGroup = (container: HTMLDivElement, list: string[], emptyText: string) => {
        container.innerHTML = '';
        if (list.length === 0) {
            container.innerHTML = `<div class="text-[10px] text-outline/60 italic py-1">${emptyText}</div>`;
            return;
        }
        list.forEach(m => {
            const isChecked = selectedSet.has(m);
            const safeId = m.replace(/[^a-zA-Z0-9]/g, '_');
            const div = document.createElement('div');
            div.className = 'flex items-center gap-1.5 text-[11px] truncate';
            const nameAttr = checkboxName ? `name="${checkboxName}"` : '';
            div.innerHTML = `
                <input type="checkbox" ${nameAttr} id="${idPrefix}_${safeId}" value="${m}" class="${checkboxClass} rounded border-outline-variant/40 text-primary focus:ring-primary cursor-pointer" ${isChecked ? 'checked' : ''}>
                <label for="${idPrefix}_${safeId}" class="truncate font-mono text-[10.5px] cursor-pointer select-none" title="${m}">${m}</label>
            `;
            container.appendChild(div);
        });
    };

    renderGroup(geminiContainer, categorized.gemini, emptyTexts?.gemini || '暂无 Gemini 模型');
    renderGroup(claudeContainer, categorized.claude, emptyTexts?.claude || '暂无 Claude 模型');
    renderGroup(othersContainer, categorized.others, emptyTexts?.others || '暂无其它模型');
}

