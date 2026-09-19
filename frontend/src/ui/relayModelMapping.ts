/**
 * relayModelMapping.ts: Relay 模型映射配置子面板动态号池 Tab 与模型映射表格交互。
 *
 * 从 relayController.ts initRelayEvents 闭包内 D 簇抽离(原 L220-1054)。原闭包内 function 声明迁为模块私有,
 * 5 个闭包内 let(allMappings/poolTabs/activeTabId/availableChannels/channelModelsCache)提升为模块作用域,
 * initRelayModelMapping() 顶部显式重置保持「每次 re-mount 重置」闭包语义(单一语义敏感点)。
 * 5 个 window._relay* 赋值移入 initRelayModelMapping 体内,经 hub initRelayEvents 在同一 onMounted 时机注册。
 */
import { ipcRenderer } from '../shared/ipc';
import state from './dashboardState';
import i18n from '../shared/i18n';
import {
    buildLiveSetLower, buildStaleConfirmPrompt, computeRemoteAddedLower,
    MappingLike, mappingBadgeHTML, shouldMarkNew, shouldMarkStale,
} from './relayModelDiff';

// ===== 模块作用域状态(每次 initRelayModelMapping 显式重置,保持闭包语义) =====
let allMappings: any[] = [];
let poolTabs: PoolTabInfo[] = [];
let activeTabId: string = 'google';
let availableChannels: string[] = ['antigravity', 'google', 'gcp', 'nvidia', 'other', 'grok', 'workbuddy'];
let channelModelsCache: Record<string, string[]> = {};
// Other 组上次拉取的「远端全集」缓存,作为 Other 组「新增」diff 的小快照基准
// (Other 走独立 IPC 无后端快照,前端自持。全局 Tab 走后端 snapshot 字段,无需此)。
let channelModelsCachePrev: Record<string, string[]> = {};
let modelMappingSearchQuery: string = '';
// diff 标记集合(按号池 channel 缓存,跨「获取号池模型」刷新覆盖):
//   channelAddedLower[channel] —— 该号池本轮「新增」模型的小写集合(本次远端 − 上次快照);
//   channelStaleLower[channel] —— 该号池「远端已删除」模型的小写集合(TargetModel 不在本次远端全集)。
// 拉取失败/未拉取时各自为空集(不下判,防误标/误删)。供 renderCurrentTabTable 逐行挂徽章 +
// 「清除失效模型」按钮计算可删行。
let channelAddedLower: Record<string, Set<string>> = {};
let channelStaleLower: Record<string, Set<string>> = {};

// ========== 动态号池 Tab 与模型映射配置交互 ==========
interface PoolTabInfo {
    id: string;
    name: string;
    targetProvider: string;
    isCustom?: boolean;
}

// isGoogleProviderKind 判定 provider 是否属于 Google 族号池(走 /v1/chat/completions 等裸名直连链路)。
// 口径与后端 internal/relay/router_entry.go 的 isGoogleProvider 一致:
//   google / gcp / antigravity / gemini-cli / 空(兜底视为 Google 族) → true。
// 非族:nvidia / deepseek / qwen / moonshot / 自定义 provider → false,这类号池的 ClientModel
//   需带 "{provider}/" 前缀以经 /route/* 精准路由(见 resolveRoutedTarget)。
function isGoogleProviderKind(p: string): boolean {
    const c = (p || '').trim().toLowerCase();
    return c === 'google' || c === 'gcp' || c === 'antigravity' || c === 'gemini-cli' || c === '';
}

// makeMappingEntry 构造一条模型映射对象,字段口径与后端 settings.ModelMappingEntry 对齐。
// injectChatTemplateKwargs 仅对 NVIDIA 号池有意义(默认 true;由 _relayAddModelMapping / 表格勾选控制),
// 其余号池该字段不参与透传,保持 undefined 即可。
// chat_template_kwargs 是 NVIDIA NIM 专属约定, Other 号池各第三方上游思考参数格式各异
// (阿里云 DeepSeek v4 要 bool、NIM 要字符串、有的根本不认), 强塞会触发上游 400 类型不匹配。
// 故 Other 号池条目默认关闭注入(后端 isNvidiaModelNoKwargs 也会兜底抑制, 此处保持一致)。
function makeMappingEntry(clientModel: string, targetModel: string, provider: string, expose: boolean): any {
    return {
        clientModel,
        targetModel,
        targetProvider: provider,
        expose,
        ownedBy: '',
        injectChatTemplateKwargs: provider !== 'other',
        variantEfforts: [],
        candidateModels: [],
        useBenchmarkPool: false
    };
}

export async function loadModelMappings() {
    try {
        // 1. 获取可用渠道列表 (若后端提供)
        try {
            const chans = await ipcRenderer.invoke('relay:get-channels');
            if (Array.isArray(chans) && chans.length > 0) {
                availableChannels = Array.from(new Set([...availableChannels, ...chans]));
            }
        } catch (_) {}

        // 2. 获取模型映射数据
        const list = await ipcRenderer.invoke('relay:get-model-mapping');
        allMappings = list || [];

        // 3. 构建默认 Tab 列表 (默认预设,对齐账号池页面顶部通道,含 Other/Grok/WorkBuddy/OpenCode)
        poolTabs = [
            { id: 'google', name: 'Gemini (Google)', targetProvider: 'google' },
            { id: 'nvidia', name: 'NVIDIA 号池', targetProvider: 'nvidia' },
            { id: 'other', name: 'Other 号池', targetProvider: 'other' },
            { id: 'gcp', name: '谷歌云 API', targetProvider: 'gcp' },
            { id: 'grok', name: 'Grok 号池', targetProvider: 'grok' },
            { id: 'workbuddy', name: 'WorkBuddy 号池', targetProvider: 'workbuddy' },
            { id: 'opencode', name: 'OpenCode 号池', targetProvider: 'opencode' }
        ];

        // 4. 根据已有 mappings 里的 ownedBy 填充可删除的自定义 Tab
        const knownIds = new Set(poolTabs.map(t => t.id));
        allMappings.forEach(m => {
            if (m.ownedBy && !knownIds.has(m.ownedBy)) {
                knownIds.add(m.ownedBy);
                poolTabs.push({
                    id: m.ownedBy,
                    name: (m.ownedBy.charAt(0).toUpperCase() + m.ownedBy.slice(1)) + ' 号池',
                    targetProvider: m.targetProvider || m.ownedBy,
                    isCustom: true
                });
            }
        });

        // 确保 activeTabId 合法
        if (!poolTabs.some(t => t.id === activeTabId)) {
            activeTabId = poolTabs[0].id;
        }

        renderPoolTabs();
        renderTargetProviderSelector();
        renderCurrentTabTable();
    } catch (err) {
        console.error('[RelayController] Failed to get model mappings:', err);
    }
}

function deleteTabById(tabId: string) {
    const tabToDelete = poolTabs.find(t => t.id === tabId);
    if (!tabToDelete || !tabToDelete.isCustom) return;

    if (!confirm(`确定要删除号池 Tab「${tabToDelete.name}」及其下的所有模型映射吗？`)) return;

    allMappings = allMappings.filter(m => getMappingTab(m) !== tabId);
    poolTabs = poolTabs.filter(t => t.id !== tabId);

    if (activeTabId === tabId) {
        activeTabId = poolTabs[0]?.id || 'google';
    }

    renderPoolTabs();
    renderTargetProviderSelector();
    renderCurrentTabTable();
}

