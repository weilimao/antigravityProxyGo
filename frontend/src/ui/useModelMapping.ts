import { ref, computed, watch, getCurrentScope, onScopeDispose, type Ref } from 'vue';
import { ipcRenderer } from '../shared/ipc';
import state from './dashboardState';
import i18n from '../shared/i18n';
import {
  buildLiveSetLower, buildStaleConfirmPrompt, computeRemoteAddedLower,
  shouldMarkNew, shouldMarkStale,
} from './relayModelDiff';
import type { ModelMappingEntry, PoolTabInfo, OtherGroupInfo } from '../components/settings/relay-mapping/types';

// 无缓存渠道时共享的空数组引用:避免模板里 getRowModels 每次渲染都返回新 [],导致所有行组件 props 变化全量重渲染。
const EMPTY_MODELS: string[] = [];

// 行稳定 key 生成器:替代 v-for 的 :key="index",过滤/删除/翻页时能按 key 复用行组件而非按位置补丁。
let rowKeySeq = 0;
function nextRowKey(): string {
  rowKeySeq += 1;
  return `mmr_${Date.now().toString(36)}_${rowKeySeq.toString(36)}`;
}

function isGoogleProviderKind(p: string): boolean {
  const c = (p || '').trim().toLowerCase();
  return c === 'google' || c === 'gcp' || c === 'antigravity' || c === 'gemini-cli' || c === '';
}

function makeMappingEntry(clientModel: string, targetModel: string, provider: string, expose: boolean): ModelMappingEntry {
  return {
    clientModel,
    targetModel,
    targetProvider: provider,
    expose,
    ownedBy: '',
    injectChatTemplateKwargs: provider !== 'other',
    variantEfforts: [],
  };
}

function getMappingTab(m: ModelMappingEntry): string {
  if (m.ownedBy) return m.ownedBy;
  const modelName = (m.clientModel || m.targetModel || '').toLowerCase();
  if (modelName.startsWith('other/')) return 'other';
  if (modelName.startsWith('nvidia/') || modelName.endsWith('-nemotron')) return 'nvidia';
  if (modelName.startsWith('grok/')) return 'grok';
  if (modelName.startsWith('workbuddy/')) return 'workbuddy';
  if (modelName.startsWith('deepseek')) return 'deepseek';
  if (modelName.startsWith('qwen')) return 'qwen';
  if (modelName.startsWith('claude')) return 'anthropic';
  return 'google';
}

