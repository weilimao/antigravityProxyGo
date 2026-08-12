"""i18n 拆分辅助脚本：按命名空间归类 key，生成各命名空间 ts 文件。
分类规则基于 key 前缀（zh 是权威顺序）。"""
import re
import json
from pathlib import Path

src = open('src/shared/i18n.ts', encoding='utf-8').read().splitlines()


def collect(lo, hi):
    keys = []
    for i in range(lo - 1, hi):
        s = src[i].strip()
        if not s or s.startswith('//'):
            continue
        m = re.match(r'^([a-zA-Z_][a-zA-Z0-9_]*):[ \t]*(.*)$', s)
        if m:
            keys.append(m.group(1))
    return keys


zhk = collect(17, 858)
enk = collect(860, 1705)
print("zh keys:", len(zhk), "en keys:", len(enk))

# 按命名空间归类的 key 集合（显式列举，避免前缀误判）
NS = {
    'common': set(),
    'dashboard': set(),
    'accounts': set(),
    'nvidia': set(),
    'other': set(),
    'relay': set(),
    'settings': set(),
    'otp': set(),
    'packets': set(),
    'pricing': set(),
    'autoTrigger': set(),
    'usage': set(),
    'help': set(),
    'requestlog': set(),
}

# 手工分组（按 key 语义，明确而非靠前缀猜）
COMMON = {
    'title', 'interceptMode', 'statusOn', 'statusOff', 'caStatus', 'certTrusted',
    'certUntrusted', 'certChecking', 'certProcessing', 'installCert', 'uninstallCert',
    'navAccounts', 'navUsage', 'navOtp', 'navPackets', 'navSettings', 'input', 'output',
    'btnClose', 'btnChangeDir', 'btnCancel', 'btnApply', 'btnCopy', 'btnEdit',
    'btnRemove', 'btnAdd', 'btnSave', 'btnConfirm', 'btnConfirmText', 'btnCancel',
    'close', 'confirm', 'cancel', 'sysMemoryLabel', 'sysMemoryTitle', 'sysMemoryDescPart',
    'goHeapLabel', 'goHeapDescPart', 'cpuUsageLabel', 'optionalPlaceholder',
    'btnManualRefresh', 'btnExport', 'btnImport', 'btnSelectAll', 'btnClearAll',
    'btnCancelEditTask', 'btnSaveTask', 'colAction', 'colName',
}
DASHBOARD = {
    'totalRequests', 'totalRetries', 'totalErrors', 'successRate', 'totalTokens',
    'cachedTokens', 'cacheHitRate', 'poolFilterLabel', 'memoryUsage', 'sysProcess',
    'savedCost', 'totalCost', 'usageTrend', 'trendScopeAll', 'trendScopeNvidia',
    'legendRequests', 'legendCost', 'legendCached', 'legendInput', 'legendOutput',
    'tooltipRequestsLabel', 'tooltipCostLabel', 'logBufferTitle', 'consoleFloatTitle',
    'consoleDockTitle', 'range24h', 'rangeToday', 'range3d', 'range7d', 'range30d',
    'rangeFilter', 'filterPanelTitle', 'filterStart', 'filterEnd',
}
SETTING_KEYS = {
    'settingsTitle', 'settingsDesc', 'settingsTabGeneral', 'settingsTabNvidia',
    'settingsTabNetwork', 'settingsTabAbout', 'dataDirLabel', 'dataDirTip',
    'currentDirLabel', 'migrationStatusTitle', 'migrationStatusSuccess',
    'migrationStatusFailed', 'migrationStatusProcessing', 'updateTitle', 'updateTip',
    'currentVersionLabel', 'btnCheckUpdate', 'checkingUpdates', 'updateAvailable',
    'alreadyLatest', 'btnUpdateNow', 'btnUpdateLater', 'btnRestartNow',
    'btnLaterRestart', 'downloadingUpdate', 'downloadComplete', 'updateFailed',
    'logSettingTitle', 'logSettingTip', 'enableLogLabel', 'enableLogDesc',
    'startupSettingTitle', 'startupSettingTip', 'enableAutoStartLabel',
    'enableAutoStartDesc', 'enableSilentStartLabel', 'enableSilentStartDesc',
    'proxySettingTitle', 'proxySettingTip', 'maxRetriesLabel', 'maxRetriesDesc',
    'maxRetryDelayLabel', 'maxRetryDelayDesc', 'maxRequestBodyLabel',
    'maxRequestBodyDesc', 'requestTimeoutLabel', 'requestTimeoutDesc',
    'packetSettingTitle', 'packetSettingTip', 'enablePacketCaptureLabel',
    'enablePacketCaptureDesc', 'fallbackSettingTitle', 'fallbackSettingTip',
    'customSocks5EnabledLabel', 'customSocks5EnabledDesc', 'customSocks5AddressLabel',
    'customSocks5AddressPlaceholder', 'customSocks5UsernameLabel',
    'customSocks5PasswordLabel', 'fallbackProxyEnabledLabel', 'fallbackProxyEnabledDesc',
    'fallbackProxyAddressLabel', 'fallbackProxyAddressPlaceholder',
    'fallbackProxyUsernameLabel', 'fallbackProxyPasswordLabel', 'fallbackPortsLabel',
    'fallbackPortsDesc', 'fallbackPortsPlaceholder', 'promptPrefixTitle',
    'promptPrefixTip', 'promptPrefixLabel', 'promptPrefixPlaceholder',
    'customModelOverrideTitle', 'customModelOverrideTip', 'enableCustomModelOverrideLabel',
    'enableCustomModelOverrideDesc', 'customModelOverrideIDLabel',
    'customModelOverrideIDPlaceholder', 'bypassOverridePrefixesLabel',
    'bypassOverridePrefixesTip', 'bypassOverridePrefixesPlaceholder',
    'enableCustomThinkingOverrideLabel', 'enableCustomThinkingOverrideDesc',
    'customThinkingSupportsLabel', 'customThinkingBudgetLabel',
    'customThinkingMinBudgetLabel', 'customMaxOutputTokensLabel',
    'sessionOptimizationTitle', 'sessionOptimizationTip', 'enableCustomCompressionLabel',
    'enableCustomCompressionDesc', 'maxTokensThresholdLabel', 'keepRecentTurnsLabel',
    'summaryModelLabel', 'ocrModelLabel', 'ocrModelDesc', 'ocrCardTitle', 'ocrCardTip',
    'netStatusTitle', 'netStatusFallbackLabel', 'netStatusDetecting',
    'netStatusCustomSocksLabel', 'netStatusFallbackProxyLabel', 'netStatusDisabled',
    'netStatusPeriodLabel', 'netStatusPeriodValue', 'netLogsTitle', 'netLogsDesc',
    'netLogTime', 'netLogTarget', 'netLogRoute', 'netLogDuration', 'netLogStatus',
    'netLogsEmpty', 'btnReleaseNotes', 'releaseNotesLabel', 'aboutDesc',
    'aboutAuthorLabel', 'aboutRepoLabel', 'aboutSponsorLabel', 'aboutSponsorDesc',
    'aboutFeedbackLabel', 'aboutFeedbackDesc',
}
RELAY_KEYS = {
    'settingsTabRelay', 'relayServerTitle', 'relayEnableLabel', 'relayEnableDesc',
    'relayUsersTitle', 'relayAddUser', 'relayAddUserTitle', 'relayUserKeyPlaceholder',
    'relayUserPasswordPlaceholder', 'relayUserRemarkPlaceholder', 'relayBtnConfirm',
    'relayBtnCancel', 'relaySubTabUsers', 'relaySubTabPackages', 'relaySubTabSecurity',
    'relaySubTabModelMapping', 'relaySubTabTutorial', 'relaySecurityTitle',
    'relaySSRFLabel', 'relaySSRFDesc', 'relayPortBlockLabel', 'relayPortBlockDesc',
    'relayDomainFilterLabel', 'relayDomainFilterDesc', 'relayDomainWhitelistLabel',
    'relayBtnSaveSecurity', 'relayUserSearchPlaceholder', 'relayUserFilterAll',
    'relayUserFilterUnlimited', 'relayUserFilterCustom', 'relayPackagesTitle',
    'relayNewPackage', 'relayModelMappingTitle', 'relayAddMapping',
    'relayMappingClientModel', 'relayMappingTargetModel', 'relayMappingMultimodal',
    'relayMappingExpose', 'relaySaveMapping', 'relaySaving', 'relaySaveSuccess',
    'relaySaveFailed', 'relayFilterAllPackages', 'relayFilterUnlimited',
    'relayFilterCustom', 'relayTokenHundredMillion', 'relayTokenTenThousand',
    'relayNoLimit', 'relayNoPermission', 'relayHour', 'relayDay', 'relayLifetime',
    'relayExpiresAt', 'relayExpiredOn', 'relayDaysUnit', 'relayMonthsUnit',
    'relayYearsUnit', 'relayRateLimitLabel', 'relayTemplateListEmpty', 'relayValidity',
    'relayQuotaGeminiTitle', 'relayQuotaClaudeTitle', 'relayRateLimitText',
    'relayClickToModify', 'relayUserCountText', 'relayUserPaginationText',
    'relayUserListEmpty', 'relayNoRemark', 'relayCreatedAt', 'relayDeleteUserConfirm',
    'relayQuotaModalTitleUser', 'relayQuotaModalTitleEditTemplate',
    'relayQuotaModalTitleCreateTemplate', 'relayDeleteTemplateConfirm',
    'relaySaveQuotaFailed', 'relayQuotaResetLimitLabel', 'relayStatsEmpty',
    'relayStatsTotalRequests', 'relayStatsTotalCost', 'relayStatsTotalTokens',
    'relayStatsCacheHit', 'relayStatsInputTokens', 'relayStatsOutputTokens',
    'relayRemain', 'relayExpectedRefresh', 'relayExpectedRefreshDate',
    'relayUserUsageTracking', 'relayStatsModelTitle', 'relayAlertEmptyKeyPassword',
    'relayAddUserFailed', 'userStatsTitle', 'manageApiKeys', 'manageKeysTitle',
    'newKeyPlaceholder', 'btnCreateKey', 'colApiKey', 'colGeminiUsage',
    'colClaudeUsage', 'modifyKeyQuota', 'geminiQuotaLabel', 'quotaPlaceholder',
    'claudeQuotaLabel', 'relayQuotaPackageNameLabel', 'relayQuotaPackageNamePlaceholder',
    'relayQuotaValidLabel', 'relayQuotaValidPlaceholder', 'relayMonth', 'relayYear',
    'relayQuotaRateLimitLabel', 'relayQuotaRateLimitPlaceholder',
    'relayQuotaQuickSetupLabel', 'relayQuotaFixedTokensLabel',
    'relayQuotaFixedTokensPlaceholder', 'relayQuotaHourlyTokensLabel',
    'relayQuotaHourlyHoursPlaceholder', 'relayQuotaHourlyTokensPlaceholder',
    'relayQuotaDailyTokensLabel', 'relayQuotaDailyDaysPlaceholder',
    'relayQuotaDailyTokensPlaceholder', 'relayDomainWhitelistPlaceholder',
    'relayMappingInjectKwargs',  # 可能不存在,保留保护
    'tutorialTitle', 'tutorialIntro', 'tutorialStep1Title', 'tutorialStep1Desc',
    'tutorialStep2Title', 'tutorialStep2Desc', 'tutorialStep3Title',
    'tutorialStep3Desc', 'tutorialStep4Title', 'tutorialStep4Desc',
    'tutorialStep4Note', 'tutorialStep5Title', 'tutorialStep5Desc',
    'tutorialStep6Title', 'tutorialStep6Desc', 'tutorialStep7Title',
    'tutorialStep7Desc', 'tutorialStep7Note', 'tutorialRouteTableTitle',
    'tutorialSecurityTitle', 'tutorialSecurityDesc', 'layoutGridTitle',
    'layoutListTitle', 'cols3', 'cols4', 'cols5',
}
NVIDIA_KEYS = {
    'nvidiaItemTitle', 'nvidiaItemDesc', 'nvidiaAddModalTitle', 'nvidiaFieldBaseUrl',
    'nvidiaFieldBaseUrlPlaceholder', 'nvidiaFieldApiKey', 'nvidiaFieldApiKeyPlaceholder',
    'nvidiaFieldLabel', 'nvidiaFieldLabelPlaceholder', 'nvidiaModelMappingTitle',
    'nvidiaModelMappingDesc', 'nvidiaFieldDefaultModel', 'nvidiaModalCancel',
    'nvidiaModalSave', 'nvidiaQuotaDesc', 'nvidiaQuotaFail', 'nvidiaAccountAvailable',
    'nvidiaAccountDisabled', 'nvidiaCooldownBubble', 'nvidiaCooldownSeconds',
    'nvidiaCooldownMinutes', 'nvidiaCooldownHours', 'nvidiaCooldownExpired',
    'nvidiaPreferredModelsTitle', 'nvidiaPreferredModelsBtn', 'nvidiaPreferredModelsFetch',
    'nvidiaPreferredModelsSave', 'nvidiaPreferredModelsSearch',
    'nvidiaPreferredModelsSearchPlaceholder', 'nvidiaPreferredModelsSelectAll',
    'nvidiaPreferredModelsEmpty', 'nvidiaPreferredModelsCountSaved',
    'nvidiaPreferredModelsUnit', 'nvidiaPreferredModelsSourceCache',
    'nvidiaPreferredModelsSourceRemote', 'nvidiaPreferredModelsNoAccountError',
    'nvidiaPreferredModalCancel', 'nvidiaPreferredSrcLocal', 'nvidiaPreferredSrcRemote',
    'nvidiaPreferredColSelected', 'nvidiaPreferredColSource',
    'nvidiaPreferredEmptyLeft', 'nvidiaPreferredEmptyRight',
    'nvidiaPreferredFetchUpstream', 'nvidiaPreferredMoveLeft',
    'nvidiaPreferredMoveRight', 'nvidiaPreferredMoveAllLeft',
    'nvidiaPreferredMoveAllRight', 'nvidiaPreferredCacheHint', 'nvidiaPool',
    'nvidiaPoolLoadBalance', 'nvidiaReasoningAsTextTitle', 'nvidiaReasoningAsTextTip',
    'nvidiaFallbackProxyTitle', 'nvidiaFallbackProxyTip', 'reasoningAsTextLabel',
    'reasoningAsTextDesc', 'enableThinkingModeLabel', 'enableThinkingModeDesc',
    'thinkingModeCardTip',
}
OTHER_KEYS_ = {
    'otherItemTitle', 'otherItemDesc', 'otherAddModalTitle', 'otherFieldGroupId',
    'otherFieldGroupIdPlaceholder', 'otherFieldGroupName',
    'otherFieldGroupNamePlaceholder', 'otherFieldFormats', 'otherFormatOpenai',
    'otherFormatAnthropic', 'otherFetchModels', 'otherModalCancel', 'otherModalSave',
    'otherNoGroups', 'otherGroupBadge', 'otherAllGroups',
    'otherLBModeSelectPlaceholder', 'otherGroupSelectTitle',
    'otherGroupSelectPlaceholder', 'otherFetchModelsEmpty',
    'otherManualInputAllowed', 'lbRoundRobin', 'lbSticky', 'maxConcurrencyLabel',
    'maxConcurrencyTip', 'otherPool', 'otherModels',
}
ACCOUNTS_KEYS = {
    'accountsTitle', 'accountsDesc', 'antigravityOfficial', 'gcpProjectApi',
    'authOfficialPlugin', 'useGcpProjectTitle', 'useGcpProjectDesc',
    'antigravityRecommended', 'btnAddAccount', 'accountsSearchPlaceholder',
    'statusAll', 'statusEnabledOnly', 'statusDisabledOnly', 'statusCoolingOnly',
    'statusActive', 'statusEnabledAccount', 'statusDisabledAccount',
    'tierAll', 'btnShowSessionBindingsTitle', 'btnShowSessionBindingsLabel',
    'btnClearSessionsTitle', 'btnClearSessionsLabel', 'btnRefreshAllQuotaTitle',
    'btnRefreshAllQuotaLabel', 'btnManageAutoTriggerTitle',
    'btnManageAutoTriggerLabel', 'btnTriggerTest', 'accountsEmptyLabel',
    'btnPrevPage', 'btnNextPage', 'btnRefreshAllTitle', 'btnRefreshAll',
    'exportAccountTitle', 'removeAccountConfirm', 'showingAccountsRange',
    'poolLoadBalance', 'selectedAccountsCount', 'resetStatus', 'resetTimeMinutes',
    'resetTimeHours', 'resetTimeDays', 'absoluteResetTime', 'cooldownText',
    'quotaNoLimit', 'noQuotaData', 'addedAtLabel', 'aiCreditTitle', 'creditNotLoaded',
    'deductExcessCredit', 'remainingQuota', 'refreshQuota',
    'authModalTitle', 'authStep1Text', 'authStep2Text', 'gettingAuthLink',
    'placeholderAuthCode', 'btnOpenAuthBrowser', 'getAuthLinkFailed',
    'getAuthLinkError', 'authLinkCopied', 'authLinkNotReady', 'pleaseInputAuthCode',
    'btnVerifying', 'verificationFailed', 'selectGcpProjectLabel', 'selectProjectTip',
    'gcpProjectListFailed', 'inputGcpProjectIdLabel', 'gcpProjectIdTip',
    'bindGcpTitle', 'authorizedEmailLabel', 'btnBindAndLogin',
    'pleaseInputProjectId', 'btnBinding', 'saveFailed', 'projectLoadBalancing',
    'btnLoggingIn', 'btnCancelLogin', 'sessionBindingsLoading',
    'sessionBindingsTotal', 'sessionBindingsEmpty', 'btnUnbind',
    'btnUnbindProcessing', 'unbindFailed', 'unbindRequestFailed',
    'loadBindingsFailed', 'btnClearAllBindingsConfirm',
    'btnClearAllBindingsProcessing', 'btnClearAllBindings', 'sessionBindingsTitle',
    'tipLabel', 'sessionBindingsTip', 'colSessionKey', 'colBoundEmail',
    'colLastActive', 'remoteGeminiTotal', 'remoteGeminiHourly',
    'remoteGeminiDaily', 'remoteClaudeTotal', 'remoteClaudeHourly',
    'remoteClaudeDaily', 'remoteConnecting', 'remoteConnect', 'remoteDisconnect',
    'remoteEnable', 'remoteDisable', 'remoteModalTitle', 'remoteHostPlaceholder',
    'remotePathPlaceholder', 'remoteKeyPlaceholder', 'remotePasswordPlaceholder',
    'remoteBtnTest', 'remoteBtnLogin', 'remoteBtnCancel',
}
OTP_KEYS = {
    'otpTitle', 'otpDesc', 'btnAddOtpAccount', 'otpCountdownLabel', 'otpSeconds',
    'otpInstantQuery', 'otpInstantPlaceholder', 'otpInvalidKey', 'otpCopyTip',
    'otpListHeader', 'otpSearchPlaceholder', 'otpColEmail', 'otpColStatus',
    'otpColCode', 'otpColAction', 'otpEmpty', 'otp_unknownError',
    'otp_clearConfirm', 'otp_clearFailed', 'otp_clearError',
    'otp_invalidSecretFormat', 'otp_calculationFailed', 'otp_countBadgeText',
    'otp_showingEmpty', 'otp_showingEntries', 'otp_protected',
    'otp_notConfigured', 'otp_invalidOrError', 'otp_copyTooltip', 'otp_btnEdit',
    'otp_btnClear', 'otp_btnConfig', 'otp_editModalTitle', 'otp_configModalTitle',
    'otp_emailLabel', 'otp_inputSecretLabel', 'otp_inputSecretPlaceholder',
    'otp_modalTips', 'otp_btnCancel', 'otp_btnSaveAndVerify', 'otp_requestError',
    'otp_addModalTitle', 'otp_emailNameLabel', 'otp_emailPlaceholder', 'otp_btnAdd',
    'otp_emailRequired', 'otp_addFailed',
}
PACKETS_KEYS = {
    'packetsTitle', 'packetsDesc', 'analyzeAccount', 'selectAnalyzeAccount',
    'btnAiAnalyze', 'btnDownloadDoc', 'btnClearPackets', 'packetListHeader',
    'packetFilterAll', 'packetFilterUnknown', 'packetEmptyText', 'packetDetailHeader',
    'btnExportMd', 'packetDetailTip', 'packetUrlLabel', 'reqPacketLabel',
    'btnCopyBody', 'respPacketLabel', 'aiDocPreview', 'btnCopyDoc',
    'aiAnalyzingTitle', 'aiAnalyzingDesc', 'aiConnecting',
    'packetSelectAccountPlaceholder', 'packetLogDocTitle', 'packetLogDocTime',
    'packetLogDocTotal', 'packetLogDocOverview', 'packetLogDocSource',
    'packetLogDocStatus', 'packetLogDocCaptured', 'packetLogDocDetails',
    'packetLogDocHost', 'packetLogDocNoBody', 'packetLogDocNoResBody',
    'exportPacketsTitle', 'exportPacketsTip', 'exportPacketsSelectLabel',
    'btnExportPacketsConfirm',
}
PRICING_KEYS = {
    'tabPricing', 'colPath', 'tokenConsumption', 'colCacheTitle', 'colActions',
    'pricingConfigTitle', 'btnResetPricing', 'btnAddPricing', 'colModelPattern',
    'colInputPrice', 'colOutputPrice', 'colCachedPrice', 'pricingModalTitle',
    'pricingModelPatternLabel', 'pricingInputValLabel', 'pricingOutputValLabel',
    'pricingCachedValLabel',
}
AUTOTRIGGER_KEYS = {
    'autoTriggerModalTitle', 'autoTriggerDesc', 'btnViewHistory', 'btnConfigList',
    'btnClearHistory', 'thTriggerTime', 'loadingHistory', 'noHistoryRecords',
    'confirmClearHistory', 'clearHistorySuccess', 'btnCreateNewTask', 'thTaskName',
    'thTriggerType', 'thAccountCount', 'thModelCount', 'thEnabledStatus',
    'thAction', 'autoTriggerColAction', 'loadingTasks', 'labelTaskName',
    'placeholderTaskName', 'labelTaskPrompt', 'placeholderTaskPrompt',
    'labelTriggerType', 'optionTimer', 'optionQuotaRefreshed', 'labelInterval',
    'labelSelectAccounts', 'btnSelectAll', 'btnClearAll', 'labelSelectModels',
    'btnCancelEditTask', 'btnSaveTask', 'triggerTestModalTitle',
    'labelTriggerPrompt', 'placeholderTriggerPrompt', 'labelTriggerModels',
    'labelLiveLogs', 'waitingConfigTrigger', 'labelTriggerResults',
    'thResultAccount', 'thResultModel', 'thResultStatus', 'thResultDetail',
    'btnStartTrigger',
}
USAGE_KEYS = {
    'usage_title', 'usage_desc', 'usage_noModelUsage', 'usage_calls', 'usage_cache',
    'usage_hitRate', 'usage_inputCost', 'usage_outputCost', 'usage_cacheCost',
    'usage_totalCost', 'usage_model', 'usage_input', 'usage_output',
    'usage_noAccountUsage', 'usage_showingEntries', 'usage_searchPlaceholder',
    'usage_accounts', 'usage_callsCount', 'usage_totalTokens',
    'usage_noMatchingData', 'usage_tabAll', 'usage_tabAntigravity',
    'usage_tabProject', 'usage_tabNvidia', 'usage_tabGrok', 'usage_tabDirect', 'usage_tabEmpty',
    'usage_prevPage', 'usage_nextPage', 'summaryTotalCostToday',
    'summaryTotalCost24h', 'summaryTotalCost3d', 'summaryTotalCost7d',
    'summaryTotalCost30d', 'summaryTotalCostCustom', 'summaryInputCost',
    'summaryOutputCost', 'summaryCachedCost', 'summaryTotalTokensToday',
    'summaryTotalTokens24h', 'summaryTotalTokens3d', 'summaryTotalTokens7d',
    'summaryTotalTokens30d', 'summaryTotalTokensCustom',
    'summaryTotalRequestsToday', 'summaryTotalRequests24h',
    'summaryTotalRequests3d', 'summaryTotalRequests7d',
    'summaryTotalRequests30d', 'summaryTotalRequestsCustom',
    'summaryInputTokens', 'summaryOutputTokens', 'summaryCachedTokens',
}
HELP_KEYS = {
    'settingsTabHelp', 'help_ide_update_title', 'help_ide_update_tag',
    'help_ide_update_desc', 'help_three_step_title', 'help_step1_title',
    'help_step1_desc', 'help_step2_title', 'help_step2_desc', 'help_step3_title',
    'help_step3_desc', 'help_cert_title', 'help_cert_desc',
    'help_cert_install_title', 'help_cert_install_desc',
    'help_cert_uninstall_title', 'help_cert_uninstall_desc', 'help_switch_title',
    'help_switch_desc', 'help_switch_on_tag', 'help_switch_on_title',
    'help_switch_on_desc', 'help_switch_off_tag', 'help_switch_off_title',
    'help_switch_off_desc',
}
REQUESTLOG_KEYS = {
    'tabModelStats', 'tabRequestLogs', 'retryErrorLogsTitle', 'allLogs',
    'onlyRetries', 'onlyErrors', 'btnClearLogs', 'btnExportLogs', 'logEmptyText',
    'logCountText', 'clearConfirm', 'finalFail', 'attemptText', 'colModel',
    'colRequests', 'colTotalTokens', 'colInputTokens', 'colOutputTokens',
    'colHitRate', 'colCost', 'colAvgCost', 'colTime', 'colType', 'colAttempt',
    'colAccount', 'colTargetModel', 'colLogPath', 'colDetail', 'colMethodHost',
    'colSession', 'colPrice', 'colResponseTime', 'colCacheStatus', 'colDuration',
    'statusHit', 'statusMiss', 'statusNone', 'loading', 'noData', 'noLogs',
    'reqDetailsTitle', 'reqTime', 'reqSessionId', 'reqModel', 'reqApi',
    'reqTokens', 'reqCacheStatus', 'reqAccount', 'reqHeaders', 'reqBody',
    'btnCopyJson',
}
AGGREGATE_KEYS = {
    'aggregateQuotaInfo', 'aggregateQuotaTitle',
}

