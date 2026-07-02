<template>
  <div class="antialiased min-h-screen flex flex-col font-sans">
    <header ref="headerRef" class="bg-white/80 dark:bg-[#1a1f30]/80 backdrop-blur-md border-b border-outline-variant/30 flex justify-between items-center w-full px-4 lg:px-6 z-50 py-3">
        <!-- 左侧及中间区域：Logo、证书管理和导航链接/下拉菜单 -->
        <div class="flex items-center gap-6 md:gap-8 lg:gap-12 flex-grow min-w-0">
            <!-- Logo 和证书管理 -->
            <div ref="leftSection" class="flex items-center gap-2 md:gap-4 flex-shrink-0">
                <div class="text-[16px] md:text-[18px] font-bold text-primary dark:text-primary-fixed-dim flex items-center gap-1.5 whitespace-nowrap">
                    <img src="/src/assets/appicon.png" class="w-[18px] h-[18px] md:w-5 md:h-5 rounded-[4px] object-cover shadow-sm shadow-primary/20" />
                    Antigravity <span class="hidden sm:inline">Proxy</span>
                </div>
                <!-- CA 证书状态 -->
                <div class="ml-2 pl-2 md:ml-4 md:pl-4 border-l border-outline-variant/30 flex items-center gap-2 md:gap-3">
                    <div id="certStatusBadge" class="flex items-center gap-1.5 text-[12px] font-medium text-emerald-600 bg-emerald-50 dark:bg-emerald-950/30 dark:text-emerald-400 px-2.5 py-0.5 rounded-full border border-emerald-100 dark:border-emerald-900/30 flex-shrink-0">
                        <span class="material-symbols-outlined text-[15px]">verified</span>
                        <span data-i18n="certChecking">检查中...</span>
                    </div>
                    <div class="flex gap-2 text-[12px] items-center">
                        <button id="btnInstallCert" class="font-medium text-primary dark:text-primary-fixed-dim hover:underline disabled:hidden whitespace-nowrap text-[12px]" disabled data-i18n="installCert">安装证书</button>
                        <button id="btnUninstallCert" class="font-medium text-outline hover:text-red-500 hover:underline disabled:hidden whitespace-nowrap text-[12px]" disabled data-i18n="uninstallCert">卸载证书</button>
                    </div>
                </div>
            </div>

            <!-- 顶部导航链接 (展开状态下显示) -->
            <nav v-show="!isCollapsed" ref="navSection" class="flex items-center gap-3 md:gap-6 lg:gap-8 flex-shrink-0">
                <router-link to="/dashboard" active-class="text-primary dark:text-primary-fixed-dim border-primary" class="text-outline hover:text-primary transition-colors pb-0.5 flex flex-col items-center whitespace-nowrap border-b-2 border-transparent">
                    <span class="nav-link-en hidden lg:block text-[9px] font-bold tracking-wider">DASHBOARD</span>
                    <span class="nav-link-zh text-[13px] font-medium" data-i18n="title">控制台</span>
                </router-link>
                <router-link to="/accounts" active-class="text-primary dark:text-primary-fixed-dim border-primary" class="text-outline hover:text-primary transition-colors pb-0.5 flex flex-col items-center whitespace-nowrap border-b-2 border-transparent">
                    <span class="nav-link-en hidden lg:block text-[9px] font-bold tracking-wider">ACCOUNTS</span>
                    <span class="nav-link-zh text-[13px] font-medium" data-i18n="navAccounts">账号池</span>
                </router-link>
                <router-link to="/usage" active-class="text-primary dark:text-primary-fixed-dim border-primary" class="text-outline hover:text-primary transition-colors pb-0.5 flex flex-col items-center whitespace-nowrap border-b-2 border-transparent">
                    <span class="nav-link-en hidden lg:block text-[9px] font-bold tracking-wider">USAGE DETAILS</span>
                    <span class="nav-link-zh text-[13px] font-medium" data-i18n="navUsage">使用详情</span>
                </router-link>
                <router-link to="/otp" active-class="text-primary dark:text-primary-fixed-dim border-primary" class="text-outline hover:text-primary transition-colors pb-0.5 flex flex-col items-center whitespace-nowrap border-b-2 border-transparent">
                    <span class="nav-link-en hidden lg:block text-[9px] font-bold tracking-wider">2FA AUTH</span>
                    <span class="nav-link-zh text-[13px] font-medium" data-i18n="navOtp">2FA验证码</span>
                </router-link>
                <router-link id="navPacketsLink" to="/packets" active-class="text-primary dark:text-primary-fixed-dim border-primary" class="text-outline hover:text-primary transition-colors pb-0.5 flex flex-col items-center whitespace-nowrap border-b-2 border-transparent">
                    <span class="nav-link-en hidden lg:block text-[9px] font-bold tracking-wider">PACKETS</span>
                    <span class="nav-link-zh text-[13px] font-medium" data-i18n="navPackets">抓包分析</span>
                </router-link>
                <router-link to="/settings" active-class="text-primary dark:text-primary-fixed-dim border-primary" class="text-outline hover:text-primary transition-colors pb-0.5 flex flex-col items-center whitespace-nowrap border-b-2 border-transparent">
                    <span class="nav-link-en hidden lg:block text-[9px] font-bold tracking-wider">SETTINGS</span>
                    <span class="nav-link-zh text-[13px] font-medium" data-i18n="navSettings">设置</span>
                </router-link>
            </nav>

            <!-- 响应式下拉菜单 (折叠状态下显示) -->
            <div v-show="isCollapsed" ref="dropdownRef" class="relative flex-shrink-0">
                <button @click.stop="toggleDropdown" class="flex items-center gap-1.5 px-3 py-1.5 rounded-lg border border-outline-variant/30 text-outline hover:text-primary dark:hover:text-primary-fixed-dim bg-outline-variant/5 hover:bg-primary/5 transition-all text-[13px] font-medium select-none">
                    <span class="material-symbols-outlined text-[18px]">menu</span>
                    <span>{{ currentActiveLabel }}</span>
                    <span class="material-symbols-outlined text-[16px] transition-transform duration-200" :class="{ 'rotate-180': isDropdownOpen }">keyboard_arrow_down</span>
                </button>
                <div v-show="isDropdownOpen" class="absolute left-0 mt-2 w-48 bg-white/95 dark:bg-[#1a1f30]/95 backdrop-blur-md border border-outline-variant/30 rounded-xl shadow-xl py-1.5 z-50">
                    <router-link to="/dashboard" @click="isDropdownOpen = false" active-class="text-primary dark:text-primary-fixed-dim bg-primary/5 font-semibold" class="flex items-center px-4 py-2.5 text-[13px] text-outline hover:text-primary hover:bg-primary/5 transition-colors">
                        <span class="material-symbols-outlined text-[18px] mr-2">dashboard</span>
                        <span class="nav-link-zh" data-i18n="title">控制台</span>
                    </router-link>
                    <router-link to="/accounts" @click="isDropdownOpen = false" active-class="text-primary dark:text-primary-fixed-dim bg-primary/5 font-semibold" class="flex items-center px-4 py-2.5 text-[13px] text-outline hover:text-primary hover:bg-primary/5 transition-colors">
                        <span class="material-symbols-outlined text-[18px] mr-2">supervisor_account</span>
                        <span class="nav-link-zh" data-i18n="navAccounts">账号池</span>
                    </router-link>
                    <router-link to="/usage" @click="isDropdownOpen = false" active-class="text-primary dark:text-primary-fixed-dim bg-primary/5 font-semibold" class="flex items-center px-4 py-2.5 text-[13px] text-outline hover:text-primary hover:bg-primary/5 transition-colors">
                        <span class="material-symbols-outlined text-[18px] mr-2">monitoring</span>
                        <span class="nav-link-zh" data-i18n="navUsage">使用详情</span>
                    </router-link>
                    <router-link to="/otp" @click="isDropdownOpen = false" active-class="text-primary dark:text-primary-fixed-dim bg-primary/5 font-semibold" class="flex items-center px-4 py-2.5 text-[13px] text-outline hover:text-primary hover:bg-primary/5 transition-colors">
                        <span class="material-symbols-outlined text-[18px] mr-2">sms</span>
                        <span class="nav-link-zh" data-i18n="navOtp">2FA验证码</span>
                    </router-link>
                    <router-link id="navPacketsLinkDropdown" to="/packets" @click="isDropdownOpen = false" active-class="text-primary dark:text-primary-fixed-dim bg-primary/5 font-semibold" class="flex items-center px-4 py-2.5 text-[13px] text-outline hover:text-primary hover:bg-primary/5 transition-colors">
                        <span class="material-symbols-outlined text-[18px] mr-2">analytics</span>
                        <span class="nav-link-zh" data-i18n="navPackets">抓包分析</span>
                    </router-link>
                    <router-link to="/settings" @click="isDropdownOpen = false" active-class="text-primary dark:text-primary-fixed-dim bg-primary/5 font-semibold" class="flex items-center px-4 py-2.5 text-[13px] text-outline hover:text-primary hover:bg-primary/5 transition-colors">
                        <span class="material-symbols-outlined text-[18px] mr-2">settings</span>
                        <span class="nav-link-zh" data-i18n="navSettings">设置</span>
                    </router-link>
                </div>
            </div>
        </div>

        <!-- 顶部右侧控制按钮 -->
        <div ref="rightSection" :class="{ 'remote-collapsed': isCollapsed }" class="flex items-center gap-2 md:gap-4 lg:gap-6 flex-shrink-0 ml-auto">
            <!-- 远程连接 -->
            <div class="flex items-center gap-1.5 md:gap-2 border-r border-outline-variant/30 pr-2 mr-1 md:pr-4 md:mr-2 flex-shrink-0">
                <div id="remoteStatusBadge" class="hidden flex items-center gap-1.5 text-[12px] font-medium px-2.5 py-0.5 rounded-full border whitespace-nowrap flex-shrink-0">
                    <span class="material-symbols-outlined text-[15px]">cloud</span>
                    <span id="remoteStatusText">远程连接中</span>
                    <button id="btnManageApiKeys" class="hidden ml-2 text-primary dark:text-primary-fixed-dim hover:text-primary/80 text-[11px] font-bold border border-primary/20 rounded px-1.5 py-0.5 flex items-center gap-0.5 bg-primary/5 transition-all" title="管理持久化 API Keys" data-i18n-title="manageKeysTitle">
                        <span class="material-symbols-outlined text-[12px] pointer-events-none">key</span>
                        <span data-i18n="manageKeys">管理 Key</span>
                    </button>
                    <button id="btnRemoteEnable" class="hidden ml-1 text-emerald-600 dark:text-emerald-400 hover:text-emerald-700 text-[11px] font-bold" data-i18n="remoteEnable">启用</button>
                    <button id="btnRemoteDisable" class="hidden ml-1 text-amber-600 dark:text-amber-400 hover:text-amber-700 text-[11px] font-bold" data-i18n="remoteDisable">停用</button>
                    <button id="btnRemoteDisconnect" class="ml-1 text-red-400 hover:text-red-600 text-[11px] font-bold" data-i18n="remoteDisconnect">退出</button>
                </div>
                <button id="btnRemoteConnect" class="flex items-center gap-1 text-[12px] font-medium text-outline hover:text-primary dark:hover:text-primary-fixed-dim transition-colors px-2 py-1 rounded-md hover:bg-primary/5 whitespace-nowrap flex-shrink-0">
                    <span class="material-symbols-outlined text-[16px]">link</span>
                    <span data-i18n="remoteConnect">远程连接</span>
                </button>
            </div>
            <!-- 拦截开关 -->
            <div class="flex items-center gap-2 flex-shrink-0">
                <span class="hidden sm:inline text-[12px] md:text-[13px] font-medium text-on-surface dark:text-white" data-i18n="interceptMode">拦截模式</span>
                <div class="relative inline-block w-10 mr-1 align-middle select-none transition duration-200 ease-in">
                    <input class="toggle-checkbox absolute block w-5 h-5 rounded-full bg-white border-4 border-outline-variant appearance-none cursor-pointer translate-x-0 transition-transform duration-200 ease-in-out" id="proxyToggle" name="toggle" type="checkbox"/>
                    <label id="proxyToggleLabel" class="toggle-label block overflow-hidden h-5 rounded-full bg-outline-variant/50 dark:bg-white/10 cursor-pointer" for="proxyToggle"></label>
                </div>
                <span id="statusText" class="text-[13px] font-bold text-outline">OFF</span>
            </div>
            <!-- 多语言及主题 -->
            <div class="flex items-center gap-2 md:gap-3 border-l border-outline-variant/30 pl-2 md:pl-4 flex-shrink-0">
                <div class="flex items-center bg-outline-variant/20 dark:bg-white/5 rounded-full p-0.5">
                    <button id="toggleEN" @click="currentLang = 'en'" :class="currentLang === 'en' ? 'bg-white dark:bg-[#1a1f30] text-primary dark:text-primary-fixed-dim shadow-sm' : 'text-outline'" class="px-2 py-0.5 text-[11px] font-medium rounded-full transition-all">EN</button>
                    <button id="toggleZH" @click="currentLang = 'zh'" :class="currentLang === 'zh' ? 'bg-white dark:bg-[#1a1f30] text-primary dark:text-primary-fixed-dim shadow-sm' : 'text-outline'" class="px-2 py-0.5 text-[11px] font-medium rounded-full transition-all">中</button>
                </div>
                <button id="toggleTheme" class="text-outline hover:text-primary transition-colors flex items-center">
                    <span class="material-symbols-outlined text-[20px]" id="themeIcon">light_mode</span>
                </button>
            </div>
        </div>
    </header>

    <main class="flex-grow px-container-padding py-6 pb-[50px] w-full flex flex-col gap-6 overflow-y-auto relative">
      <Dashboard v-show="$route.path === '/' || $route.path === '/dashboard'" />
      <Accounts v-show="$route.path === '/accounts'" />
      <UsageDetails v-show="$route.path === '/usage'" />
      <OTP v-show="$route.path === '/otp'" />
      <Packets v-show="$route.path === '/packets'" />
      <Settings v-show="$route.path === '/settings'" />
    </main>

    <!-- 抽屉式控制台日志 -->
    <div id="systemConsole" class="fixed bottom-0 left-0 right-0 z-[100] bg-slate-100 dark:bg-[#0b0e17] border-t border-slate-200 dark:border-white/10 flex flex-col transition-all duration-300" style="height: 36px;">
        <div class="console-header h-[36px] px-6 flex items-center justify-between cursor-pointer select-none text-[12px] font-semibold text-slate-600 dark:text-[#988d9f] hover:bg-slate-500/5 transition-colors" id="consoleHeader">
            <span data-i18n="logBufferTitle">控制台系统日志</span>
            <span class="material-symbols-outlined text-[16px]">keyboard_double_arrow_up</span>
        </div>
        <div class="console-body flex-1 overflow-y-auto px-6 py-2 text-[11px] font-mono leading-relaxed" id="consoleBody" style="display: none;"></div>
    </div>

    <DetailsModal />
    <PricingModal />
    <RetryErrorLogsModal />
    <UpdateModal />
    <SessionBindingsModal />
    <ExportPacketsModal />
    <RemoteModal />
    <RemoteKeysModal />
    <RelayUserModal />
    <RelayUserStatsModal />
    <RelayUserQuotaModal />
    <TriggerTestModal />
    <AutoTriggerModal />
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, nextTick, watch } from 'vue';
import { useRoute } from 'vue-router';
import Dashboard from './views/Dashboard.vue';
import Accounts from './views/Accounts.vue';
import UsageDetails from './views/UsageDetails.vue';
import OTP from './views/OTP.vue';
import Packets from './views/Packets.vue';
import Settings from './views/Settings.vue';
import DetailsModal from './components/modals/DetailsModal.vue';
import PricingModal from './components/modals/PricingModal.vue';
import RetryErrorLogsModal from './components/modals/RetryErrorLogsModal.vue';
import UpdateModal from './components/modals/UpdateModal.vue';
import SessionBindingsModal from './components/modals/SessionBindingsModal.vue';
import ExportPacketsModal from './components/modals/ExportPacketsModal.vue';
import RemoteModal from './components/modals/RemoteModal.vue';
import RemoteKeysModal from './components/modals/RemoteKeysModal.vue';
import RelayUserModal from './components/modals/RelayUserModal.vue';
import RelayUserStatsModal from './components/modals/RelayUserStatsModal.vue';
import RelayUserQuotaModal from './components/modals/RelayUserQuotaModal.vue';
import TriggerTestModal from './components/modals/TriggerTestModal.vue';
import AutoTriggerModal from './components/modals/AutoTriggerModal.vue';
import { initRemoteEvents } from './ui/remoteController';
import { setLanguage, switchView, initDashboardEvents } from './ui/dashboard';
import { ipcRenderer } from './shared/ipc';
import { initAccountsEvents } from './ui/accountsController';
import { initPricingEvents } from './ui/pricingController';
import { initPacketsEvents } from './ui/packetsController';
import { initSettings } from './ui/settingsController';
import { initChartFilters } from './ui/chartRenderer';
import { init as initUsageDetails } from './ui/usageDetails';
import { initMigrationEvents } from './ui/migrationController';
import { initUpdaterEvents } from './ui/updaterController';
import { initRetryErrorLogsEvents } from './ui/retryErrorLogsController';
import { initOtpEvents } from './ui/otpController';
import { initRelayEvents } from './ui/relayController';

