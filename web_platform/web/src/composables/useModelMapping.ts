import { ref, computed, watch } from 'vue';
import { mappingApi } from '../api/client';
import {
  buildLiveSetLower, buildStaleConfirmPrompt, computeRemoteAddedLower,
  shouldMarkNew, shouldMarkStale,
} from './relayModelDiff';
import type { ModelMappingEntry, PoolTabInfo, OtherGroupInfo } from './mappingTypes';

const EMPTY_MODELS: string[] = [];

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
    candidateModels: [],
    variantEfforts: [],
  };
}

function getMappingTab(m: ModelMappingEntry): string {
  if (m.ownedBy) {
    if (m.ownedBy === 'other' || m.ownedBy.startsWith('other/')) return 'other';
    return m.ownedBy;
  }
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
  const allMappings = ref<ModelMappingEntry[]>([]);
  const poolTabs = ref<PoolTabInfo[]>([]);
  const otherGroups = ref<OtherGroupInfo[]>([]);
  const activeTabId = ref('google');
  const availableChannels = ref<string[]>(['antigravity', 'google', 'gcp', 'nvidia', 'other', 'grok', 'workbuddy']);
  const channelModelsCache = ref<Record<string, string[]>>({});
  const channelModelsCachePrev = ref<Record<string, string[]>>({});
  const searchQuery = ref('');
  const channelAddedLower = ref<Record<string, Set<string>>>({});
  const channelStaleLower = ref<Record<string, Set<string>>>({});
  const saving = ref(false);
  const fetching = ref(false);
  const fetchStatusMsg = ref('');

  const currentTab = computed(() =>
    poolTabs.value.find(t => t.id === activeTabId.value) || poolTabs.value[0]
  );

  const isNvidiaTab = computed(() =>
    currentTab.value && (currentTab.value.targetProvider === 'nvidia' || currentTab.value.id === 'nvidia')
  );

  const isOtherTab = computed(() =>
    currentTab.value && (currentTab.value.targetProvider === 'other' || currentTab.value.id === 'other')
  );

  const selectedOtherSubGroup = ref<string>('all');
  const fetchingGroupId = ref<string>('');

  const otherSubGroups = computed(() => {
    const map = new Map<string, { groupId: string; groupName: string; formats: string[]; count: number }>();
    for (const g of otherGroups.value) {
      const gid = g.groupId.toLowerCase();
      map.set(gid, {
        groupId: g.groupId,
        groupName: g.groupName || g.groupId,
        formats: g.formats || [],
        count: 0,
      });
    }
    for (const m of allMappings.value) {
      if (getMappingTab(m) === 'other') {
        const cm = (m.clientModel || '').trim();
        const match = cm.match(/^other\/([^/]+)\//i);
        const gid = match ? match[1].toLowerCase() : (m.targetGroupId || '').trim().toLowerCase();
        if (gid) {
          if (!map.has(gid)) {
            map.set(gid, { groupId: gid, groupName: gid, formats: ['openai'], count: 0 });
          }
          map.get(gid)!.count++;
        }
      }
    }
    return Array.from(map.values());
  });

  const totalOtherMappingsCount = computed(() => {
    return allMappings.value.filter(m => getMappingTab(m) === 'other').length;
  });

  const currentTabMappings = computed(() => {
    const tabMappings = allMappings.value.filter(m => getMappingTab(m) === activeTabId.value);
    if (isOtherTab.value && selectedOtherSubGroup.value && selectedOtherSubGroup.value !== 'all') {
      const targetGid = selectedOtherSubGroup.value.toLowerCase();
      return tabMappings.filter(m => {
        const cm = (m.clientModel || '').trim().toLowerCase();
        const match = cm.match(/^other\/([^/]+)\//i);
        const gid = match ? match[1].toLowerCase() : (m.targetGroupId || '').trim().toLowerCase();
        return gid === targetGid;
      });
    }
    return tabMappings;
  });

  function selectOtherSubGroup(gid: string) {
    selectedOtherSubGroup.value = gid;
    currentPage.value = 1;
  }

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
      const list = await mappingApi.getMappings();
      allMappings.value = (list || []).map((m: any) => ({ ...m, _rowKey: nextRowKey() }));

      poolTabs.value = [
        { id: 'google', name: 'Gemini (Google)', targetProvider: 'google' },
        { id: 'nvidia', name: 'NVIDIA 号池', targetProvider: 'nvidia' },
        { id: 'other', name: 'Other 号池', targetProvider: 'other' },
        { id: 'gcp', name: '谷歌云 API', targetProvider: 'gcp' },
        { id: 'grok', name: 'Grok 号池', targetProvider: 'grok' },
        { id: 'workbuddy', name: 'WorkBuddy 号池', targetProvider: 'workbuddy' },
      ];

      await refreshOtherGroups();

      const knownIds = new Set(poolTabs.value.map(t => t.id));
      allMappings.value.forEach(m => {
        if (m.ownedBy && !knownIds.has(m.ownedBy) && !m.ownedBy.startsWith('other/')) {
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
    } catch (err) {
      console.error('[useModelMapping] Failed to load:', err);
    }
  }

  async function refreshOtherGroups() {
    try {
      const res = await mappingApi.getOtherGroups();
      const groupList = (res && res.groups && Array.isArray(res.groups)) ? res.groups : (Array.isArray(res) ? res : []);
      if (groupList.length > 0) {
        otherGroups.value = groupList.map((g: any) => ({
          groupId: String(g.groupId || g.groupID || g.id || ''),
          groupName: String(g.groupName || g.groupId || ''),
          formats: Array.isArray(g.formats) ? g.formats : [],
          accountCount: Number(g.accountCount) || 0,
          enabledCount: Number(g.enabledCount) || 0,
        })).filter((g: any) => g.groupId);
      } else {
        otherGroups.value = [];
      }
    } catch (e) {
      console.warn('[useModelMapping] Failed to fetch other groups from gateway:', e);
      otherGroups.value = [];
    }
  }

  async function ensureOtherEntryGroupId(entry: ModelMappingEntry): Promise<string> {
    if (!entry) return '';
    const existing = (entry.targetGroupId || '').trim();
    if (existing) return existing;
    
    const gid = prompt('该映射未绑定 Other 组，请输入目标组 ID（例如: siliconflow）：');
    if (!gid) {
      return '';
    }
    entry.targetGroupId = gid.trim();
    return gid.trim();
  }

  async function fetchChannelModels() {
    const tab = currentTab.value;
    if (!tab) return;
    const channel = tab.targetProvider || tab.id;
    fetching.value = true;
    
    try {
      const res = await mappingApi.fetchChannelModels(channel);
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

  async function fetchOtherGroupModels(groupId: string, groupName?: string) {
    if (!groupId) return;
    fetching.value = true;
    fetchingGroupId.value = groupId;
    const displayName = groupName || groupId;
    fetchStatusMsg.value = `正在拉取 [${displayName}] 上游模型快照...`;
    
    try {
      const res = await mappingApi.fetchOtherGroupModels(groupId);
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
        fetchStatusMsg.value = `✅ [${displayName}] 已获取 ${res.models.length} 个最新模型${newCount > 0 ? ` · 新增 ${newCount} 个映射` : ''}`;
        selectedOtherSubGroup.value = groupId;

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
          currentPage.value = 1;
        }
      } else if (res && res.allowManualInput) {
        fetchStatusMsg.value = `⚠️ [${displayName}] 上游暂不支持模型列表,请手动填写(前缀 other/${groupId}/)`;
      } else {
        fetchStatusMsg.value = `❌ [${displayName}] 获取失败: ${res?.error || '网络超时或上游未响应'}`;
      }
    } catch (e: any) {
      console.error('[useModelMapping] Fetch other group models error:', e);
      fetchStatusMsg.value = `❌ [${displayName}] 获取出错: ${e?.message || '网络异常'}`;
    } finally {
      fetching.value = false;
      fetchingGroupId.value = '';
    }
  }

  function selectTab(tabId: string) {
    activeTabId.value = tabId;
    searchQuery.value = '';
    currentPage.value = 1;
    selectedOtherSubGroup.value = 'all';
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
      const provider = (m.targetProvider || '').trim();
      const cm = (m.clientModel || '').trim();
      const tm = (m.targetModel || '').trim();
      if (provider === 'other') {
        if (cm === tm) {
          const gid = (m.targetGroupId || '').trim();
          if (gid) {
            m.clientModel = `other/${gid}/${cm}`;
          }
        }
      } else if (!isGoogleProviderKind(provider) && cm === tm && !cm.toLowerCase().startsWith(`${provider.toLowerCase()}/`)) {
        m.clientModel = `${provider}/${cm}`;
      }
      const { _rowKey, ...entryWithoutRowKey } = m;
      mappingsToSave.push(entryWithoutRowKey);
    }

    try {
      const res = await mappingApi.setMappings(mappingsToSave);
      fetchStatusMsg.value = `✅ ${res?.message || '模型映射已保存并同步'}`;
    } catch (err: any) {
      console.error('[useModelMapping] Save error:', err);
      fetchStatusMsg.value = `❌ 保存失败: ${err?.message || '保存出错'}`;
    } finally {
      saving.value = false;
    }
  }

  function clearStaleMappings() {
    const staleToRemove = collectStaleMappingsInTab(activeTabId.value);
    if (staleToRemove.length === 0) {
      const msg = hasFetchedStaleBasis(activeTabId.value)
        ? '当前号池无失效模型映射'
        : '请先成功「获取号池模型」以核对远端全集';
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
    fetching,
    fetchStatusMsg,
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
    otherGroups,
    otherSubGroups,
    fetchingGroupId,
    totalOtherMappingsCount,
    selectedOtherSubGroup,
    selectOtherSubGroup,
    refreshOtherGroups,
  };
}