export function useModelMapping() {
  const allMappings: Ref<ModelMappingEntry[]> = ref([]);
  const poolTabs: Ref<PoolTabInfo[]> = ref([]);
  const activeTabId = ref('google');
  const availableChannels = ref<string[]>(['antigravity', 'google', 'gcp', 'nvidia', 'other', 'grok', 'workbuddy']);
  const channelModelsCache = ref<Record<string, string[]>>({});
  const channelModelsCachePrev = ref<Record<string, string[]>>({});
  const searchQuery = ref('');
  const channelAddedLower = ref<Record<string, Set<string>>>({});
  const channelStaleLower = ref<Record<string, Set<string>>>({});
  const saving = ref(false);
  const saveStatus = ref<'idle' | 'success' | 'error'>('idle');
  const fetching = ref(false);
  const fetchStatusMsg = ref('');
  const otherGroups = ref<OtherGroupInfo[]>([]);

  const currentTab = computed(() =>
    poolTabs.value.find(t => t.id === activeTabId.value) || poolTabs.value[0]
  );

  const isNvidiaTab = computed(() =>
    currentTab.value && (currentTab.value.targetProvider === 'nvidia' || currentTab.value.id === 'nvidia')
  );

  const isOtherTab = computed(() =>
    currentTab.value && (currentTab.value.targetProvider === 'other' || currentTab.value.id === 'other')
  );

  const currentTabMappings = computed(() =>
    allMappings.value.filter(m => (m.clientModel || '').trim().toLowerCase() !== 'auto' && getMappingTab(m) === activeTabId.value)
  );

  const filteredMappings = computed(() => {
    const q = searchQuery.value.trim().toLowerCase();
    if (!q) return currentTabMappings.value;
    return currentTabMappings.value.filter(item => {
      const cm = (item.clientModel || '').toLowerCase();
      const tm = (item.targetModel || '').toLowerCase();
      const tp = (item.targetProvider || '').toLowerCase();
      const gid = (item.targetGroupId || '').toLowerCase();
      const effs = Array.isArray(item.variantEfforts) ? item.variantEfforts.join(' ').toLowerCase() : '';
      return cm.includes(q) || tm.includes(q) || tp.includes(q) || gid.includes(q) || effs.includes(q);
    });
  });

  // ===== 分页:映射行数可达数百条,全部平铺渲染(每行含组合框)会导致整页卡顿,只渲染当前页 =====
  const pageSize = 15;
  const currentPage = ref(1);
  const totalPages = computed(() => Math.max(1, Math.ceil(filteredMappings.value.length / pageSize)));
  const pagedMappings = computed(() => {
    const total = filteredMappings.value.length;
    if (total === 0) return [];
    const page = Math.min(currentPage.value, totalPages.value);
    const start = (page - 1) * pageSize;
    return filteredMappings.value.slice(start, start + pageSize);
  });

  function gotoPage(p: number) {
    currentPage.value = Math.min(Math.max(1, p), totalPages.value);
  }

  // 搜索词变化回到第 1 页;删除/清除失效导致总页数缩小时自动夹回边界。
  watch(searchQuery, () => { currentPage.value = 1; });
  watch(totalPages, (pages) => {
    if (currentPage.value > pages) currentPage.value = pages;
  });

  function getRowModels(item: ModelMappingEntry): string[] {
    if (!isOtherTab.value) {
      const ch = currentTab.value?.targetProvider || currentTab.value?.id || '';
      return channelModelsCache.value[ch] || EMPTY_MODELS;
    }
    const cm = (item.clientModel || '').trim();
    const m = cm.match(/^other\/([^/]+)\//);
    const gid = m ? m[1] : '';
    return gid ? (channelModelsCache.value[`other/${gid}`] || EMPTY_MODELS) : EMPTY_MODELS;
  }

  function isStaleItem(item: ModelMappingEntry): boolean {
    if (isOtherTab.value) {
      const cm = (item.clientModel || '').trim();
      const m = cm.match(/^other\/([^/]+)\//);
      const gid = m ? m[1] : '';
      const grpKey = gid ? `other/${gid}` : '';
      const liveSet = grpKey ? (channelStaleLower.value[grpKey] || new Set<string>()) : new Set<string>();
      return shouldMarkStale(item, liveSet);
    }
    const chKey = (currentTab.value?.targetProvider || currentTab.value?.id || '').toLowerCase();
    const liveSet = channelStaleLower.value[chKey] || new Set<string>();
    return shouldMarkStale(item, liveSet);
  }

  function isNewItem(item: ModelMappingEntry): boolean {
    if (isOtherTab.value) {
      const cm = (item.clientModel || '').trim();
      const m = cm.match(/^other\/([^/]+)\//);
      const gid = m ? m[1] : '';
      const grpKey = gid ? `other/${gid}` : '';
      const addedSet = grpKey ? (channelAddedLower.value[grpKey] || new Set<string>()) : new Set<string>();
      return shouldMarkNew(item.targetModel, addedSet);
    }
    const chKey = (currentTab.value?.targetProvider || currentTab.value?.id || '').toLowerCase();
    const addedSet = channelAddedLower.value[chKey] || new Set<string>();
    return shouldMarkNew(item.targetModel, addedSet);
  }

  const staleCount = computed(() => collectStaleMappingsInTab(activeTabId.value).length);

  function hasFetchedStaleBasis(tabId: string): boolean {
    const tab = poolTabs.value.find(t => t.id === tabId) || poolTabs.value[0];
    if (!tab) return false;
    const isOther = tab.targetProvider === 'other' || tab.id === 'other';
    if (isOther) {
      return Object.keys(channelStaleLower.value).some(k => k.startsWith('other/'));
    }
    const chKey = (tab.targetProvider || tab.id || '').toLowerCase();
    return !!channelStaleLower.value[chKey];
  }

  function collectStaleMappingsInTab(tabId: string): ModelMappingEntry[] {
    const tab = poolTabs.value.find(t => t.id === tabId) || poolTabs.value[0];
    if (!tab) return [];
    const provider = (tab.targetProvider || tab.id).trim();
    const isOther = tab.targetProvider === 'other' || tab.id === 'other';
    const staleToRemove: ModelMappingEntry[] = [];
    // 同一 liveKey 的远端全集小写 Set 只建一次(原先逐条映射重建,O(n*m) → O(n+m))。
    const liveSetCache = new Map<string, Set<string>>();
    for (const m of allMappings.value) {
      if (getMappingTab(m) !== tabId) continue;
      const cm = (m.clientModel || '').trim();
      const tm = (m.targetModel || '').trim();
      if (!cm || !tm) continue;
      let liveKey = provider.toLowerCase();
      if (isOther) {
        const mtch = cm.match(/^other\/([^/]+)\//);
        const gid = mtch ? mtch[1] : '';
        if (mtch) liveKey = `other/${gid}`; else continue;
      }
      const live = channelModelsCache.value[liveKey];
      if (!live || live.length === 0) continue;
      let liveSet = liveSetCache.get(liveKey);
      if (!liveSet) {
        liveSet = buildLiveSetLower(live);
        liveSetCache.set(liveKey, liveSet);
      }
      if (shouldMarkStale(m, liveSet)) staleToRemove.push(m);
    }
    return staleToRemove;
  }

  async function loadModelMappings() {
    try {
      try {
        const chans = await ipcRenderer.invoke('relay:get-account-channels');
        if (Array.isArray(chans) && chans.length > 0) {
          availableChannels.value = Array.from(new Set([...availableChannels.value, ...chans]));
        }
      } catch (e) {
        console.warn('[useModelMapping] Failed to get channels:', e);
      }

      const list = await ipcRenderer.invoke('relay:get-model-mapping');
      allMappings.value = (list || []).map((m: any) => ({ ...m, _rowKey: nextRowKey() }));

      poolTabs.value = [
        { id: 'google', name: 'Gemini (Google)', targetProvider: 'google' },
        { id: 'nvidia', name: 'NVIDIA 号池', targetProvider: 'nvidia' },
        { id: 'other', name: 'Other 号池', targetProvider: 'other' },
        { id: 'gcp', name: '谷歌云 API', targetProvider: 'gcp' },
        { id: 'grok', name: 'Grok 号池', targetProvider: 'grok' },
        { id: 'workbuddy', name: 'WorkBuddy 号池', targetProvider: 'workbuddy' },
      ];

      const knownIds = new Set(poolTabs.value.map(t => t.id));
      allMappings.value.forEach(m => {
        if (m.ownedBy && !knownIds.has(m.ownedBy)) {
          knownIds.add(m.ownedBy);
          poolTabs.value.push({
            id: m.ownedBy,
            name: (m.ownedBy.charAt(0).toUpperCase() + m.ownedBy.slice(1)) + ' 号池',
            targetProvider: m.targetProvider || m.ownedBy,
            isCustom: true,
          });
        }
      });

      if (!poolTabs.value.some(t => t.id === activeTabId.value)) {
        activeTabId.value = poolTabs.value[0]?.id || 'google';
      }

      await refreshOtherGroups();
    } catch (err) {
      console.error('[useModelMapping] Failed to load:', err);
    }
  }

  // 监听 accounts-res 广播, 当 Other 账号添加/修改/删除/改名时, 即时同步 otherGroups 供模型映射面板展示。
  // 每次挂载都会执行, 必须随组件作用域销毁解绑, 否则设置页反复进出会累积死监听器(泄漏 + 空转)。
  const offAccountsRes = ipcRenderer.on('accounts-res', (_event: any, data: any) => {
    if (data && Array.isArray(data.otherGroups)) {
      otherGroups.value = data.otherGroups.map((g: any) => ({
        groupId: String(g.groupId || g.groupID || g.id || ''),
        groupName: String(g.groupName || g.groupId || ''),
        formats: Array.isArray(g.formats) ? g.formats : [],
        accountCount: Number(g.accountCount) || 0,
        enabledCount: Number(g.enabledCount) || 0,
      })).filter((g: OtherGroupInfo) => g.groupId);
    }
  });
  if (getCurrentScope()) {
    onScopeDispose(() => offAccountsRes());
  }

  async function refreshOtherGroups() {
    otherGroups.value = await getOtherGroups();
  }

  async function getOtherGroups(): Promise<OtherGroupInfo[]> {
    try {
      const backendData = (state as any).lastBackendData;
      if (backendData && Array.isArray(backendData.otherGroups) && backendData.otherGroups.length > 0) {
        return backendData.otherGroups.map((g: any) => ({
          groupId: String(g.groupId || g.groupID || g.id || ''),
          groupName: String(g.groupName || g.groupId || ''),
          formats: Array.isArray(g.formats) ? g.formats : [],
          accountCount: Number(g.accountCount) || 0,
          enabledCount: Number(g.enabledCount) || 0,
        })).filter((g: OtherGroupInfo) => g.groupId);
      }
    } catch (e) { /* ignore */ }
    try {
      const res = await ipcRenderer.invoke('other:list-groups');
      if (res && res.success && Array.isArray(res.groups)) {
        return res.groups.map((g: any) => ({
          groupId: String(g.groupId || g.groupID || g.id || ''),
          groupName: String(g.groupName || g.groupId || ''),
          formats: Array.isArray(g.formats) ? g.formats : [],
          accountCount: Number(g.accountCount) || 0,
          enabledCount: Number(g.enabledCount) || 0,
        })).filter((g: OtherGroupInfo) => g.groupId);
      }
    } catch (e) { /* ignore */ }
    return [];
  }

  async function ensureOtherEntryGroupId(entry: ModelMappingEntry): Promise<string> {
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

  async function fetchChannelModels() {
    const tab = currentTab.value;
    if (!tab) return;
    const channel = tab.targetProvider || tab.id;
    fetching.value = true;
    fetchStatusMsg.value = '';

    try {
      const res = await ipcRenderer.invoke('relay:fetch-channel-models', channel);
      if (res && res.success && Array.isArray(res.models)) {
        channelModelsCache.value[channel] = res.models;
        const snapshot: string[] = Array.isArray(res.snapshot) ? res.snapshot : [];
        const addedRaw: string[] = Array.isArray(res.added) ? res.added : [];
        const chKey = (channel || '').toLowerCase();
        channelAddedLower.value = {
          ...channelAddedLower.value,
          [chKey]: computeRemoteAddedLower(res.models, snapshot),
        };
        if (addedRaw.length > 0) {
          channelAddedLower.value[chKey] = new Set(addedRaw.map((s: string) => (s || '').trim().toLowerCase()).filter(Boolean));
        }
        channelStaleLower.value = {
          ...channelStaleLower.value,
          [chKey]: buildLiveSetLower(res.models),
        };
        const newCount = channelAddedLower.value[chKey].size;
        fetchStatusMsg.value = `✅ 已获取 ${res.models.length} 个模型${newCount > 0 ? ` · 新增 ${newCount}` : ''}`;

        const provider = (tab.targetProvider || tab.id || '').trim();
        const tabId = tab.id;
        const existingClientSet = new Set<string>();
        for (const m of allMappings.value) {
          const cm = (m.clientModel || '').trim();
          if (cm) existingClientSet.add(cm.toLowerCase());
        }
        // 按当前 Tab 作用域去重:仅收集本 Tab 已存在的上游模型名,
        // 避免全局 targetModel 去重误伤跨号池同名模型(不同号池上游可提供同名模型)。
        const existingTargetSet = new Set<string>();
        for (const m of allMappings.value) {
          if (getMappingTab(m) === tabId) {
            const tm = (m.targetModel || '').trim();
            if (tm) existingTargetSet.add(tm.toLowerCase());
          }
        }
        const newEntries: ModelMappingEntry[] = [];
        for (const modelRaw of res.models) {
          const model = (modelRaw || '').trim();
          if (!model) continue;
          if (existingTargetSet.has(model.toLowerCase())) continue;
          const isGoogle = isGoogleProviderKind(provider);
          if (isGoogle) {
            if (!existingClientSet.has(model.toLowerCase())) {
              newEntries.push(makeMappingEntry(model, model, provider, true));
              existingClientSet.add(model.toLowerCase());
            }
            const prefixed = `${provider}/${model}`;
            if (!existingClientSet.has(prefixed.toLowerCase())) {
              newEntries.push(makeMappingEntry(prefixed, model, provider, true));
              existingClientSet.add(prefixed.toLowerCase());
            }
          } else {
            const prefixed = `${provider}/${model}`;
            if (!existingClientSet.has(prefixed.toLowerCase())) {
              newEntries.push(makeMappingEntry(prefixed, model, provider, true));
              existingClientSet.add(prefixed.toLowerCase());
            }
          }
          existingTargetSet.add(model.toLowerCase());
        }
        if (newEntries.length > 0) {
          const tabId = tab.id;
          for (const ne of newEntries) {
            ne.ownedBy = tabId;
            ne._rowKey = nextRowKey();
            allMappings.value.push(ne);
          }
          // 新条目追加在列表尾部,跳到最后一页让用户立即看到带「新增」徽章的行。
          currentPage.value = totalPages.value;
        }
      } else {
        fetchStatusMsg.value = `❌ 获取失败: ${res?.error || '网络超时'}`;
      }
    } catch (e: any) {
      console.error('[useModelMapping] Fetch models error:', e);
      fetchStatusMsg.value = `❌ 获取出错`;
    } finally {
      fetching.value = false;
    }
  }

  async function fetchOtherGroupModels(groupId: string, groupName: string) {
    fetching.value = true;
    fetchStatusMsg.value = '';

    try {
      const res = await ipcRenderer.invoke('other:fetch-models', groupId);
      if (res && res.success && Array.isArray(res.models)) {
        const grpKey = `other/${groupId}`;
        channelModelsCache.value[grpKey] = res.models;
        const prevCache = channelModelsCachePrev.value[grpKey];
        channelAddedLower.value = {
          ...channelAddedLower.value,
          [grpKey]: prevCache
            ? computeRemoteAddedLower(res.models, prevCache)
            : new Set(res.models.map((s: string) => (s || '').trim().toLowerCase()).filter(Boolean)),
        };
        channelModelsCachePrev.value = {
          ...channelModelsCachePrev.value,
          [grpKey]: res.models.slice(),
        };
        channelStaleLower.value = {
          ...channelStaleLower.value,
          [grpKey]: buildLiveSetLower(res.models),
        };
        const newCount = channelAddedLower.value[grpKey].size;
        fetchStatusMsg.value = `✅ [${groupName}] 已获取 ${res.models.length} 个模型${newCount > 0 ? ` · 新增 ${newCount}` : ''}`;

        const provider = 'other';
        const existingClientSet = new Set<string>();
        for (const m of allMappings.value) {
          const cm = (m.clientModel || '').trim();
          if (cm) existingClientSet.add(cm.toLowerCase());
        }
        const existingSameGroupTargetSet = new Set<string>();
        for (const m of allMappings.value) {
          const cm = (m.clientModel || '').trim().toLowerCase();
          const tm = (m.targetModel || '').trim().toLowerCase();
          if (!tm) continue;
          if (cm.startsWith(`${provider}/${groupId}/`)) {
            existingSameGroupTargetSet.add(tm);
          }
        }
        const newEntries: ModelMappingEntry[] = [];
        for (const modelRaw of res.models) {
          const model = (modelRaw || '').trim();
          if (!model) continue;
          if (existingSameGroupTargetSet.has(model.toLowerCase())) continue;
          const prefixed = `${provider}/${groupId}/${model}`;
          if (!existingClientSet.has(prefixed.toLowerCase())) {
            const entry = makeMappingEntry(prefixed, model, provider, true);
            entry.ownedBy = 'other';
            entry.targetGroupId = groupId;
            newEntries.push(entry);
            existingClientSet.add(prefixed.toLowerCase());
          }
          existingSameGroupTargetSet.add(model.toLowerCase());
        }
        if (newEntries.length > 0) {
          for (const ne of newEntries) {
            ne._rowKey = nextRowKey();
            allMappings.value.push(ne);
          }
          currentPage.value = totalPages.value;
        }
      } else if (res && res.allowManualInput) {
        fetchStatusMsg.value = `⚠️ [${groupName}] 上游暂不支持模型列表,请手动填写(前缀 other/${groupId}/)`;
      } else {
        fetchStatusMsg.value = `❌ [${groupName}] 获取失败: ${res?.error || '网络超时'}`;
      }
    } catch (e: any) {
      console.error('[useModelMapping] Fetch other group models error:', e);
      fetchStatusMsg.value = `❌ [${groupName}] 获取出错`;
    } finally {
      fetching.value = false;
    }
  }

  function selectTab(tabId: string) {
    activeTabId.value = tabId;
    searchQuery.value = '';
    currentPage.value = 1;
  }

  function addTab() {
    const tabName = prompt('请输入新号池 Tab 名称（例如：DeepSeek 账号池）：');
    if (!tabName || !tabName.trim()) return;
    const providerId = prompt('请输入该 Tab 绑定的号池 Provider 标识（例如：deepseek）：') || tabName.trim().toLowerCase();
    const tabId = 'custom_' + Date.now();
    poolTabs.value.push({
      id: tabId,
      name: tabName.trim(),
      targetProvider: providerId.trim().toLowerCase(),
      isCustom: true,
    });
    activeTabId.value = tabId;
  }

  function deleteTab(tabId: string) {
    const tab = poolTabs.value.find(t => t.id === tabId);
    if (!tab || !tab.isCustom) return;
    if (!confirm(`确定要删除号池 Tab「${tab.name}」及其下的所有模型映射吗？`)) return;
    allMappings.value = allMappings.value.filter(m => getMappingTab(m) !== tabId);
    poolTabs.value = poolTabs.value.filter(t => t.id !== tabId);
    if (activeTabId.value === tabId) {
      activeTabId.value = poolTabs.value[0]?.id || 'google';
    }
  }

  function deleteCurrentTab() {
    deleteTab(activeTabId.value);
  }

  function addModelMapping() {
    if (searchQuery.value) searchQuery.value = '';
    const tab = currentTab.value;
    const targetProv = tab ? tab.targetProvider : '';
    allMappings.value.unshift({
      clientModel: '',
      targetModel: '',
      expose: true,
      injectChatTemplateKwargs: targetProv !== 'other',
      ownedBy: activeTabId.value,
      targetProvider: targetProv,
      variantEfforts: [],
      _rowKey: nextRowKey(),
    });
    // unshift 在列表头部,回到第 1 页让新空行立即可见。
    currentPage.value = 1;
  }

  function deleteMapping(item: ModelMappingEntry) {
    const idx = allMappings.value.indexOf(item);
    if (idx !== -1) {
      allMappings.value.splice(idx, 1);
    }
  }

  function updateTabProvider(provider: string) {
    const tab = currentTab.value;
    if (!tab) return;
    tab.targetProvider = provider;
    allMappings.value.forEach(m => {
      if (getMappingTab(m) === activeTabId.value) {
        m.targetProvider = provider;
      }
    });
  }

  async function onTargetModelChange(item: ModelMappingEntry, val: string) {
    item.targetModel = val;
    if (!item.clientModel || !item.clientModel.trim()) {
      const tab = currentTab.value;
      const provider = tab?.targetProvider || '';
      if (provider === 'other') {
        const gid = await ensureOtherEntryGroupId(item);
        const autoClient = gid ? `other/${gid}/${val}` : val;
        item.clientModel = autoClient;
      } else {
        item.clientModel = !isGoogleProviderKind(provider) ? `${provider}/${val}` : val;
      }
    }
  }

  async function saveModelMappings() {
    saving.value = true;
    saveStatus.value = 'idle';

    allMappings.value.forEach(m => {
      const tabId = getMappingTab(m);
      const tabObj = poolTabs.value.find(t => t.id === tabId);
      if (!m.ownedBy) m.ownedBy = tabId;
      if (tabObj && tabObj.targetProvider) {
        m.targetProvider = tabObj.targetProvider;
      }
    });

    const filtered = allMappings.value.filter(
      m => m.clientModel && m.clientModel.trim() !== '' && m.targetModel && m.targetModel.trim() !== ''
    );

    const mappingsToSave: ModelMappingEntry[] = [];
    for (const m of filtered) {
      // _rowKey 是前端行渲染专用字段,剔除后再落盘。
      const { _rowKey, ...entryWithoutRowKey } = m;
      mappingsToSave.push(entryWithoutRowKey);
    }

    try {
      const res = await ipcRenderer.invoke('relay:set-model-mapping', mappingsToSave);
      if (res && res.success) {
        saveStatus.value = 'success';
      } else {
        saveStatus.value = 'error';
      }
      setTimeout(() => { saveStatus.value = 'idle'; }, 2000);
    } catch (err) {
      console.error('[useModelMapping] Save error:', err);
      saveStatus.value = 'error';
      setTimeout(() => { saveStatus.value = 'idle'; }, 2000);
    } finally {
      saving.value = false;
    }
  }

  function clearStaleMappings() {
    const dict = (i18n as any)[state.currentLanguage] || (i18n as any).zh || {};
    const staleToRemove = collectStaleMappingsInTab(activeTabId.value);
    if (staleToRemove.length === 0) {
      const msg = hasFetchedStaleBasis(activeTabId.value)
        ? (dict.relayClearStaleEmpty || '当前号池无失效模型映射')
        : (dict.relayClearStaleNeedFetch || '请先成功「获取号池模型」以核对远端全集');
      window.alert(msg);
      return;
    }
    const promptText = buildStaleConfirmPrompt(staleToRemove);
    if (!window.confirm(promptText)) return;
    const removeSet = new Set(staleToRemove);
    allMappings.value = allMappings.value.filter(m => !removeSet.has(m));
  }

  const allKnownModelOptions = computed(() => {
    const set = new Set<string>();
    allMappings.value.forEach(m => {
      const cm = (m.clientModel || '').trim();
      const tm = (m.targetModel || '').trim();
      if (cm && cm.toLowerCase() !== 'auto') set.add(cm);
      if (tm && tm.toLowerCase() !== 'auto' && tm.toLowerCase() !== 'benchmark-pool') set.add(tm);
      if (Array.isArray(m.candidateModels)) {
        m.candidateModels.forEach(c => {
          if (c && c.trim()) set.add(c.trim());
        });
      }
    });
    Object.values(channelModelsCache.value).forEach(list => {
      if (Array.isArray(list)) {
        list.forEach(m => {
          if (m && m.trim()) set.add(m.trim());
        });
      }
    });
    return Array.from(set);
  });

  function updateAutoMapping(entry: ModelMappingEntry) {
    const idx = allMappings.value.findIndex(m => (m.clientModel || '').trim().toLowerCase() === 'auto');
    if (idx >= 0) {
      allMappings.value[idx] = { ...allMappings.value[idx], ...entry };
    } else {
      allMappings.value.unshift({ ...entry, _rowKey: nextRowKey() });
    }
  }

  return {
    allMappings,
    allKnownModelOptions,
    updateAutoMapping,
    poolTabs,
    activeTabId,
    availableChannels,
    channelModelsCache,
    searchQuery,
    channelAddedLower,
    channelStaleLower,
    saving,
    saveStatus,
    fetching,
    fetchStatusMsg,
    otherGroups,
    currentTab,
    isNvidiaTab,
    isOtherTab,
    filteredMappings,
    currentTabMappings,
    pageSize,
    currentPage,
    totalPages,
    pagedMappings,
    gotoPage,
    staleCount,
    loadModelMappings,
    refreshOtherGroups,
    fetchChannelModels,
    fetchOtherGroupModels,
    selectTab,
    addTab,
    deleteTab,
    deleteCurrentTab,
    addModelMapping,
    deleteMapping,
    updateTabProvider,
    onTargetModelChange,
    saveModelMappings,
    clearStaleMappings,
    getRowModels,
    isStaleItem,
    isNewItem,
    ensureOtherEntryGroupId,
    hasFetchedStaleBasis,
  };
}
