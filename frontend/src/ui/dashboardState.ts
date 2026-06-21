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

    // Pagination
    currentPage: number;
    itemsPerPage: number;

    // Pricing Config Cache
    pricingConfig: { [modelName: string]: any };

    // UI Interactive States
    isLoadingAuth: boolean;
    isRefreshingAll: boolean;
    isRefreshingAggregate: boolean;

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

    // Pagination
    currentPage: 1,
    itemsPerPage: 8,

    // Pricing Config Cache
    pricingConfig: {},

    // UI Interactive States
    isLoadingAuth: false,
    isRefreshingAll: false,
    isRefreshingAggregate: false,

    // Shared Callbacks for Cross-Module Communication
    callbacks: {
        renderLogsTable: () => {},
        renderAccounts: () => {},
        updateAggregateQuotaUI: () => {},
        fetchPricing: () => {},
        setLanguage: () => {},
        updateStatusLabel: () => {},
        refreshPacketsList: () => {},
        updateAnalyzeAccountSelect: () => {}
    }
};

export default state;