function renderPoolTabs() {
    const nav = document.getElementById('modelMappingTabsNav');
    if (!nav) return;
    nav.innerHTML = '';
    poolTabs.forEach(tab => {
        const btn = document.createElement('button');
        const isActive = tab.id === activeTabId;
        btn.className = `px-3 py-1.5 rounded-lg text-[12px] font-bold transition-all cursor-pointer whitespace-nowrap flex items-center gap-1.5 ${
            isActive
                ? 'bg-primary text-white shadow-sm shadow-primary/30'
                : 'bg-slate-100 dark:bg-white/5 text-slate-600 dark:text-slate-300 hover:bg-slate-200 dark:hover:bg-white/10'
        }`;
        btn.innerHTML = `<span>${tab.name}</span>${tab.isCustom ? `<span class="material-symbols-outlined text-[14px] hover:text-red-400 ml-1 transition-colors btn-delete-tab-icon" title="删除此 Tab">close</span>` : ''}`;
        btn.addEventListener('click', (e) => {
            const target = e.target as HTMLElement;
            if (target.classList.contains('btn-delete-tab-icon')) {
                e.stopPropagation();
                deleteTabById(tab.id);
                return;
            }
            activeTabId = tab.id;
            renderPoolTabs();
            renderTargetProviderSelector();
            renderCurrentTabTable();
        });
        nav.appendChild(btn);
    });
}

function renderTargetProviderSelector() {
    const select = document.getElementById('tabTargetProviderSelect') as HTMLSelectElement | null;
    const customInput = document.getElementById('tabTargetProviderCustom') as HTMLInputElement | null;
    const btnDeleteTab = document.getElementById('btnDeleteCurrentTab');
    if (!select) return;

    const currentTab = poolTabs.find(t => t.id === activeTabId) || poolTabs[0];

    // 收集所有去重的 Provider 选项
    const providers = Array.from(new Set(['google', 'nvidia', 'gcp', 'antigravity', 'deepseek', 'qwen', 'anthropic', 'moonshot', ...availableChannels, currentTab.targetProvider].filter(Boolean)));

    select.innerHTML = providers.map(p => `<option value="${p}">${p}</option>`).join('') + `<option value="__custom__">+ 自定义 Provider...</option>`;

    if (providers.includes(currentTab.targetProvider)) {
        select.value = currentTab.targetProvider;
        if (customInput) customInput.classList.add('hidden');
    } else {
        select.value = '__custom__';
        if (customInput) {
            customInput.classList.remove('hidden');
            customInput.value = currentTab.targetProvider;
        }
    }

    // 控制删除按钮显示状态
    if (btnDeleteTab) {
        if (currentTab.isCustom) {
            btnDeleteTab.classList.remove('hidden');
        } else {
            btnDeleteTab.classList.add('hidden');
        }
    }

    // 绑定下拉切换事件
    select.onchange = () => {
        if (select.value === '__custom__') {
            if (customInput) {
                customInput.classList.remove('hidden');
                customInput.focus();
            }
        } else {
            if (customInput) customInput.classList.add('hidden');
            currentTab.targetProvider = select.value;
            // 同步更新当前 Tab 下的所有映射 targetProvider
            allMappings.forEach(m => {
                if (getMappingTab(m) === activeTabId) {
                    m.targetProvider = select.value;
                }
            });
        }
    };

    if (customInput) {
        customInput.oninput = () => {
            const val = customInput.value.trim();
            currentTab.targetProvider = val;
            allMappings.forEach(m => {
                if (getMappingTab(m) === activeTabId) {
                    m.targetProvider = val;
                }
            });
        };
    }
}

function getMappingTab(m: any): string {
    if (m.ownedBy) return m.ownedBy;
    // 隐式根据 clientModel/targetModel 归类
    const modelName = (m.clientModel || m.targetModel || '').toLowerCase();
    if (modelName.startsWith('other/')) return 'other';
    if (modelName.startsWith('nvidia/') || modelName.endsWith('-nemotron')) return 'nvidia';
    if (modelName.startsWith('grok/')) return 'grok';
    if (modelName.startsWith('workbuddy/')) return 'workbuddy';
    if (modelName.startsWith('opencode/')) return 'opencode';
    if (modelName.startsWith('deepseek')) return 'deepseek';
    if (modelName.startsWith('qwen')) return 'qwen';
    if (modelName.startsWith('claude')) return 'anthropic';
    return 'google';
}

function getTabMappings(): any[] {
    return allMappings.filter(m => getMappingTab(m) === activeTabId);
}

function updateDatalist(models: string[]) {
    const datalist = document.getElementById('channelModelsDatalist');
    if (!datalist) return;
    datalist.innerHTML = models.map(m => `<option value="${m}">${m}</option>`).join('');
}

// getOtherGroups:从后端 accounts-res 广播的 lastBackendData 读取 Other 号池组列表,
// 兼容直接调 other:list-groups 的回退。返回 [{groupId, groupName, formats}]。
async function getOtherGroups(): Promise<Array<{ groupId: string; groupName: string; formats: string[] }>> {
    try {
        const backendData = state.lastBackendData;
        if (backendData && Array.isArray(backendData.otherGroups) && backendData.otherGroups.length > 0) {
            return backendData.otherGroups.map((g: any) => ({
                groupId: String(g.groupId || g.groupID || g.id || ''),
                groupName: String(g.groupName || g.groupId || ''),
                formats: Array.isArray(g.formats) ? g.formats : []
            })).filter((g: any) => g.groupId);
        }
    } catch (e) { /* ignore */ }
    try {
        const res = await ipcRenderer.invoke('other:list-groups');
        if (res && res.success && Array.isArray(res.groups)) {
            return res.groups.map((g: any) => ({
                groupId: String(g.groupId || g.groupID || g.id || ''),
                groupName: String(g.groupName || g.groupId || ''),
                formats: Array.isArray(g.formats) ? g.formats : []
            })).filter((g: any) => g.groupId);
        }
    } catch (e) { /* ignore */ }
    return [];
}

// renderOtherGroupFetchButtons:Other Tab 专用的「按组获取模型」按钮组渲染。
// 每个已在号池配置的组渲染一个独立「获取 [组名] 模型」按钮,点击调 other:fetch-models(groupId),
// 拉到的模型自动补全为 other/{groupId}/{model} 三段前缀 ClientModel 映射条目。
function renderOtherGroupFetchButtons(container: HTMLElement | null) {
    if (!container) return;
    container.innerHTML = '';
    getOtherGroups().then(groups => {
        if (groups.length === 0) {
            container.innerHTML = `<span class="text-[11px] text-outline italic">Other 号池暂无组,请先在账号池添加 Other 账号创建组</span>`;
            container.classList.remove('hidden');
            container.classList.add('flex');
            return;
        }
        groups.forEach(g => {
            const btn = document.createElement('button');
            btn.className = 'flex items-center gap-1 px-2.5 py-1 text-[12px] font-medium bg-purple-500/10 text-purple-600 dark:text-purple-300 hover:bg-purple-500/20 rounded-lg transition-colors cursor-pointer border border-purple-500/20';
            const fmtTag = g.formats && g.formats.length
                ? ` (${g.formats.map(f => f === 'anthropic' ? 'A' : 'O').join('/')})`
                : '';
            btn.innerHTML = `<span class="material-symbols-outlined text-[15px]">sync</span><span>获取 ${escapeHtmlLocal(g.groupName || g.groupId)}${fmtTag}</span>`;
            btn.addEventListener('click', () => fetchOtherGroupModels(g.groupId, g.groupName || g.groupId, btn));
            container.appendChild(btn);
        });
        container.classList.remove('hidden');
        container.classList.add('flex');
    });
}

