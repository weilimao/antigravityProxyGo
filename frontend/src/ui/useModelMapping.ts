import { ref, computed, type Ref } from 'vue';
import { ipcRenderer } from '../shared/ipc';
import state from './dashboardState';
import i18n from '../shared/i18n';
import {
  buildLiveSetLower, buildStaleConfirmPrompt, computeRemoteAddedLower,
  shouldMarkNew, shouldMarkStale,
} from './relayModelDiff';
import type { ModelMappingEntry, PoolTabInfo, OtherGroupInfo } from '../components/settings/relay-mapping/types';

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
  if (modelName.startsWith('deepseek')) return 'deepseek';
  if (modelName.startsWith('qwen')) return 'qwen';
  if (modelName.startsWith('claude')) return 'anthropic';
  return 'google';
}

export function useModelMapping() {
  const allMappings: Ref<ModelMappingEntry[]> = ref([]);
  const poolTabs: Ref<PoolTabInfo[]> = ref([]);
  const activeTabId = ref('google');
  const availableChannels = ref<string[]>(['antigravity', 'google', 'gcp', 'nvidia', 'other', 'grok']);
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
    allMappings.value.filter(m => getMappingTab(m) === activeTabId.value)
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

  function getRowModels(item: ModelMappingEntry): string[] {
    if (!isOtherTab.value) {
      const ch = currentTab.value?.targetProvider || currentTab.value?.id || '';
      return channelModelsCache.value[ch] || [];
    }
    const cm = (item.clientModel || '').trim();
    const m = cm.match(/^other\/([^/]+)\//);
    const gid = m ? m[1] : '';
    return gid ? (channelModelsCache.value[`other/${gid}`] || []) : [];
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
    const provider = (tab.targetProvider || tab.id || '').trim();
    const isOther = tab.targetProvider === 'other' || tab.id === 'other';
    const staleToRemove: ModelMappingEntry[] = [];
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
      const liveSet = buildLiveSetLower(live);
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
      allMappings.value = (list || []).map((m: any) => ({ ...m }));

      poolTabs.value = [
        { id: 'google', name: 'Gemini (Google)', targetProvider: 'google' },
        { id: 'nvidia', name: 'NVIDIA 号池', targetProvider: 'nvidia' },
        { id: 'other', name: 'Other 号池', targetProvider: 'other' },
        { id: 'gcp', name: '谷歌云 API', targetProvider: 'gcp' },
        { id: 'grok', name: 'Grok 号池', targetProvider: 'grok' },
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
        const existingClientSet = new Set<string>();
        for (const m of allMappings.value) {
          const cm = (m.clientModel || '').trim();
          if (cm) existingClientSet.add(cm.toLowerCase());
        }
        const existingTargetSet = new Set<string>();
        for (const m of allMappings.value) {
          const tm = (m.targetModel || '').trim();
          if (tm) existingTargetSet.add(tm.toLowerCase());
        }
        const newEntries: ModelMappingEntry[] = [];
        for (const modelRaw of res.models) {
          const model = (modelRaw || '').trim();
          if (!model) continue;
          if (existingTargetSet.has(model.toLowerCase())) continue;
          const isGoogle = isGoogleProviderKind(provider);
          if (isGoogle) {
            newEntries.push(makeMappingEntry(model, model, provider, true));
            const prefixed = `${provider}/${model}`;
            if (!existingClientSet.has(prefixed.toLowerCase())) {
              newEntries.push(makeMappingEntry(prefixed, model, provider, true));
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
            allMappings.value.push(ne);
          }
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
          for (const ne of newEntries) allMappings.value.push(ne);
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
    });
  }

  function deleteMapping(index: number) {
    const filtered = filteredMappings.value;
    const targetItem = filtered[index];
    if (!targetItem) return;
    const mainIdx = allMappings.value.indexOf(targetItem);
    if (mainIdx !== -1) {
      allMappings.value.splice(mainIdx, 1);
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
        mappingsToSave.push(m);
      } else if (!isGoogleProviderKind(provider) && cm === tm && !cm.toLowerCase().startsWith(`${provider.toLowerCase()}/`)) {
        m.clientModel = `${provider}/${cm}`;
        mappingsToSave.push(m);
      } else {
        mappingsToSave.push(m);
      }
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

  return {
    allMappings,
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
