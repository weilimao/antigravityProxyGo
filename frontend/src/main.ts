import './style.css';
import './app.css';
import './ui/dashboard.css';

import { initDashboardEvents, switchView, switchTab, setLanguage } from './ui/dashboard';
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

// Mount global interaction functions requested by DOM inline click events
(window as any).switchView = switchView;
(window as any).switchTab = switchTab;

function initApp() {
    console.log('[Main] Initializing all frontend controllers...');
    try {
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

        // Set default language
        setLanguage('zh');

        // Flush any pending listeners
        if ((window as any).initWailsReady) {
            (window as any).initWailsReady();
        }
    } catch (err) {
        console.error('[Main] Error during application initialization:', err);
    }
}

if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', initApp);
} else {
    initApp();
}