const route = useRoute();

// Responsive and dynamic layout states
const isCollapsed = ref(false);
const isDropdownOpen = ref(false);
const currentLang = ref('zh');

const headerRef = ref<HTMLElement | null>(null);
const leftSection = ref<HTMLElement | null>(null);
const navSection = ref<HTMLElement | null>(null);
const rightSection = ref<HTMLElement | null>(null);
const dropdownRef = ref<HTMLElement | null>(null);

let lastRequiredWidth = 1280;
let lastLeftHtml = '';
let lastRightHtml = '';
let layoutTimer: any = null;

const toggleDropdown = () => {
  isDropdownOpen.value = !isDropdownOpen.value;
};

const closeDropdown = (e: MouseEvent) => {
  if (dropdownRef.value && !dropdownRef.value.contains(e.target as Node)) {
    isDropdownOpen.value = false;
  }
};

const i18nLabels: Record<string, Record<string, string>> = {
  zh: {
    title: "控制台",
    navAccounts: "账号池",
    navUsage: "使用详情",
    navOtp: "2FA验证码",
    navPackets: "抓包分析",
    navSettings: "设置",
    menu: "菜单"
  },
  en: {
    title: "Console",
    navAccounts: "Accounts",
    navUsage: "Usage Details",
    navOtp: "2FA Auth",
    navPackets: "Packets",
    navSettings: "Settings",
    menu: "Menu"
  }
};