// fetchOtherGroupModels:调 other:fetch-models 拉指定组的上游模型,自动补全三段前缀映射条目。
async function fetchOtherGroupModels(groupId: string, groupName: string, btn: HTMLButtonElement) {
    const lblCount = document.getElementById('lblFetchedModelsCount');
    const origHTML = btn.innerHTML;
    btn.disabled = true;
    btn.innerHTML = `<span class="material-symbols-outlined text-[15px] animate-spin">sync</span><span>获取中...</span>`;

    try {
        const res = await ipcRenderer.invoke('other:fetch-models', groupId);
        if (res && res.success && Array.isArray(res.models)) {
            channelModelsCache[`other/${groupId}`] = res.models;
            updateDatalist(res.models);
            // diff:Other 组按组键 other/{groupId} 单独存「新增/失效」。other:fetch-models 走 app_account_ipc
            // (独立于 relay:fetch-channel-models,暂未返回 snapshot/added),故「新增」用前端按 res.models
            // 与本组上次缓存现场算(退化但可用),「失效」用本次全集(与全局 Tab 同口径,最稳)。
            const grpKey = `other/${groupId}`;
            const prevCache = channelModelsCachePrev[grpKey];
            channelAddedLower[grpKey] = prevCache
                ? computeRemoteAddedLower(res.models, prevCache)
                : new Set(res.models.map((s: string) => (s || '').trim().toLowerCase()).filter(Boolean));
            channelModelsCachePrev[grpKey] = res.models.slice();
            channelStaleLower[grpKey] = buildLiveSetLower(res.models);
            if (lblCount) {
                const newCount = channelAddedLower[grpKey].size;
                lblCount.textContent = `✅ [${groupName}] 已获取 ${res.models.length} 个模型${newCount > 0 ? ` · 新增 ${newCount}` : ''}`;
                lblCount.classList.remove('hidden');
            }
            // 自动补全:Other 组每个新模型生成三段前缀 other/{groupId}/{model} 单条映射。
            const provider = 'other';
            const tabId = 'other';
            const existingClientSet = new Set<string>();
            for (const m of allMappings) {
                const cm = (m.clientModel || '').trim();
                if (cm) existingClientSet.add(cm.toLowerCase());
            }
            // 按组作用域去重:仅收集「本组」(other/{groupId}/...) 已存在的上游模型名,
            // 避免全局 targetModel 去重误伤跨组同名模型(不同组是不同上游,模型名可相同)。
            const existingSameGroupTargetSet = new Set<string>();
            for (const m of allMappings) {
                const cm = (m.clientModel || '').trim().toLowerCase();
                const tm = (m.targetModel || '').trim().toLowerCase();
                if (!tm) continue;
                if (cm.startsWith(`${provider}/${groupId}/`)) {
                    existingSameGroupTargetSet.add(tm);
                }
            }
            const newEntries: any[] = [];
            for (const modelRaw of res.models) {
                const model = (modelRaw || '').trim();
                if (!model) continue;
                // 仅当本组已存在该上游模型时才跳过;跨组同名模型不拦截。
                if (existingSameGroupTargetSet.has(model.toLowerCase())) continue;
                const prefixed = `${provider}/${groupId}/${model}`;
                if (!existingClientSet.has(prefixed.toLowerCase())) {
                    const entry = makeMappingEntry(prefixed, model, provider, true);
                    entry.ownedBy = tabId;
                    entry.targetGroupId = groupId; // 三段前缀携带组 ID,供后端按组选号
                    newEntries.push(entry);
                    existingClientSet.add(prefixed.toLowerCase());
                }
                existingSameGroupTargetSet.add(model.toLowerCase());
            }
            if (newEntries.length > 0) {
                for (const ne of newEntries) allMappings.push(ne);
            }
            renderCurrentTabTable();
        } else if (res && res.allowManualInput) {
            // Anthropic-only 上游无 /v1/models 端点 → 提示手动填写,不补全条目。
            if (lblCount) {
                lblCount.textContent = `⚠️ [${groupName}] 上游暂不支持模型列表,请手动填写(前缀 other/${groupId}/)`;
                lblCount.classList.remove('hidden');
            }
        } else {
            if (lblCount) {
                lblCount.textContent = `❌ [${groupName}] 获取失败: ${res?.error || '网络超时'}`;
                lblCount.classList.remove('hidden');
            }
        }
    } catch (e: any) {
        console.error('[RelayController] Fetch other group models error:', e);
        if (lblCount) {
            lblCount.textContent = `❌ [${groupName}] 获取出错`;
            lblCount.classList.remove('hidden');
        }
    } finally {
        btn.disabled = false;
        btn.innerHTML = origHTML;
    }
}

