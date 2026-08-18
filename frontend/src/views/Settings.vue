<template>
<div class="flex flex-col gap-5 w-full" id="view-settings">
<div class="flex flex-wrap items-center justify-between gap-4 border-b border-outline-variant/20 pb-4">
<div>
<h1 class="text-2xl font-bold text-on-surface dark:text-white" data-i18n="settingsTitle">系统设置</h1>
<p class="text-xs text-outline dark:text-outline-variant" data-i18n="settingsDesc">配置代理软件的底层行为与本地数据存储路径</p>
</div>
<!-- Sub Tab Menu -->
<div class="flex gap-1 bg-slate-100 dark:bg-white/5 p-1 rounded-lg text-[12px]">
<button class="px-4 py-1.5 text-[12px] bg-white dark:bg-[#1a1f30] text-primary dark:text-primary-fixed-dim rounded-md shadow-sm font-bold cursor-pointer transition-all duration-200" id="btnSettingsTabGeneral" data-i18n="settingsTabGeneral">参数配置</button>
<button class="px-4 py-1.5 text-[12px] text-slate-500 dark:text-slate-400 hover:text-slate-800 dark:hover:text-slate-200 rounded-md font-medium cursor-pointer transition-all duration-200" id="btnSettingsTabNvidia" data-i18n="settingsTabNvidia">NVIDIA设置</button>
<button class="px-4 py-1.5 text-[12px] text-slate-500 dark:text-slate-400 hover:text-slate-800 dark:hover:text-slate-200 rounded-md font-medium cursor-pointer transition-all duration-200" data-i18n="settingsTabRelay" id="btnSettingsTabRelay">中继服务器</button>
<button class="px-4 py-1.5 text-[12px] text-slate-500 dark:text-slate-400 hover:text-slate-800 dark:hover:text-slate-200 rounded-md font-medium cursor-pointer transition-all duration-200" id="btnSettingsTabNetwork" data-i18n="settingsTabNetwork">网络监控</button>
<button class="px-4 py-1.5 text-[12px] text-slate-500 dark:text-slate-400 hover:text-slate-800 dark:hover:text-slate-200 rounded-md font-medium cursor-pointer transition-all duration-200" id="btnSettingsTabAgentConfig" data-i18n="settingsTabAgentConfig">Agent配置</button>
<button class="px-4 py-1.5 text-[12px] text-slate-500 dark:text-slate-400 hover:text-slate-800 dark:hover:text-slate-200 rounded-md font-medium cursor-pointer transition-all duration-200" id="btnSettingsTabWallpaper" data-i18n="settingsTabWallpaper">外观壁纸</button>
<button class="px-4 py-1.5 text-[12px] text-slate-500 dark:text-slate-400 hover:text-slate-800 dark:hover:text-slate-200 rounded-md font-medium cursor-pointer transition-all duration-200" id="btnSettingsTabHelp" data-i18n="settingsTabHelp">使用说明</button>
<button class="px-4 py-1.5 text-[12px] text-slate-500 dark:text-slate-400 hover:text-slate-800 dark:hover:text-slate-200 rounded-md font-medium cursor-pointer transition-all duration-200" id="btnSettingsTabAbout" data-i18n="settingsTabAbout">关于</button>
</div>
</div>
<!-- 参数配置面板 -->
<GeneralPanel />
<!-- End settings-panel-general -->

<!-- 英伟达设置面板:NVIDIA 链路专属配置(思考直吐/Debugger/断流兜底代理),与参数配置独立 -->
<NvidiaPanel />
<!-- End settings-panel-nvidia -->

<!-- 网络监控面板 -->
<NetworkPanel />

<!-- Agent 配置管理面板 -->
<AgentConfigPanel />

<!-- 外观与壁纸调谐面板：仅在用户当前处于设置页且主动点击"外观壁纸"时挂载渲染，离开时 100% 卸载，0 渲染 0 内存占用 -->
<WallpaperPanel v-if="isWallpaperActive" />

<!-- 关于面板 -->
<AboutPanel />
<!-- 中继服务器管理面板 -->
<RelayPanel />
<!-- 使用说明面板（模块化组件） -->
<UsageHelpPanel />
</div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue';
import { useRoute } from 'vue-router';
import GeneralPanel from '../components/settings/panels/GeneralPanel.vue';
import NvidiaPanel from '../components/settings/panels/NvidiaPanel.vue';
import NetworkPanel from '../components/settings/panels/NetworkPanel.vue';
import AgentConfigPanel from '../components/settings/panels/AgentConfigPanel.vue';
import WallpaperPanel from '../components/settings/panels/WallpaperPanel.vue';
import AboutPanel from '../components/settings/panels/AboutPanel.vue';
import RelayPanel from '../components/settings/panels/RelayPanel.vue';
import UsageHelpPanel from '../components/settings/UsageHelpPanel.vue';
import PasswordInput from '../components/modals/PasswordInput.vue';
import { initSettings, onSettingsTabChanged } from '../ui/settingsController';
import { initRelayEvents } from '../ui/relayController';
import { initAboutPanelEvents } from '../ui/updaterController';

const route = useRoute();
const activeSubTab = ref<string>('general');
let unbindTabChanged: (() => void) | null = null;

// 仅在当前处于 /settings 路由且激活了 wallpaper 子页签时才挂载，离开即彻底销毁
const isWallpaperActive = computed(() => {
  return route.path === '/settings' && activeSubTab.value === 'wallpaper';
});

onMounted(() => {
  initSettings();
  initRelayEvents();
  initAboutPanelEvents();

  unbindTabChanged = onSettingsTabChanged((tabName: string) => {
    activeSubTab.value = tabName;
  });

  // 强制初始化子页签显隐状态，切到常规参数配置 Tab
  if (typeof (window as any).switchSettingsTab === 'function') {
    (window as any).switchSettingsTab('general');
  }
});

onUnmounted(() => {
  if (unbindTabChanged) {
    unbindTabChanged();
    unbindTabChanged = null;
  }
});
</script>