# 组装分类
groups = {
    'common': COMMON,
    'dashboard': DASHBOARD,
    'settings': SETTING_KEYS,
    'relay': RELAY_KEYS,
    'nvidia': NVIDIA_KEYS,
    'other': OTHER_KEYS_,
    'accounts': ACCOUNTS_KEYS,
    'otp': OTP_KEYS,
    'packets': PACKETS_KEYS,
    'pricing': PRICING_KEYS,
    'autoTrigger': AUTOTRIGGER_KEYS,
    'usage': USAGE_KEYS,
    'help': HELP_KEYS,
    'requestlog': REQUESTLOG_KEYS,
    'aggregate': AGGREGATE_KEYS,
}

# 归类 + 登记
all_zh = set(zhk)
result = {g: [] for g in groups}
unclassified = []
for k in zhk:  # 保持 zh 顺序
    placed = False
    for g, ks in groups.items():
        if k in ks:
            result[g].append(k)
            placed = True
            break
    if not placed:
        unclassified.append(k)

# 校验每个分组集合里的 key 是否都存在于 zh（ catches typos）
for g, ks in groups.items():
    extra = ks - all_zh
    if extra:
        print(f"[WARN] {g} has keys not in zh:", sorted(extra))

print("=== 命名空间 key 数 ===")
total = 0
for g, ks in result.items():
    print(f"{g}: {len(ks)}")
    total += len(ks)
print(f"total classified: {total} / {len(zhk)}")
print("UNCLASSIFIED:", unclassified)

Path('/tmp/ns.json').write_text(json.dumps(result, ensure_ascii=False, indent=2), encoding='utf-8')
print("written /tmp/ns.json")