// escapeHtmlLocal:本模块内联的轻量 HTML 转义,避免依赖外部工具函数。
function escapeHtmlLocal(s: string): string {
    return String(s)
        .replace(/&/g, '&')
        .replace(/</g, '<')
        .replace(/>/g, '>')
        .replace(/"/g, '"')
        .replace(/'/g, '&#39;');
}

// ensureOtherEntryGroupId:Other Tab 下保证某映射行携带 targetGroupId。
// 1) 行已有 targetGroupId → 直接返回;2) 无则向后端拉 Other 组列表,单组自动绑定、多组弹窗让用户选;
// 3) 无任何组 → 提示并返回空串(调用方据此保留裸名,等用户先去账号池建组)。
// 返回的 groupId 同时写回 entry.targetGroupId(entry 为 allMappings 元素引用,落盘一致)。
async function ensureOtherEntryGroupId(entry: any): Promise<string> {
    if (!entry) return '';
    const existing = (entry.targetGroupId || '').trim();
    if (existing) return existing;
    const groups = await getOtherGroups();
    if (!groups.length) {
        alert('Other 号池暂无组,请先在「账号池」添加 Other 账号创建组后再手动填写模型。');
        return '';
    }
    if (groups.length === 1) {
        entry.targetGroupId = groups[0].groupId;
        return groups[0].groupId;
    }
    const list = groups.map((g, i) => `${i + 1}. ${g.groupName || g.groupId} (ID: ${g.groupId})`).join('\n');
    const pick = prompt(`该映射未绑定 Other 组,请选择目标组(输入序号):\n${list}`, '1');
    if (!pick) return '';
    const n = parseInt(pick, 10);
    if (isNaN(n) || n < 1 || n > groups.length) {
        alert('序号无效,已取消自动填充 Client Model。请重新输入目标模型以再次触发组选择。');
        return '';
    }
    const g = groups[n - 1];
    entry.targetGroupId = g.groupId;
    return g.groupId;
}

function renderCurrentTabTable() {
    const tbody = document.getElementById('modelMappingTableBody');
    if (!tbody) return;
    tbody.innerHTML = '';

    const currentTabMappings = getTabMappings();
    const currentTab = poolTabs.find(t => t.id === activeTabId) || poolTabs[0];

    const isNvidiaTab = (currentTab.targetProvider === 'nvidia' || currentTab.id === 'nvidia');
    const isOtherTab = (currentTab.targetProvider === 'other' || currentTab.id === 'other');
    // Other 组模型缓存键为 other/{groupId}(fetchOtherGroupModels 写入),
    // 渲染下拉需按行所属组解析,不能只用全局 other 键(否则恒为空 → 显示「未拉取模型」)。
    const fetchedModels = isOtherTab
        ? []
        : (channelModelsCache[currentTab.targetProvider || currentTab.id] || []);
    // diff 标记集:按当前 Tab 的号池 channel 取「新增/失效」小写集合(Other 按 group 键,口径与下方 rowModels 一致)。
    // 用一个小写集合代表「本 Tab 视角下的远端全集」——失效比对就基于 fetchedModels(Other 时按行重解,故本键对 Other 不生效)。
    const channelKey = (currentTab.targetProvider || currentTab.id || '').toLowerCase();
    const addedSet = channelAddedLower[channelKey] || new Set<string>();
    const staleSet = channelStaleLower[channelKey] || new Set<string>();
    // 同步「清除失效模型」按钮:计数改走「当前 Tab 真实待删映射数」(collectStaleMappingsInTab),
    // 杜绝旧逻辑直接传 staleSet.size —— staleSet 实为「远端全集小写集合」(buildLiveSetLower 返回),
    // size 等于远端模型总数(如 103),与「新增 N」同源于一次拉取 → 数字恒等,且即便本 Tab 一条
    // 失效都没有(新增即全集,全在 liveSet 非失效)也会启用按钮 → 严重误导。此处改传真实失效数。
    // hasFetchedStaleBasis 供 staleCount==0 时区分「无失效」与「未拉取」两种 tooltip 文案。
    syncClearStaleBtn(collectStaleMappingsInTab(activeTabId).length, hasFetchedStaleBasis(activeTabId));
    const thInjectKwargs = document.getElementById('thInjectKwargs');
    if (thInjectKwargs) {
        if (isNvidiaTab) {
            thInjectKwargs.classList.remove('hidden');
        } else {
            thInjectKwargs.classList.add('hidden');
        }
    }

    // Other 号池:切到 Other Tab 时隐藏全局「获取号池模型」按钮,改为按组渲染多个获取按钮;
    // 非 Other Tab 时恢复全局按钮、清空组按钮容器。
    const btnFetchChannelModels = document.getElementById('btnFetchChannelModels');
    const otherFetchContainer = document.getElementById('otherGroupFetchContainer');
    if (otherFetchContainer) otherFetchContainer.innerHTML = '';
    if (isOtherTab) {
        if (btnFetchChannelModels) btnFetchChannelModels.classList.add('hidden');
        renderOtherGroupFetchButtons(otherFetchContainer);
    } else {
        if (btnFetchChannelModels) btnFetchChannelModels.classList.remove('hidden');
        if (otherFetchContainer) otherFetchContainer.classList.add('hidden');
    }

    if (fetchedModels.length > 0) {
        updateDatalist(fetchedModels);
    }

    // 联动控制清空搜索按钮显隐状态
    const btnClearSearch = document.getElementById('btnClearRelayModelMappingSearch') as HTMLButtonElement | null;
    if (btnClearSearch) {
        btnClearSearch.classList.toggle('hidden', !modelMappingSearchQuery);
    }

    // 应用关键词搜索筛选 (匹配 ClientModel / TargetModel / TargetProvider / TargetGroupId / VariantEfforts)
    const filteredMappings = modelMappingSearchQuery
        ? currentTabMappings.filter(item => {
            const cm = (item.clientModel || '').toLowerCase();
            const tm = (item.targetModel || '').toLowerCase();
            const tp = (item.targetProvider || '').toLowerCase();
            const gid = (item.targetGroupId || '').toLowerCase();
            const effs = Array.isArray(item.variantEfforts) ? item.variantEfforts.join(' ').toLowerCase() : '';
            return cm.includes(modelMappingSearchQuery) ||
                   tm.includes(modelMappingSearchQuery) ||
                   tp.includes(modelMappingSearchQuery) ||
                   gid.includes(modelMappingSearchQuery) ||
                   effs.includes(modelMappingSearchQuery);
        })
        : currentTabMappings;

    if (filteredMappings.length === 0) {
        const tr = document.createElement('tr');
        tr.className = 'border-b border-outline-variant/10 text-outline/60 text-center';
        const emptyMsg = currentTabMappings.length === 0
            ? '暂无模型映射，点击上方「添加映射模型」或「获取号池模型」'
            : `未找到匹配「<span class="text-primary font-bold">${escapeHtmlLocal(modelMappingSearchQuery)}</span>」的模型映射`;
        tr.innerHTML = `<td colspan="${isNvidiaTab ? 6 : 5}" class="py-8 text-[12px]">${emptyMsg}</td>`;
        tbody.appendChild(tr);
        return;
    }

    filteredMappings.forEach((item, index) => {
        const tr = document.createElement('tr');
        tr.className = 'border-b border-outline-variant/15 hover:bg-slate-50 dark:hover:bg-white/5';
        // Other Tab:按行所属组解析下拉模型(other/{groupId}/{model} 三段前缀取组缓存);
        // 非 Other Tab:沿用全局号池缓存。
        let rowModels = fetchedModels;
        if (isOtherTab) {
            const cm = (item.clientModel || '').trim();
            const m = cm.match(/^other\/([^/]+)\//);
            const gid = m ? m[1] : '';
            rowModels = gid ? (channelModelsCache[`other/${gid}`] || []) : [];
        }
        if (rowModels.length > 0) {
            updateDatalist(rowModels);
        }
        // diff 徽章:失效(红)标在 TargetModel 列——按本行所属 group/号池的远端全集判 TargetModel 是否下架;
        // 新增(绿)标在 TargetModel 列——TargetModel 命中本轮「新增」集合(本次远端 − 上次快照)。
        // Other Tab 按行 group 重解 stale(口径与 rowModels 一致),「新增」沿用本 channel 集合(够用:同 group 同源拉取)。
        let rowStaleSet = staleSet;
        let rowAddedSet = addedSet;
        if (isOtherTab) {
            const cm = (item.clientModel || '').trim();
            const m = cm.match(/^other\/([^/]+)\//);
            const gid = m ? m[1] : '';
            const grpKey = gid ? `other/${gid}` : '';
            rowStaleSet = grpKey ? (channelStaleLower[grpKey] || new Set<string>()) : new Set<string>();
            rowAddedSet = grpKey ? (channelAddedLower[grpKey] || new Set<string>()) : new Set<string>();
        }
        // 失效判定经 shouldMarkStale(内部对 liveSet 取反):rowStaleSet 实为「远端全集小写集合」,
        // TargetModel 不在远端全集才算下架。此前直接 .has(tmLower) 漏了取反 → 远端明明有却误标删除线。
        // 新增判定经 shouldMarkNew:rowAddedSet 为「本轮新增」集合,在集合内即新增,极性正确无需取反。
        const isStale = shouldMarkStale(item, rowStaleSet);
        const isNew = shouldMarkNew(item.targetModel, rowAddedSet);
        const staleBadge = isStale ? mappingBadgeHTML('stale') : '';
        const newBadge = isNew ? mappingBadgeHTML('new') : '';
        const targetExtraClass = isStale ? 'text-red-500 dark:text-red-400 line-through opacity-70' : '';
        tr.innerHTML = `
            <td class="py-2 px-1">
                <input type="text" class="w-full px-2 py-1 text-[12px] rounded border border-outline-variant/30 bg-transparent text-on-surface dark:text-white client-model-input" value="${item.clientModel || ''}" data-index="${index}" placeholder="例如: gpt-4o" />
            </td>
            <td class="py-2 px-1">
                <div class="flex items-center gap-1.5">
                    <input type="text" class="w-full px-2 py-1 text-[12px] rounded border border-outline-variant/30 bg-transparent ${targetExtraClass} target-model-input" value="${item.targetModel || ''}" data-index="${index}" list="channelModelsDatalist" placeholder="例如: gemini-1.5-pro" />
                    <select class="px-2 py-1 text-[11px] font-mono rounded border border-outline-variant/30 bg-slate-100 dark:bg-white/10 text-on-surface dark:text-white target-model-quick-select cursor-pointer w-32 flex-shrink-0" data-index="${index}">
                        <option value="">${rowModels.length > 0 ? '选择模型...' : '未拉取模型'}</option>
                        ${rowModels.map(m => `<option value="${m}" ${m === item.targetModel ? 'selected' : ''}>${m}</option>`).join('')}
                    </select>
                    ${newBadge}${staleBadge}
                </div>
            </td>
            <td class="py-2 text-center inject-kwargs-cell ${isNvidiaTab ? '' : 'hidden'}">
                <input type="checkbox" class="text-primary focus:ring-primary rounded inject-kwargs-checkbox" ${item.injectChatTemplateKwargs !== false ? 'checked' : ''} data-index="${index}" title="是否向 NVIDIA 等上游注入 chat_template_kwargs (默认勾选)" />
            </td>
            <td class="py-2 text-center px-1">
                <select class="px-2 py-1 text-[11px] rounded border border-outline-variant/30 bg-slate-50 dark:bg-[#1a1f30] text-on-surface dark:text-white multimodal-select cursor-pointer w-28 text-center focus:outline-none focus:border-primary" data-index="${index}" title="多模态模式：自动判定(启发式) / 强制多模态(直送) / 强制非多模态(OCR)">
                    <option value="auto" ${item.multimodal === undefined || item.multimodal === null ? 'selected' : ''}>自动判定</option>
                    <option value="true" ${item.multimodal === true ? 'selected' : ''}>强制多模态</option>
                    <option value="false" ${item.multimodal === false ? 'selected' : ''}>强制非多模态</option>
                </select>
            </td>
            <td class="py-2 text-center">
                <input type="checkbox" class="text-primary focus:ring-primary rounded expose-checkbox" ${item.expose ? 'checked' : ''} data-index="${index}" />
            </td>
            <td class="py-2 text-center">
                <button class="text-red-500 hover:text-red-700 transition-colors flex items-center justify-center mx-auto btn-delete-mapping cursor-pointer" data-index="${index}">
                    <span class="material-symbols-outlined text-[18px]">delete</span>
                </button>
            </td>
        `;
        tbody.appendChild(tr);
    });

    // 绑定删除按钮
    tbody.querySelectorAll('.btn-delete-mapping').forEach(btn => {
        btn.addEventListener('click', (e) => {
            const idx = parseInt((e.currentTarget as HTMLElement).getAttribute('data-index') || '0');
            const targetItem = filteredMappings[idx];
            if (!targetItem) return;
            const mainIdx = allMappings.indexOf(targetItem);
            if (mainIdx !== -1) {
                allMappings.splice(mainIdx, 1);
            }
            renderCurrentTabTable();
        });
    });

    // 绑定输入改变
    tbody.querySelectorAll('.client-model-input').forEach(input => {
        input.addEventListener('input', (e) => {
            const target = e.target as HTMLInputElement;
            const idx = parseInt(target.getAttribute('data-index') || '0');
            if (filteredMappings[idx]) {
                filteredMappings[idx].clientModel = target.value.trim();
            }
        });
    });

    tbody.querySelectorAll('.target-model-input').forEach(input => {
        const handleTargetModelChange = async (e: Event) => {
            const target = e.target as HTMLInputElement;
            const idx = parseInt(target.getAttribute('data-index') || '0');
            if (!filteredMappings[idx]) return;
            const val = target.value.trim();
            filteredMappings[idx].targetModel = val;

            // 若 Client Model 为空，自动联动填入 Client Model。
            // 非 Google 族号池(NVIDIA/DeepSeek/自定义)需带 "{provider}/" 前缀以经 /route/* 精准路由,
            // 故填 `nvidia/deepseek-ai/deepseek-v4-pro` 而非裸名;Google 族裸名直连,不加前缀。
            const clientInput = tbody.querySelector(`.client-model-input[data-index="${idx}"]`) as HTMLInputElement | null;
            if (clientInput && (!clientInput.value || !clientInput.value.trim())) {
                const currentTab = poolTabs.find(t => t.id === activeTabId) || poolTabs[0];
                const provider = currentTab?.targetProvider || '';
                if (provider === 'other') {
                    // Other 号池:三段前缀 other/{groupId}/{model}。groupId 来自映射行的 targetGroupId,
                    // 缺失则弹窗选组并写回;选组失败(无组/取消)保留裸名,等用户先去账号池建组。
                    const gid = await ensureOtherEntryGroupId(filteredMappings[idx]);
                    const autoClient = gid ? `other/${gid}/${val}` : val;
                    filteredMappings[idx].clientModel = autoClient;
                    clientInput.value = autoClient;
                } else {
                    const autoClient = !isGoogleProviderKind(provider) ? `${provider}/${val}` : val;
                    filteredMappings[idx].clientModel = autoClient;
                    clientInput.value = autoClient;
                }
            }
        };

        input.addEventListener('input', handleTargetModelChange);
        input.addEventListener('change', handleTargetModelChange);
    });

    tbody.querySelectorAll('.target-model-quick-select').forEach(sel => {
        sel.addEventListener('change', async (e) => {
            const target = e.target as HTMLSelectElement;
            const idx = parseInt(target.getAttribute('data-index') || '0');
            if (!filteredMappings[idx]) return;
            const val = target.value;
            if (val) {
                // 1. 填入 Target Model 文本框与底层数据
                filteredMappings[idx].targetModel = val;
                const targetInput = tbody.querySelector(`.target-model-input[data-index="${idx}"]`) as HTMLInputElement | null;
                if (targetInput) targetInput.value = val;

                // 2. 若 Client Model 为空，自动联动填入 Client Model(非 Google 族带 provider/ 前缀)。
                const clientInput = tbody.querySelector(`.client-model-input[data-index="${idx}"]`) as HTMLInputElement | null;
                if (clientInput && (!clientInput.value || !clientInput.value.trim())) {
                    const currentTab = poolTabs.find(t => t.id === activeTabId) || poolTabs[0];
                    const provider = currentTab?.targetProvider || '';
                    if (provider === 'other') {
                        // Other 号池:三段前缀 other/{groupId}/{model},groupId 同上从行内或弹窗取。
                        const gid = await ensureOtherEntryGroupId(filteredMappings[idx]);
                        const autoClient = gid ? `other/${gid}/${val}` : val;
                        filteredMappings[idx].clientModel = autoClient;
                        clientInput.value = autoClient;
                    } else {
                        const autoClient = !isGoogleProviderKind(provider) ? `${provider}/${val}` : val;
                        filteredMappings[idx].clientModel = autoClient;
                        clientInput.value = autoClient;
                    }
                }
            }
        });
    });

    tbody.querySelectorAll('.multimodal-select').forEach(sel => {
        sel.addEventListener('change', (e) => {
            const target = e.target as HTMLSelectElement;
            const idx = parseInt(target.getAttribute('data-index') || '0');
            if (!filteredMappings[idx]) return;
            const val = target.value;
            if (val === 'true') {
                filteredMappings[idx].multimodal = true;
            } else if (val === 'false') {
                filteredMappings[idx].multimodal = false;
            } else {
                filteredMappings[idx].multimodal = undefined; // 默认自动判定模式
            }
        });
    });



    tbody.onclick = (e) => {
        const target = (e.target as HTMLElement).closest('input[type="checkbox"]') as HTMLInputElement | null;
        if (!target) {
            return;
        }
        const idx = parseInt(target.getAttribute('data-index') || '-1');
        if (idx < 0 || idx >= filteredMappings.length) return;

        if (target.classList.contains('expose-checkbox')) {
            filteredMappings[idx].expose = target.checked;
        } else if (target.classList.contains('inject-kwargs-checkbox')) {
            filteredMappings[idx].injectChatTemplateKwargs = target.checked;
        }
    };
}

// syncClearStaleBtn 按「本 Tab 真实待删映射数」同步「清除失效模型」按钮可点性 + 文案计数。
// staleCount 为 collectStaleMappingsInTab 现算的真实失效条数;hasFetched 区分 staleCount==0 时
// 「已拉取无失效」与「未拉取」两类 tooltip。杜绝旧逻辑传 staleSet.size(=远端全集数,与新增同源)。
function syncClearStaleBtn(staleCount: number, hasFetched: boolean): void {
    const btn = document.getElementById('btnClearStaleModels') as HTMLButtonElement | null;
    if (!btn) return;
    const dict = i18n[state.currentLanguage] || i18n.zh || {};
    btn.disabled = staleCount <= 0;
    if (staleCount > 0) {
        const tpl = (dict.relayClearStaleModels || '清除失效模型');
        btn.textContent = `${tpl} (${staleCount})`;
        btn.title = '';
    } else {
        btn.textContent = (dict.relayClearStaleModels || '清除失效模型');
        btn.title = hasFetched
            ? (dict.relayClearStaleEmpty || '当前号池无失效模型映射')
            : (dict.relayClearStaleNeedFetch || '请先成功「获取号池模型」以核对远端全集');
    }
}

// collectStaleMappingsInTab 收集「指定 Tab 下、真实目标模型已从远端下架」的映射条目引用。
// 与 _relayClearStaleMappings 删除动作 + renderCurrentTabTable 按钮计数共享同一失效筛选真源,
// 保证「按钮显示的待删数」与「确认后实际删除数」逐字节一致。三重安全:
//   1) Tab 隔离:经 getMappingTab 只处理归属该 Tab 的映射,杜绝跨号池误判(历史缺陷根因);
//   2) 缓存键隔离:Other 按行 group 解析 other/{groupId},非 Other 用 provider 小写;
//   3) 未拉取跳过:对应缓存缺失/为空时不判(不误删)。仅当远端确认下架(不在本次全集)才入列。
// 由此迁出为独立函数(原逻辑内联在 _relayClearStaleMappings 闭包内),供两处复用、消除口径漂移。
function collectStaleMappingsInTab(tabId: string): any[] {
    const currentTab = poolTabs.find(t => t.id === tabId) || poolTabs[0];
    if (!currentTab) return [];
    const provider = (currentTab.targetProvider || currentTab.id || '').trim();
    const isOtherTab = (currentTab.targetProvider === 'other' || currentTab.id === 'other');
    const staleToRemove: any[] = [];
    for (const m of allMappings) {
        if (getMappingTab(m) !== tabId) continue; // Tab 隔离:仅处理归属该 Tab 的映射
        const cm = (m.clientModel || '').trim();
        const tm = (m.targetModel || '').trim();
        if (!cm || !tm) continue;
        let liveKey = provider.toLowerCase();
        if (isOtherTab) {
            // Other:按行 group 解析键 other/{groupId}(与 fetchOtherGroupModels 写入口径一致)。
            const mtch = cm.match(/^other\/([^/]+)\//);
            const gid = mtch ? mtch[1] : '';
            if (mtch) liveKey = `other/${gid}`; else continue; // 非三段前缀的 Other 行不判(无对应上游缓存)
        }
        const live = channelModelsCache[liveKey];
        if (!live || live.length === 0) continue; // 该组未成功拉取过,跳过(不误删)
        const liveSet = buildLiveSetLower(live);
        if (shouldMarkStale(m, liveSet)) staleToRemove.push(m);
    }
    return staleToRemove;
}

// hasFetchedStaleBasis 判断指定 Tab 是否已成功拉取过远端模型(作为失效判定的基准是否就绪)。
// Other Tab 走 group 键(任一 other/{groupId} 已拉取即视为该 Tab 有基准);非 Other 走 provider 小写键。
// true=「已拉取,staleCount==0 是真无失效」;false=「未拉取,需先点获取号池模型」。
function hasFetchedStaleBasis(tabId: string): boolean {
    const currentTab = poolTabs.find(t => t.id === tabId) || poolTabs[0];
    if (!currentTab) return false;
    const isOtherTab = (currentTab.targetProvider === 'other' || currentTab.id === 'other');
    if (isOtherTab) {
        return Object.keys(channelStaleLower).some(k => k.startsWith('other/'));
    }
    const chKey = (currentTab.targetProvider || currentTab.id || '').toLowerCase();
    return !!channelStaleLower[chKey];
}

export function initRelayModelMapping() {
    // 重置 6 个模块级 let 到初始值(模拟原闭包每次 initRelayEvents 重建语义),避免跨 mount 状态残留。
    allMappings = [];
    poolTabs = [];
    activeTabId = 'google';
    availableChannels = ['antigravity', 'google', 'gcp', 'nvidia', 'other', 'grok', 'workbuddy', 'opencode'];
    channelModelsCache = {};
    channelModelsCachePrev = {};
    modelMappingSearchQuery = '';
    channelAddedLower = {};
    channelStaleLower = {};

    // 绑定搜索输入框与清除按钮事件
    const searchInput = document.getElementById('inputRelayModelMappingSearch') as HTMLInputElement | null;
    const btnClearSearch = document.getElementById('btnClearRelayModelMappingSearch') as HTMLButtonElement | null;
    if (searchInput) {
        searchInput.value = '';
        searchInput.oninput = () => {
            modelMappingSearchQuery = searchInput.value.trim().toLowerCase();
            if (btnClearSearch) {
                btnClearSearch.classList.toggle('hidden', !modelMappingSearchQuery);
            }
            renderCurrentTabTable();
        };
    }
    if (btnClearSearch) {
        btnClearSearch.onclick = () => {
            modelMappingSearchQuery = '';
            if (searchInput) {
                searchInput.value = '';
                searchInput.focus();
            }
            btnClearSearch.classList.add('hidden');
            renderCurrentTabTable();
        };
    }

    (window as any)._relayFetchChannelModels = async () => {
        const btnFetch = document.getElementById('btnFetchChannelModels');
        const lblCount = document.getElementById('lblFetchedModelsCount');
        const currentTab = poolTabs.find(t => t.id === activeTabId) || poolTabs[0];
        const channel = currentTab.targetProvider || currentTab.id;

        if (btnFetch) {
            btnFetch.innerHTML = `<span class="material-symbols-outlined text-[15px] animate-spin">sync</span><span>获取中...</span>`;
            (btnFetch as HTMLButtonElement).disabled = true;
        }

        try {
            const res = await ipcRenderer.invoke('relay:fetch-channel-models', channel);
            if (res && res.success && Array.isArray(res.models)) {
                channelModelsCache[channel] = res.models;
                updateDatalist(res.models);
                // diff:后端新增 added/snapshot 字段(向后兼容缺失即空)。按 channel 落「新增/失效」小写集合,
                // 供 renderCurrentTabTable 行徽章与「清除失效模型」按钮读取。失效用本次全集现场对比本 Tab 映射。
                const snapshot: string[] = Array.isArray(res.snapshot) ? res.snapshot : [];
                const addedRaw: string[] = Array.isArray(res.added) ? res.added : [];
                const chKey = (channel || '').toLowerCase();
                channelAddedLower[chKey] = computeRemoteAddedLower(res.models, snapshot);
                // 「本轮新增」也按后端 added 校准(后端用快照算更准,前端缓存 snapshot 可能因切 Tab 不全)。
                if (addedRaw.length > 0) {
                    channelAddedLower[chKey] = new Set(addedRaw.map((s: string) => (s || '').trim().toLowerCase()).filter(Boolean));
                }
                channelStaleLower[chKey] = buildLiveSetLower(res.models);
                // 失效集合应是「不在远端全集的 TargetModel」。buildLiveSetLower 返回的是「远端全集小写」,
                // 行渲染时用 shouldMarkStale(tm, liveSet) 判不在即失效。此处直接存 liveSet 即可。
                if (lblCount) {
                    const newCount = channelAddedLower[chKey].size;
                    lblCount.textContent = `✅ 已获取 ${res.models.length} 个模型${newCount > 0 ? ` · 新增 ${newCount}` : ''}`;
                    lblCount.classList.remove('hidden');
                }
                // 拉取成功后,自动为当前 Tab 批量补全映射条目,免去逐条手动添加 + 保存再补前缀的两步操作。
                // 补全语义:
                //   - 非 Google 族号池(NVIDIA/DeepSeek/自定义):每个新模型生成「单条带前缀」
                //     ClientModel = `{provider}/{model}`,TargetModel = `{model}`,TargetProvider = {provider}。
                //     客户端用带前缀名请求 /route/* 精准路由。
                //   - Google 族号池(google/gcp/antigravity/空):每个新模型生成「双条目」
                //     ① 裸名条目 ClientModel = TargetModel = `{model}`(走 /v1/chat/completions 等裸名直连);
                //     ② 带前缀条目 ClientModel = `{provider}/{model}`,TargetModel = `{model}`(走 /route 精准路由)。
                //     双条目并存:既保持裸名直连链路零回归,又让 /route/v1/models 能列出带前缀名精准路由。
                // 「新模型」判定:已有映射里 ClientModel 等于裸名 {model} 或带前缀 {provider}/{model} 的都算已存在,跳过。
                const provider = (currentTab.targetProvider || currentTab.id || '').trim();
                const tabId = currentTab.id;
                const existingClientSet = new Set<string>();
                for (const m of allMappings) {
                    const cm = (m.clientModel || '').trim();
                    if (cm) existingClientSet.add(cm.toLowerCase());
                }
                // 按当前 Tab 作用域去重:仅收集本 Tab 已存在的上游模型名,
                // 避免全局 targetModel 去重误伤跨号池同名模型(不同号池上游可提供同名模型)。
                const existingTargetSet = new Set<string>();
                for (const m of allMappings) {
                    if (getMappingTab(m) === tabId) {
                        const tm = (m.targetModel || '').trim();
                        if (tm) existingTargetSet.add(tm.toLowerCase());
                    }
                }
                const newEntries: any[] = [];
                for (const modelRaw of res.models) {
                    const model = (modelRaw || '').trim();
                    if (!model) continue;
                    if (existingTargetSet.has(model.toLowerCase())) continue; // 同 Tab 已有该上游模型,跳过
                    const isGoogle = isGoogleProviderKind(provider);
                    if (isGoogle) {
                        // ① 裸名条目(供 /v1/* 裸名直连)。
                        if (!existingClientSet.has(model.toLowerCase())) {
                            newEntries.push(makeMappingEntry(model, model, provider, true));
                            existingClientSet.add(model.toLowerCase());
                        }
                        // ② 带前缀条目(供 /route 精准路由)。
                        const prefixed = `${provider}/${model}`;
                        if (!existingClientSet.has(prefixed.toLowerCase())) {
                            newEntries.push(makeMappingEntry(prefixed, model, provider, true));
                            existingClientSet.add(prefixed.toLowerCase());
                        }
                    } else {
                        // 非 Google 族:仅带前缀单条。
                        const prefixed = `${provider}/${model}`;
                        if (!existingClientSet.has(prefixed.toLowerCase())) {
                            newEntries.push(makeMappingEntry(prefixed, model, provider, true));
                            existingClientSet.add(prefixed.toLowerCase());
                        }
                    }
                    existingTargetSet.add(model.toLowerCase());
                }
                if (newEntries.length > 0) {
                    // 对该 Tab 新增的映射,归类 ownedBy 用 TabId(与 getMappingTab/save 逻辑一致)。
                    const tabId = currentTab.id;
                    for (const ne of newEntries) {
                        ne.ownedBy = tabId;
                        allMappings.push(ne);
                    }
                }
                renderCurrentTabTable();
            } else {
                if (lblCount) {
                    lblCount.textContent = `❌ 获取失败: ${res?.error || '网络超时'}`;
                    lblCount.classList.remove('hidden');
                }
            }
        } catch (e: any) {
            console.error('[RelayController] Fetch models error:', e);
            if (lblCount) {
                lblCount.textContent = `❌ 获取出错`;
                lblCount.classList.remove('hidden');
            }
        } finally {
            if (btnFetch) {
                btnFetch.innerHTML = `<span class="material-symbols-outlined text-[15px]">sync</span><span>获取号池模型</span>`;
                (btnFetch as HTMLButtonElement).disabled = false;
            }
        }
    };

    (window as any)._relayAddTab = () => {
        const tabName = prompt('请输入新号池 Tab 名称（例如：DeepSeek 账号池）：');
        if (!tabName || !tabName.trim()) return;

        const providerId = prompt('请输入该 Tab 绑定的号池 Provider 标识（例如：deepseek）：') || tabName.trim().toLowerCase();
        const tabId = 'custom_' + Date.now();

        const newTab: PoolTabInfo = {
            id: tabId,
            name: tabName.trim(),
            targetProvider: providerId.trim().toLowerCase(),
            isCustom: true
        };

        poolTabs.push(newTab);
        activeTabId = tabId;

        renderPoolTabs();
        renderTargetProviderSelector();
        renderCurrentTabTable();
    };

    (window as any)._relayDeleteCurrentTab = () => {
        const currentTab = poolTabs.find(t => t.id === activeTabId);
        if (!currentTab || !currentTab.isCustom) return;

        if (!confirm(`确定要删除号池 Tab「${currentTab.name}」及其下的所有模型映射吗？`)) return;

        // 删除属于该 Tab 的所有映射
        allMappings = allMappings.filter(m => getMappingTab(m) !== activeTabId);
        poolTabs = poolTabs.filter(t => t.id !== activeTabId);

        activeTabId = poolTabs[0]?.id || 'google';

        renderPoolTabs();
        renderTargetProviderSelector();
        renderCurrentTabTable();
    };

    (window as any)._relayAddModelMapping = () => {
        const currentTab = poolTabs.find(t => t.id === activeTabId);
        const targetProv = currentTab ? currentTab.targetProvider : '';

        // 如果当前有搜索关键词，重置搜索以确保新添加的行处于可见状态
        if (modelMappingSearchQuery) {
            modelMappingSearchQuery = '';
            const searchInput = document.getElementById('inputRelayModelMappingSearch') as HTMLInputElement | null;
            const btnClearSearch = document.getElementById('btnClearRelayModelMappingSearch') as HTMLButtonElement | null;
            if (searchInput) searchInput.value = '';
            if (btnClearSearch) btnClearSearch.classList.add('hidden');
        }

        allMappings.unshift({
            clientModel: '',
            targetModel: '',
            expose: true,
            // chat_template_kwargs 是 NVIDIA NIM 专属约定, Other 号池各第三方上游思考参数格式
            // 各异(阿里云 DeepSeek v4 要 bool、NIM 要字符串、有的根本不认), 强塞会触发上游 400。
            // 故 Other 号池新增映射默认关闭注入(后端 isNvidiaModelNoKwargs 也会兜底抑制, 此处保持 UI 一致)。
            injectChatTemplateKwargs: targetProv !== 'other',
            ownedBy: activeTabId,
            targetProvider: targetProv,
            variantEfforts: []
        });

        renderCurrentTabTable();
    };

    (window as any)._relaySaveModelMapping = async () => {
        const btnSaveModelMapping = document.getElementById('btnSaveModelMapping');
        if (!btnSaveModelMapping) return;
        const originalText = btnSaveModelMapping.innerHTML;
        const dict = i18n[state.currentLanguage] || {};
        btnSaveModelMapping.innerHTML = `<span class="material-symbols-outlined text-[16px] animate-spin">sync</span><span>${dict.relaySaving || '保存中...'}</span>`;

        // 整理补齐所有 mappings 的 ownedBy 与 targetProvider
        allMappings.forEach(m => {
            const tabId = getMappingTab(m);
            const tabObj = poolTabs.find(t => t.id === tabId);
            if (!m.ownedBy) m.ownedBy = tabId;
            if (tabObj && tabObj.targetProvider) {
                m.targetProvider = tabObj.targetProvider;
            }
        });

        // 过滤空映射
        const filteredMappings = allMappings.filter(m => m.clientModel && m.clientModel.trim() !== '' && m.targetModel && m.targetModel.trim() !== '');

        // 非 Google 族号池的 ClientModel 自动补 "{provider}/" 前缀(仅经 /route/* 精准路由时生效)。
        // 触发条件:provider 非 Google 族(走 /route) && 当前未带该前缀 && ClientModel==TargetModel
        //   (纯路由用途;若用户已显式改名 ClientModel!=TargetModel 表示自定义别名,尊重不动)。
        // 写回 allMappings 内存态并 re-render,让表格即时呈现带前缀的 ClientModel(与落盘一致)。
        // 幂等性:补前缀后 cm!=tm,下次保存 cm===tm 判定不成立,不会叠加 `nvidia/nvidia/xxx`。
        // 注意:Google 族号池由 _relayFetchChannelModels 拉取时双条目生成(裸名 + 带前缀)并存,
        //   此处不再为 Google 族补前缀,避免破坏裸名直连条目。
        // Other 号池单独走三段前缀 other/{groupId}/{model},不走通用二段逻辑,避免误拼 other/{model}。
        const mappingsToSave: any[] = [];
        for (const m of filteredMappings) {
            const provider = (m.targetProvider || '').trim();
            const cm = (m.clientModel || '').trim();
            const tm = (m.targetModel || '').trim();
            if (provider === 'other') {
                // Other:只有 cm===tm(裸名)且已携带 groupId 时才补三段前缀;
                // 无 groupId 时保留裸名(用户尚未选组,后端不会路由,留待用户回去选组),绝不拼成 other/{model} 两段。
                if (cm === tm) {
                    const gid = (m.targetGroupId || '').trim();
                    if (gid) {
                        m.clientModel = `other/${gid}/${cm}`;
                    }
                }
                mappingsToSave.push(m);
            } else if (!isGoogleProviderKind(provider) && cm === tm && !cm.toLowerCase().startsWith(`${provider.toLowerCase()}/`)) {
                const prefixed = `${provider}/${cm}`;
                m.clientModel = prefixed;
                mappingsToSave.push(m);
            } else {
                mappingsToSave.push(m);
            }
        }
        renderCurrentTabTable();

        try {
            const res = await ipcRenderer.invoke('relay:set-model-mapping', mappingsToSave);
            const isSuccess = res && res.success;
            const successMsg = (res && res.isRemote)
                ? (dict.relaySaveRemoteSuccess || '已同步至远程服务器')
                : (dict.relaySaveSuccess || '保存成功');
            btnSaveModelMapping.innerHTML = isSuccess
                ? `<span class="material-symbols-outlined text-[16px]">done</span><span>${successMsg}</span>`
                : `<span class="material-symbols-outlined text-[16px]">error</span><span>${dict.relaySaveFailed || '保存失败'}</span>`;
            setTimeout(() => { btnSaveModelMapping.innerHTML = originalText; }, 2000);
        } catch (err) {
            console.error('[RelayController] Failed to save model mappings:', err);
            btnSaveModelMapping.innerHTML = `<span class="material-symbols-outlined text-[16px]">error</span><span>${dict.relaySaveFailed || '保存失败'}</span>`;
            setTimeout(() => { btnSaveModelMapping.innerHTML = originalText; }, 2000);
        }
    };

    (window as any)._relayClearStaleMappings = () => {
        const currentTab = poolTabs.find(t => t.id === activeTabId) || poolTabs[0];
        if (!currentTab) return;
        const dict = i18n[state.currentLanguage] || i18n.zh || {};

        // 复用 collectStaleMappingsInTab:与 renderCurrentTabTable 按钮计数共享同一失效筛选真源,
        // 保证「按钮显示的待删数」与「确认后实际删除数」逐字节一致,杜绝两处口径漂移。
        // 该函数内部已按 getMappingTab 隔离当前 Tab + 按组解析缓存键 + 未拉取跳过(不误删)。
        const staleToRemove = collectStaleMappingsInTab(activeTabId);
        if (staleToRemove.length === 0) {
            // 无失效(或未拉取)→ 友好提示而非静默。区分「已拉取但无失效」与「未拉取」两种语义。
            const msg = hasFetchedStaleBasis(activeTabId)
                ? (dict.relayClearStaleEmpty || '当前号池无失效模型映射')
                : (dict.relayClearStaleNeedFetch || '请先成功「获取号池模型」以核对远端全集');
            window.alert(msg);
            return;
        }
        const promptText = buildStaleConfirmPrompt(staleToRemove as MappingLike[]);
        if (!window.confirm(promptText)) return;
        // 删除:用引用比对从 allMappings 移除。删除后失效标记随之清空(这些 TargetModel 已不在列表)。
        const removeSet = new Set(staleToRemove);
        allMappings = allMappings.filter(m => !removeSet.has(m));
        renderCurrentTabTable();
    };
}