const currentActiveLabel = computed(() => {
  const path = route.path;
  const lang = currentLang.value;
  const dict = i18nLabels[lang] || i18nLabels.zh;
  
  if (path === '/' || path === '/dashboard') return dict.title;
  if (path === '/accounts') return dict.navAccounts;
  if (path === '/usage') return dict.navUsage;
  if (path === '/otp') return dict.navOtp;
  if (path === '/packets') return dict.navPackets;
  if (path === '/settings') return dict.navSettings;
  return dict.menu;
});

const forceMeasure = async () => {
  const originalState = isCollapsed.value;
  isCollapsed.value = false;
  
  await nextTick();
  
  if (headerRef.value && leftSection.value && navSection.value && rightSection.value) {
    const leftWidth = leftSection.value.scrollWidth;
    const navWidth = navSection.value.scrollWidth;
    const rightWidth = rightSection.value.scrollWidth;
    
    lastRequiredWidth = leftWidth + navWidth + rightWidth + 50; // 50px safety buffer
    
    const headerWidth = headerRef.value.clientWidth;
    if (headerWidth < lastRequiredWidth) {
      isCollapsed.value = true;
    } else {
      isCollapsed.value = false;
    }
  } else {
    isCollapsed.value = originalState;
  }
};

const updateResponsiveLayout = () => {
  if (!headerRef.value) return;
  const headerWidth = headerRef.value.clientWidth;

  if (!isCollapsed.value) {
    const leftWidth = leftSection.value ? leftSection.value.scrollWidth : 0;
    const navWidth = navSection.value ? navSection.value.scrollWidth : 0;
    const rightWidth = rightSection.value ? rightSection.value.scrollWidth : 0;
    
    const required = leftWidth + navWidth + rightWidth + 50;
    if (required > 100) {
      lastRequiredWidth = required;
    }

    if (headerWidth < lastRequiredWidth) {
      isCollapsed.value = true;
    }
  } else {
    if (headerWidth >= lastRequiredWidth) {
      isCollapsed.value = false;
    }
  }
};

