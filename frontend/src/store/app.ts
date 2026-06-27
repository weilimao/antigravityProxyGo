import { defineStore } from 'pinia';

export const useAppStore = defineStore('app', {
  state: () => ({
    currentLanguage: 'zh',
    currentTheme: 'light',
    activeTab: 'logs',
    trendsData: [] as any[],
    allRequests: [] as any[],
    searchQuery: '',
    currentRange: '24h',
    customStartDate: null as number | null,
    customEndDate: null as number | null,
    quotaCache: {} as Record<string, any[]>,
    quotaLoadingState: {} as Record<string, 'loading' | 'success' | 'error'>,
    currentAccountsList: [] as any[],
    currentActiveChannel: 'antigravity',
    lastBackendData: null as any,
    currentViewTab: '',
    memoryHistory: [] as number[],
    maxMemoryHistoryPoints: 25,
    activeView: 'dashboard',
    statsData: null as any,
    usageData: null as any,

    // Pagination
    currentPage: 1,
    itemsPerPage: 8,

    // Pricing Config Cache
    pricingConfig: {} as Record<string, any>,

    // UI Interactive States
    isLoadingAuth: false,
    isRefreshingAll: false,
    isRefreshingAggregate: false,

    // Remote Mode State
    isRemoteMode: false,
    remoteHost: '',
    remotePort: '',
    remoteUserKey: '',
    remoteStats: null as any,
  }),
  actions: {
    setLanguage(lang: string) {
      this.currentLanguage = lang;
    },
    setTheme(theme: string) {
      this.currentTheme = theme;
    }
  }
});
