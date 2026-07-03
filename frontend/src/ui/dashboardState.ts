/**
 * Antigravity Proxy - Shared Dashboard State
 */

export interface DashboardState {
    currentLanguage: string;
    currentTheme: string;
    activeTab: string;
    trendsData: any[];
    allRequests: any[];
    searchQuery: string;
    currentRange: string;
    customStartDate: number | null;
    customEndDate: number | null;
    quotaCache: { [accountId: string]: any[] };
    quotaLoadingState: { [accountId: string]: 'loading' | 'success' | 'error' };
    currentAccountsList: any[];
    currentActiveChannel: string;
    lastBackendData: any;
    currentViewTab: string;
    memoryHistory: number[];
    maxMemoryHistoryPoints: number;
    activeView: string;
    statsData: any | null;
    usageData: any | null;

    // Pagination
    currentPage: number;
    itemsPerPage: number;

    // Account Pool Specific State (Filters & Pagination)
    accountSearchQuery: string;
    accountStatusFilter: string;
    accountTierFilter: string;
    accountCurrentPage: number;
    accountItemsPerPage: number;
    selectedAccountIds: string[];

    // Pricing Config Cache
    pricingConfig: { [modelName: string]: any };

    // UI Interactive States
    isLoadingAuth: boolean;
    isRefreshingAll: boolean;
    isRefreshingAggregate: boolean;

    // Remote Mode State
    isRemoteMode: boolean;
    remoteHost: string;
    remotePort: string;
    remotePath: string;
    remoteUserKey: string;
    remoteToken: string;
    remoteStats: any | null;

    // Shared Callbacks for Cross-Module Communication
    callbacks: {
        renderLogsTable: () => void;
        renderAccounts: (accounts: any[]) => void;
        updateAggregateQuotaUI: () => void;
        fetchPricing: () => void;
        setLanguage: (lang: string) => void;
        updateStatusLabel: () => void;
        refreshPacketsList: () => void;
        updateAnalyzeAccountSelect: () => void;
        updateRemoteStatus: () => void;
    };
}

const state: DashboardState = {
    // Basic State Variables
    currentLanguage: 'zh',
    currentTheme: 'light',
    activeTab: 'logs',
    trendsData: [],
    allRequests: [],
    searchQuery: '',
    currentRange: '24h',
    customStartDate: null,
    customEndDate: null,
    quotaCache: {},
    quotaLoadingState: {},
    currentAccountsList: [],
    currentActiveChannel: 'antigravity',
    lastBackendData: null,
    currentViewTab: '',
    memoryHistory: [],
    maxMemoryHistoryPoints: 25,
    activeView: 'dashboard',
    statsData: null,
    usageData: null,

    // Pagination
    currentPage: 1,
    itemsPerPage: 8,

    // Account Pool Specific State (Filters & Pagination)
    accountSearchQuery: '',
    accountStatusFilter: 'all',
    accountTierFilter: 'all',
    accountCurrentPage: 1,
    accountItemsPerPage: 10,
    selectedAccountIds: [],

    // Pricing Config Cache
    pricingConfig: {},

    // UI Interactive States
    isLoadingAuth: false,
    isRefreshingAll: false,
    isRefreshingAggregate: false,

    // Remote Mode State
    isRemoteMode: false,
    remoteHost: '',
    remotePort: '',
    remotePath: '',
    remoteUserKey: '',
    remoteToken: '',
    remoteStats: null,

    // Shared Callbacks for Cross-Module Communication
    callbacks: {
        renderLogsTable: () => {},
        renderAccounts: (accounts?: any) => {},
        updateAggregateQuotaUI: () => {},
        fetchPricing: () => {},
        setLanguage: () => {},
        updateStatusLabel: () => {},
        refreshPacketsList: () => {},
        updateAnalyzeAccountSelect: () => {},
        updateRemoteStatus: () => {}
    }
};

export default state;