const checkDOMChanges = () => {
  const leftHtml = leftSection.value ? leftSection.value.innerHTML : '';
  const rightHtml = rightSection.value ? rightSection.value.innerHTML : '';
  
  if (leftHtml !== lastLeftHtml || rightHtml !== lastRightHtml) {
    lastLeftHtml = leftHtml;
    lastRightHtml = rightHtml;
    forceMeasure();
  } else {
    updateResponsiveLayout();
  }
};

watch(() => route.path, async (newPath) => {
  const viewName = newPath === '/' ? 'dashboard' : newPath.substring(1);
  switchView(viewName);
  // Auto-close dropdown on route change
  isDropdownOpen.value = false;
  
  await nextTick();
  setLanguage(currentLang.value);
});

watch(currentLang, async (newLang) => {
  await nextTick();
  setLanguage(newLang);
  ipcRenderer.send('settings:language-changed', newLang);
  forceMeasure();
});

onMounted(() => {
  document.addEventListener('click', closeDropdown);
  window.addEventListener('resize', updateResponsiveLayout);

  setTimeout(() => {
    initDashboardEvents();
    initAccountsEvents();
    initPricingEvents();
    initPacketsEvents();
    initSettings();
    initChartFilters();
    initUsageDetails();
    initMigrationEvents();
    initUpdaterEvents();
    initRetryErrorLogsEvents();
    initOtpEvents();
    initRelayEvents();
    initRemoteEvents();
    
    (window as any).refreshLanguageFromBackend = () => {
      const savedLang = ipcRenderer.sendSync('settings:get-language') || 'zh';
      setLanguage(savedLang);
      if (currentLang.value !== savedLang) {
        currentLang.value = savedLang;
      }
    };

    (window as any).refreshLanguageFromBackend();

    // Manually trigger initial switchView to populate settings etc.
    const initialView = route.path === '/' ? 'dashboard' : route.path.substring(1);
    switchView(initialView);

    // Flush any pending listeners after all components are mounted
    if ((window as any).initWailsReady) {
        (window as any).initWailsReady();
    }
    
    // Set initial measured widths and start DOM change checks
    setTimeout(() => {
      forceMeasure();
      layoutTimer = setInterval(checkDOMChanges, 500);
    }, 150);
  }, 100);
});

onUnmounted(() => {
  document.removeEventListener('click', closeDropdown);
  window.removeEventListener('resize', updateResponsiveLayout);
  if (layoutTimer) {
    clearInterval(layoutTimer);
  }
});
</script>

<style scoped>
.remote-collapsed #remoteStatusText {
  display: none !important;
}
</style>
