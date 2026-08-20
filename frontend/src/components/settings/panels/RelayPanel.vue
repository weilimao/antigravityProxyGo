<template>
<div class="hidden flex-col gap-5 w-full" id="settings-panel-relay">
<!-- 启用中继服务器 -->
<div class="bg-white dark:bg-[#1e2538] rounded-xl border border-outline-variant/20 p-5">
<h3 class="text-[14px] font-bold text-on-surface dark:text-white mb-4 flex items-center gap-2">
<span class="material-symbols-outlined text-[18px] text-primary">dns</span>
<span data-i18n="relayServerTitle">中继服务器</span>
</h3>
<div class="flex items-center justify-between mb-4">
<div>
<div class="text-[13px] font-medium text-on-surface dark:text-white" data-i18n="relayEnableLabel">启用中继服务器</div>
<div class="text-[11px] text-outline/60" data-i18n="relayEnableDesc">开放端口供其他客户端远程连接使用</div>
</div>
<div class="flex items-center gap-3">
<input class="w-20 px-2 py-1 text-[12px] rounded-md border border-outline-variant/30 bg-white dark:bg-[#1a1f30] text-on-surface dark:text-white text-center" id="relayPortInput" placeholder="18444" type="text" value="18444">
<label class="relative inline-block w-10 h-5 cursor-pointer">
<input class="sr-only peer" id="chkRelayEnabled" type="checkbox">
<div class="w-10 h-5 bg-slate-300 dark:bg-slate-600 rounded-full peer-checked:bg-primary transition-colors"></div>
<div class="absolute left-0.5 top-0.5 w-4 h-4 bg-white rounded-full transition-transform peer-checked:translate-x-5 shadow-sm"></div>
</input></label>
</input></div>
</div>

<!-- 新增子 Tab 切换栏 -->
<div class="flex items-center gap-2 border-t border-outline-variant/10 pt-4 mt-4">
  <button id="btnRelaySubTabUsers" class="px-4 py-1.5 text-[12px] font-bold bg-primary/10 text-primary dark:bg-primary/20 rounded-lg cursor-pointer transition-all duration-200" data-i18n="relaySubTabUsers">中继用户</button>
  <button id="btnRelaySubTabPackages" class="px-4 py-1.5 text-[12px] font-medium text-slate-500 hover:text-slate-800 dark:text-slate-400 dark:hover:text-slate-200 rounded-lg cursor-pointer transition-all duration-200" data-i18n="relaySubTabPackages">限额套餐</button>
  <button id="btnRelaySubTabSecurity" class="px-4 py-1.5 text-[12px] font-medium text-slate-500 hover:text-slate-800 dark:text-slate-400 dark:hover:text-slate-200 rounded-lg cursor-pointer transition-all duration-200" data-i18n="relaySubTabSecurity">中继配置</button>
  <button id="btnRelaySubTabModelMapping" class="px-4 py-1.5 text-[12px] font-medium text-slate-500 hover:text-slate-800 dark:text-slate-400 dark:hover:text-slate-200 rounded-lg cursor-pointer transition-all duration-200" data-i18n="relaySubTabModelMapping">模型映射</button>
  <button id="btnRelaySubTabTutorial" class="px-4 py-1.5 text-[12px] font-medium text-slate-500 hover:text-slate-800 dark:text-slate-400 dark:hover:text-slate-200 rounded-lg cursor-pointer transition-all duration-200" data-i18n="relaySubTabTutorial">配置教程</button>
</div>
</div>

<!-- 中继用户面板 (默认显示) -->
<div id="relay-sub-panel-users" class="flex flex-col gap-6 w-full">
    <!-- 中继用户列表 -->
    <div class="bg-white dark:bg-[#1e2538] rounded-xl border border-outline-variant/20 p-5">
    <div class="flex items-center justify-between mb-4">
    <h3 class="text-[14px] font-bold text-on-surface dark:text-white flex items-center gap-2">
    <span class="material-symbols-outlined text-[18px] text-primary">group</span>
    <span data-i18n="relayUsersTitle">中继用户</span>
    </h3>
    <button class="flex items-center gap-1 text-[12px] font-medium text-primary hover:text-primary/80 transition-colors" id="btnAddRelayUser">
    <span class="material-symbols-outlined text-[16px]">person_add</span>
    <span data-i18n="relayAddUser">添加用户</span>
    </button>
    </div>
    <!-- 筛选与搜索条件 -->
    <div class="flex flex-wrap items-center gap-3 mb-4">
        <!-- 账户名搜索 -->
        <div class="relative flex-1 min-w-[200px]">
            <span class="absolute inset-y-0 left-0 pl-3 flex items-center pointer-events-none text-outline/50">
                <span class="material-symbols-outlined text-[18px]">search</span>
            </span>
            <input type="text" id="relayUserSearchInput" placeholder="按账户名搜索..." data-i18n-placeholder="relayUserSearchPlaceholder"
                class="w-full pl-9 pr-3 py-1.5 text-[12px] rounded-lg border border-outline-variant/30 bg-slate-50 dark:bg-white/5 text-on-surface dark:text-white focus:outline-none focus:border-primary/60" />
        </div>
        <!-- 套餐类型筛选 -->
        <div class="relative w-[180px]">
            <select id="relayUserPackageFilter" 
                class="w-full px-3 py-1.5 text-[12px] rounded-lg border border-outline-variant/30 bg-slate-50 dark:bg-white/5 text-on-surface dark:text-white focus:outline-none focus:border-primary/60 appearance-none cursor-pointer">
                <option value="all" data-i18n="relayUserFilterAll">所有套餐类型</option>
                <option value="unlimited" data-i18n="relayUserFilterUnlimited">无限制</option>
                <option value="custom" data-i18n="relayUserFilterCustom">自定义限额</option>
            </select>
            <span class="absolute inset-y-0 right-0 pr-3 flex items-center pointer-events-none text-outline/50">
                <span class="material-symbols-outlined text-[16px]">keyboard_arrow_down</span>
            </span>
        </div>
    </div>
    <div id="relayUsersList"></div>
    <!-- 分页控件 -->
    <div class="flex items-center justify-between border-t border-outline-variant/20 pt-4 mt-3">
        <span class="text-[11px] text-outline" id="relayUserPaginationInfo">显示第 0 - 0 个用户，共 0 个</span>
        <div class="flex items-center gap-1">
            <button id="btnRelayUserPrevPage" class="px-2.5 py-1 text-[11px] font-medium border border-outline-variant/30 rounded-md hover:bg-slate-50 dark:hover:bg-white/5 text-on-surface dark:text-white disabled:opacity-50 disabled:pointer-events-none flex items-center gap-0.5 cursor-pointer">
                <span class="material-symbols-outlined text-[14px]">chevron_left</span>
                <span data-i18n="btnPrevPage">上一页</span>
            </button>
            <span class="text-[11px] px-2 text-on-surface dark:text-white font-bold" id="relayUserCurrentPage">1</span>
            <button id="btnRelayUserNextPage" class="px-2.5 py-1 text-[11px] font-medium border border-outline-variant/30 rounded-md hover:bg-slate-50 dark:hover:bg-white/5 text-on-surface dark:text-white disabled:opacity-50 disabled:pointer-events-none flex items-center gap-0.5 cursor-pointer">
                <span data-i18n="btnNextPage">下一页</span>
                <span class="material-symbols-outlined text-[14px]">chevron_right</span>
            </button>
        </div>
    </div>
    </div>
</div>

<!-- 限额套餐面板 (默认隐藏) -->
<div id="relay-sub-panel-packages" class="flex flex-col gap-6 w-full hidden">
    <!-- 套餐模板管理 -->
    <div class="bg-white dark:bg-[#1e2538] rounded-xl border border-outline-variant/20 p-5">
    <div class="flex items-center justify-between mb-4">
    <h3 class="text-[14px] font-bold text-on-surface dark:text-white flex items-center gap-2">
    <span class="material-symbols-outlined text-[18px] text-primary">view_quilt</span>
    <span data-i18n="relayPackagesTitle">限额套餐模板</span>
    </h3>
    <button class="flex items-center gap-1 text-[12px] font-medium text-primary hover:text-primary/80 transition-colors" onclick="window._relayOpenPackageSettings('')">
    <span class="material-symbols-outlined text-[16px]">add</span>
    <span data-i18n="relayNewPackage">新建套餐</span>
    </button>
    </div>
    <div class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-3" id="relayPackagesList"></div>
    </div>
</div>

<!-- 中继配置面板 (默认隐藏) -->
<div id="relay-sub-panel-security" class="flex flex-col gap-6 w-full hidden">
    <div class="bg-white dark:bg-[#1e2538] rounded-xl border border-outline-variant/20 p-5">
        <h3 class="text-[14px] font-bold text-on-surface dark:text-white mb-5 flex items-center gap-2">
            <span class="material-symbols-outlined text-[18px] text-primary">security</span>
            <span data-i18n="relaySecurityTitle">中继安全与防攻击配置</span>
        </h3>
        
        <div class="space-y-5">
            <!-- SSRF Switch -->
            <div class="flex items-center justify-between">
                <div>
                    <div class="text-[13px] font-medium text-on-surface dark:text-white" data-i18n="relaySSRFLabel">启用 SSRF 安全防护 (推荐)</div>
                    <div class="text-[11px] text-outline/60" data-i18n="relaySSRFDesc">拦截发往 127.0.0.1、localhost 以及局域网私有网段的恶意拨号穿透</div>
                </div>
                <label class="relative inline-block w-10 h-5 cursor-pointer">
                    <input class="sr-only peer" id="chkRelaySSRFBlock" type="checkbox">
                    <div class="w-10 h-5 bg-slate-300 dark:bg-slate-600 rounded-full peer-checked:bg-primary transition-colors"></div>
                    <div class="absolute left-0.5 top-0.5 w-4 h-4 bg-white rounded-full transition-transform peer-checked:translate-x-5 shadow-sm"></div>
                </label>
            </div>

            <!-- Port Block Switch -->
            <div class="flex items-center justify-between border-t border-outline-variant/10 pt-4">
                <div>
                    <div class="text-[13px] font-medium text-on-surface dark:text-white" data-i18n="relayPortBlockLabel">限制代理端口</div>
                    <div class="text-[11px] text-outline/60" data-i18n="relayPortBlockDesc">仅放行 80 (HTTP) 和 443 (HTTPS) 常用端口，防止将中继作为其他服务代理</div>
                </div>
                <label class="relative inline-block w-10 h-5 cursor-pointer">
                    <input class="sr-only peer" id="chkRelayPortBlock" type="checkbox">
                    <div class="w-10 h-5 bg-slate-300 dark:bg-slate-600 rounded-full peer-checked:bg-primary transition-colors"></div>
                    <div class="absolute left-0.5 top-0.5 w-4 h-4 bg-white rounded-full transition-transform peer-checked:translate-x-5 shadow-sm"></div>
                </label>
            </div>

            <!-- Domain Whitelist Switch -->
            <div class="flex items-center justify-between border-t border-outline-variant/10 pt-4">
                <div>
                    <div class="text-[13px] font-medium text-on-surface dark:text-white" data-i18n="relayDomainFilterLabel">启用目标域名白名单过滤</div>
                    <div class="text-[11px] text-outline/60" data-i18n="relayDomainFilterDesc">开启后中继服务器只代理列表内的域名流量，拦截并丢弃其余外部网站访问</div>
                </div>
                <label class="relative inline-block w-10 h-5 cursor-pointer">
                    <input class="sr-only peer" id="chkRelayDomainFilter" type="checkbox">
                    <div class="w-10 h-5 bg-slate-300 dark:bg-slate-600 rounded-full peer-checked:bg-primary transition-colors"></div>
                    <div class="absolute left-0.5 top-0.5 w-4 h-4 bg-white rounded-full transition-transform peer-checked:translate-x-5 shadow-sm"></div>
                </label>
            </div>

            <!-- Whitelist List Textarea -->
            <div class="border-t border-outline-variant/10 pt-4 flex flex-col gap-2">
                <div class="text-[13px] font-medium text-on-surface dark:text-white" data-i18n="relayDomainWhitelistLabel">代理域名白名单 (每行一个，支持通配符如 *.google.com)</div>
                <textarea id="txtRelayDomainWhitelist" rows="6" 
                    class="w-full p-3 text-[12px] font-mono rounded-lg border border-outline-variant/30 bg-slate-50 dark:bg-white/5 text-on-surface dark:text-white focus:outline-none focus:border-primary/60 placeholder-slate-400"
                    placeholder="输入允许的域名列表，例如：&#10;*.googleapis.com&#10;*.google.com&#10;*.anthropic.com" data-i18n-placeholder="relayDomainWhitelistPlaceholder"></textarea>
                <div class="flex justify-end mt-1">
                    <button id="btnSaveRelaySecurity" class="px-4 py-1.5 text-[12px] font-medium bg-primary text-white hover:bg-primary/90 rounded-lg cursor-pointer transition-colors flex items-center gap-1.5 shadow-sm">
                        <span class="material-symbols-outlined text-[16px]">save</span>
                        <span data-i18n="relayBtnSaveSecurity">保存配置</span>
                    </button>
                </div>
            </div>
        </div>
    </div>
</div>

<!-- 模型映射面板 (默认隐藏) -->
<div id="relay-sub-panel-modelmapping" class="flex flex-col gap-6 w-full hidden">
    <div class="bg-white dark:bg-[#1e2538] rounded-xl border border-outline-variant/20 p-5">
        <!-- 顶部标题与新增 Tab 按钮 -->
        <div class="flex items-center justify-between mb-4">
            <h3 class="text-[14px] font-bold text-on-surface dark:text-white flex items-center gap-2">
                <span class="material-symbols-outlined text-[18px] text-primary">alt_route</span>
                <span data-i18n="relayModelMappingTitle">自定义中继模型映射与号池绑定</span>
            </h3>
            <div class="flex items-center gap-2">
                <button class="flex items-center gap-1 px-3 py-1 text-[12px] font-medium bg-primary/10 text-primary hover:bg-primary/20 rounded-lg transition-colors cursor-pointer" onclick="window._relayAddTab()">
                    <span class="material-symbols-outlined text-[16px]">add_box</span>
                    <span>新增号池 Tab</span>
                </button>
            </div>
        </div>

        <!-- 动态号池 Tab 列表导航 -->
        <div class="flex items-center gap-2 border-b border-outline-variant/20 pb-2 mb-4 overflow-x-auto" id="modelMappingTabsNav">
            <!-- 动态渲染 Tab 按钮 -->
        </div>

        <!-- 当前 Tab 绑定账号池配置区 -->
        <div class="bg-slate-50 dark:bg-white/5 p-3 rounded-lg border border-outline-variant/15 flex flex-wrap items-center justify-between gap-3 mb-4">
            <div class="flex items-center gap-3 flex-wrap">
                <!-- 搜索筛选输入框 (带清除图标) -->
                <div class="relative w-56">
                    <span class="material-symbols-outlined absolute left-2.5 top-1/2 -translate-y-1/2 text-outline/70 text-[16px] pointer-events-none">search</span>
                    <input type="text" id="inputRelayModelMappingSearch" class="w-full pl-8 pr-7 py-1 text-[12px] bg-white dark:bg-[#1e2538] border border-outline-variant/30 rounded-lg focus:border-primary focus:ring-2 focus:ring-primary/15 focus:outline-none transition-all placeholder:text-outline/50 text-on-surface dark:text-white" placeholder="搜索模型映射..." data-i18n-placeholder="relayModelMappingSearchPlaceholder" />
                    <button id="btnClearRelayModelMappingSearch" class="hidden absolute right-2 top-1/2 -translate-y-1/2 text-outline hover:text-on-surface dark:hover:text-white p-0.5 rounded-full hover:bg-slate-200 dark:hover:bg-white/10 transition-colors cursor-pointer" title="清空搜索" data-i18n-title="relayModelMappingClearSearch">
                        <span class="material-symbols-outlined text-[13px] block">close</span>
                    </button>
                </div>

                <span class="text-[12px] font-bold text-on-surface dark:text-white flex items-center gap-1">
                    <span class="material-symbols-outlined text-[16px] text-primary">hub</span>
                    <span>路由目标账号池 (Target Provider):</span>
                </span>
                <select id="tabTargetProviderSelect" class="px-2 py-1 text-[12px] font-mono rounded border border-outline-variant/30 bg-white dark:bg-[#1e2538] text-on-surface dark:text-white focus:outline-none focus:border-primary">
                    <!-- 动态渲染可用账号池列表 -->
                </select>
                <input type="text" id="tabTargetProviderCustom" class="px-2 py-1 text-[12px] font-mono rounded border border-outline-variant/30 bg-white dark:bg-[#1e2538] text-on-surface dark:text-white hidden w-32" placeholder="自定义号池ID" />
                <button id="btnFetchChannelModels" class="flex items-center gap-1 px-2.5 py-1 text-[12px] font-medium bg-primary/10 text-primary hover:bg-primary/20 rounded-lg transition-colors cursor-pointer border border-primary/20" onclick="window._relayFetchChannelModels()">
                    <span class="material-symbols-outlined text-[15px]">sync</span>
                    <span>获取号池模型</span>
                </button>
                <span id="lblFetchedModelsCount" class="text-[11px] text-primary font-medium hidden"></span>
                <!-- 清除失效模型:删除「真实目标模型已从远端下架」的映射行。默认禁用,需先成功获取号池模型核对远端全集后才可点。 -->
                <button id="btnClearStaleModels" disabled
                    class="flex items-center gap-1 px-2.5 py-1 text-[12px] font-medium bg-red-500/10 text-red-500 hover:bg-red-500/20 rounded-lg transition-colors cursor-pointer border border-red-500/20 disabled:opacity-40 disabled:cursor-not-allowed disabled:hover:bg-red-500/10"
                    onclick="window._relayClearStaleMappings()"
                    data-i18n="relayClearStaleModels" data-i18n-title="relayClearStaleTip"
                    title="删除「真实目标模型」已从远端下架的映射行(需先成功获取号池模型)">
                    <span class="material-symbols-outlined text-[15px]">cleaning_services</span>
                    <span>清除失效模型</span>
                </button>
                <!-- Other 号池多组获取按钮容器:切到 Other Tab 时由 relayController 动态渲染按组按钮,默认隐藏 -->
                <div id="otherGroupFetchContainer" class="hidden flex flex-wrap items-center gap-2 ml-1"></div>
            </div>
            <div class="flex items-center gap-2">
                <button id="btnDeleteCurrentTab" class="text-red-500 hover:text-red-700 text-[12px] font-medium flex items-center gap-1 transition-colors cursor-pointer hidden" onclick="window._relayDeleteCurrentTab()">
                    <span class="material-symbols-outlined text-[15px]">delete</span>
                    <span>删除当前 Tab</span>
                </button>
                <button class="flex items-center gap-1 text-[12px] font-medium text-primary hover:text-primary/80 transition-colors cursor-pointer px-2 py-1 rounded bg-primary/10" onclick="window._relayAddModelMapping()">
                    <span class="material-symbols-outlined text-[16px]">add</span>
                    <span data-i18n="relayAddMapping">添加映射模型</span>
                </button>
            </div>
        </div>

        <datalist id="channelModelsDatalist"></datalist>

        <!-- 当前 Tab 下的模型映射表格 -->
        <div class="overflow-x-auto max-h-[360px] overflow-y-auto pr-1">
            <table class="w-full text-left text-[12px]">
                <thead>
                    <tr class="border-b border-outline-variant/25 text-outline/80">
                        <th class="py-2.5 font-bold pl-2" data-i18n="relayMappingClientModel">客户端请求模型 (Client Model)</th>
                        <th class="py-2.5 font-bold pl-2" data-i18n="relayMappingTargetModel">真实目标模型 (Target Model)</th>
                        <th id="thInjectKwargs" class="py-2.5 font-bold text-center w-[160px] hidden" data-i18n="relayMappingInjectKwargs">注入 Template Kwargs</th>
                        <th class="py-2.5 font-bold text-center w-[140px]" data-i18n="relayMappingMultimodal">多模态</th>
                        <th class="py-2.5 font-bold text-center w-[120px]" data-i18n="relayMappingExpose">是否公开 (Expose)</th>
                        <th class="py-2.5 font-bold text-center w-[80px]" data-i18n="autoTriggerColAction">操作</th>
                    </tr>
                </thead>
                <tbody id="modelMappingTableBody">
                    <!-- 动态渲染映射行 -->
                </tbody>
            </table>
        </div>

        <div class="flex justify-end gap-3 mt-5 border-t border-outline-variant/20 pt-4">
            <button class="px-4 py-1.5 text-[12px] font-bold bg-primary text-white rounded-lg hover:bg-primary/90 transition-all duration-200 cursor-pointer shadow-md shadow-primary/20 flex items-center gap-1" onclick="window._relaySaveModelMapping()" id="btnSaveModelMapping">
                <span class="material-symbols-outlined text-[16px]">save</span>
                <span data-i18n="relaySaveMapping">保存全部映射与号池配置</span>
            </button>
        </div>
    </div>
</div>
<!-- 教程子面板(中继服务器子 tab) -->
<div id="relay-sub-panel-tutorial" class="flex flex-col gap-6 w-full hidden">
<div class="glass-card rounded-xl p-6 flex flex-col gap-4">
<h2 class="text-[15px] font-bold text-on-surface dark:text-white flex items-center gap-2">
<span class="material-symbols-outlined text-primary text-[20px]">menu_book</span>
<span data-i18n="tutorialTitle">中继网关配置完全指南</span>
</h2>
<p class="text-xs text-outline leading-relaxed" data-i18n="tutorialIntro">
本指南基于本软件真实代码整理，带你从零配置一个可对外提供服务的 AI 中继网关。按顺序完成下方 7 步即可。
</p>
<!-- 步骤 1 -->
<div class="flex flex-col gap-2 border-t border-outline-variant/20 pt-4">
<h3 class="text-[13px] font-bold text-on-surface dark:text-white flex items-center gap-1.5">
<span class="material-symbols-outlined text-primary text-[16px]">toggle_on</span>
<span data-i18n="tutorialStep1Title">第 1 步：启用中继服务器</span>
</h3>
<p class="text-[12px] text-outline leading-relaxed" data-i18n="tutorialStep1Desc">
进入「系统设置 → 中继服务器」子页，勾选顶部「启用中继服务器」开关。后端字段 RelayEnabled，开启后自动监听端口并接受外部连接；关闭时立即停止监听并踢掉所有现役连接。
</p>
</div>
<!-- 步骤 2 -->
<div class="flex flex-col gap-2 border-t border-outline-variant/20 pt-4">
<h3 class="text-[13px] font-bold text-on-surface dark:text-white flex items-center gap-1.5">
<span class="material-symbols-outlined text-primary text-[16px]">router</span>
<span data-i18n="tutorialStep2Title">第 2 步：设置监听端口</span>
</h3>
<p class="text-[12px] text-outline leading-relaxed" data-i18n="tutorialStep2Desc">
同页端口输入框，默认 18444，留空时后端自动兜底为 18444。监听地址 0.0.0.0，意味着局域网/公网（需自行放行防火墙）均可访问。如端口被占用，改为其他空闲端口即可。
</p>
</div>
<!-- 步骤 3 -->
<div class="flex flex-col gap-2 border-t border-outline-variant/20 pt-4">
<h3 class="text-[13px] font-bold text-on-surface dark:text-white flex items-center gap-1.5">
<span class="material-symbols-outlined text-primary text-[16px]">account_circle</span>
<span data-i18n="tutorialStep3Title">第 3 步：往账号池添加上游账号</span>
</h3>
<p class="text-[12px] text-outline leading-relaxed" data-i18n="tutorialStep3Desc">
账号池管理在「账号池」页面（不是本设置页），支持三种上游渠道，按页顶 Tab 切换：
</p>
<div class="flex flex-col gap-2 ml-1">
<div class="text-[12px] text-outline leading-relaxed">
<span class="font-bold text-on-surface dark:text-white">① Antigravity 官方账号</span>：走 OAuth 登录，凭据自动落库（需 access_token + refresh_token）。
</div>
<div class="text-[12px] text-outline leading-relaxed">
<span class="font-bold text-on-surface dark:text-white">② GCP 项目通道（project）</span>：OAuth 登录或手动录入，比 Antigravity 多一个必填 ProjectID 字段。
</div>
<div class="text-[12px] text-outline leading-relaxed">
<span class="font-bold text-on-surface dark:text-white">③ NVIDIA 号池（nvidia）</span>：手动录入 API Key，必填 BaseURL（必须是 https，留空默认 <code class="text-[11px]">https://integrate.api.nvidia.com/v1</code>）+ APIKey；模型字段全留空时默认模型为 <code class="text-[11px]">moonshotai/kimi-k2.5</code>。
</div>
</div>
</div>
<!-- 步骤 4 -->
<div class="flex flex-col gap-2 border-t border-outline-variant/20 pt-4">
<h3 class="text-[13px] font-bold text-on-surface dark:text-white flex items-center gap-1.5">
<span class="material-symbols-outlined text-primary text-[16px]">swap_horiz</span>
<span data-i18n="tutorialStep4Title">第 4 步：配置模型映射</span>
</h3>
<p class="text-[12px] text-outline leading-relaxed" data-i18n="tutorialStep4Desc">
在「中继服务器 → 模型映射」子页配置。每条映射含三项：ClientModel（客户端请求的模型名）→ TargetModel（真实上游模型名）+ Expose（是否在 /v1/models 列表对外公开）。例如把 <code class="text-[11px]">claude-sonnet-4-5</code> 映射到 <code class="text-[11px]">gemini-2.5-pro</code>，客户端用 Claude 模型名请求，后端自动转译到 Gemini 上游。映射为空时自动回退内置默认 70+ 条。
</p>
<p class="text-[12px] text-outline leading-relaxed" data-i18n="tutorialStep4Note">
例外：NVIDIA 号池走自己的「四档位」解析（sonnet/opus/haiku/fable），按关键字命中后取账号上对应字段，不走全局模型映射。
</p>
</div>
<!-- 步骤 5 -->
<div class="flex flex-col gap-2 border-t border-outline-variant/20 pt-4">
<h3 class="text-[13px] font-bold text-on-surface dark:text-white flex items-center gap-1.5">
<span class="material-symbols-outlined text-primary text-[16px]">hub</span>
<span data-i18n="tutorialStep5Title">第 5 步：选择当前中继通道</span>
</h3>
<p class="text-[12px] text-outline leading-relaxed" data-i18n="tutorialStep5Desc">
「中继通道」即账号池页顶部的 Antigravity / GCP / NVIDIA 三选一切换。它决定 /v1internal:* 直连请求从哪个通道选号。注意：/v1/messages、/v1/chat/completions 这类转译链路固定经本地回环到 ProxyEngine 再选号，与通道切换无关。
</p>
</div>
<!-- 步骤 6 -->
<div class="flex flex-col gap-2 border-t border-outline-variant/20 pt-4">
<h3 class="text-[13px] font-bold text-on-surface dark:text-white flex items-center gap-1.5">
<span class="material-symbols-outlined text-primary text-[16px]">key</span>
<span data-i18n="tutorialStep6Title">第 6 步：创建中继用户并分配套餐</span>
</h3>
<p class="text-[12px] text-outline leading-relaxed" data-i18n="tutorialStep6Desc">
在「中继服务器 → 中继用户」子页添加下游用户，系统为其生成 API Key（格式 <code class="text-[11px]">sk-ant-</code> 开头）。额度模板在「用量套餐」子页维护（Pro / Pro 5x / Pro 20x 三档额度+有效期+限流），建用户时绑定套餐即可继承额度。鉴权时客户端凭此 API Key 通过。
</p>
</div>
<!-- 步骤 7：客户端连入 -->
<div class="flex flex-col gap-2 border-t border-outline-variant/20 pt-4">
<h3 class="text-[13px] font-bold text-on-surface dark:text-white flex items-center gap-1.5">
<span class="material-symbols-outlined text-primary text-[16px]">link</span>
<span data-i18n="tutorialStep7Title">第 7 步：客户端连入网关</span>
</h3>
<p class="text-[12px] text-outline leading-relaxed" data-i18n="tutorialStep7Desc">
网关地址形如 <code class="text-[11px]">http://[中继主机IP]:18444</code>，API Key 通过 <code class="text-[11px]">Authorization: Bearer sk-ant-...</code> 或 <code class="text-[11px]">x-api-key: sk-ant-...</code> 传递。各客户端按入站协议选路径：
</p>
<pre class="text-[11px] bg-slate-50 dark:bg-black/30 border border-outline-variant/20 rounded-lg p-3 overflow-x-auto text-outline leading-relaxed font-mono"># 统一模型路由模式（按「模型映射/规则」自动路由分发至指定号池）
base_url=http://[host]:18444/route/v1/chat/completions
# 也支持 Anthropic 协议: ANTHROPIC_BASE_URL=http://[host]:18444/route/v1/messages

# Claude Code (Anthropic 协议)
ANTHROPIC_BASE_URL=http://[host]:18444/v1/messages
ANTHROPIC_API_KEY=sk-ant-...

# OpenAI SDK / Codex / Cherry Studio (Chat 协议)
base_url=http://[host]:18444/v1/chat/completions

# Codex Responses 模式
base_url=http://[host]:18444/v1/responses

# NVIDIA 兼容客户端（直连号池 NVIDIA 账号；/vc 为 /nvidia 的纯别名快捷前缀）
base_url=http://[host]:18444/nvidia/v1/chat/completions
# 快捷别名: base_url=http://[host]:18444/vc/v1/chat/completions
# 也可 /nvidia/v1/messages 或 /vc/v1/messages 走 Anthropic 协议回译

# antigravity v1internal 非流式接口（一次性返回完整 JSON）
POST http://[host]:18444/v1internal:generateContent
#   流式可在路径后加 ?alt=sse，或直接用 /v1internal:streamGenerateContent
Authorization: Bearer sk-ant-...</pre>
<p class="text-[12px] text-outline leading-relaxed" data-i18n="tutorialStep7Note">
免登录体验：若未创建任何中继用户，只要请求头的 Key 以 sk-ant- / nvapi- / sk- 开头，后端会降级放行，方便快速裸跑验证（生产环境务必建用户管控）。
</p>
</div>
<!-- 路由速查表 -->
<div class="flex flex-col gap-2 border-t border-outline-variant/20 pt-4">
<h3 class="text-[13px] font-bold text-on-surface dark:text-white flex items-center gap-1.5">
<span class="material-symbols-outlined text-primary text-[16px]">alt_route</span>
<span data-i18n="tutorialRouteTableTitle">入站端点速查</span>
</h3>
<div class="overflow-x-auto">
<table class="text-[11px] w-full border border-outline-variant/20 rounded-lg">
<thead>
<tr class="bg-slate-50 dark:bg-white/5 text-on-surface dark:text-white">
<th class="text-left p-2 font-bold">入站路径</th>
<th class="text-left p-2 font-bold">入站协议</th>
<th class="text-left p-2 font-bold">上游去向</th>
</tr>
</thead>
<tbody class="text-outline">
<tr class="border-t border-outline-variant/20"><td class="p-2 font-mono text-primary font-bold">/route/*</td><td class="p-2">OpenAI / Anthropic / v1internal</td><td class="p-2"><b>统一模型路由</b>：匹配「自定义模型映射」自动分发到目标号池 (google/nvidia/deepseek等)</td></tr>
<tr class="border-t border-outline-variant/20"><td class="p-2 font-mono">/v1/messages</td><td class="p-2">Anthropic</td><td class="p-2">转译→Gemini/Vertex 回环 ProxyEngine</td></tr>
<tr class="border-t border-outline-variant/20"><td class="p-2 font-mono">/v1/chat/completions</td><td class="p-2">OpenAI Chat</td><td class="p-2">转译→Gemini/Vertex 回环 ProxyEngine</td></tr>
<tr class="border-t border-outline-variant/20"><td class="p-2 font-mono">/v1/responses</td><td class="p-2">OpenAI Responses</td><td class="p-2">转译→Gemini/Vertex 回环 ProxyEngine</td></tr>
<tr class="border-t border-outline-variant/20"><td class="p-2 font-mono">/nvidia/v1/* 或 /vc/v1/*</td><td class="p-2">OpenAI Chat / Anthropic</td><td class="p-2">直连号池 NVIDIA 账号 BaseURL（<code>/vc</code> 为 <code>/nvidia</code> 纯别名快捷前缀）</td></tr>
<tr class="border-t border-outline-variant/20"><td class="p-2 font-mono">/v1internal:generateContent</td><td class="p-2">antigravity 内部</td><td class="p-2">按当前通道直选号发包（非流式；加 ?alt=sse 升流）</td></tr>
<tr class="border-t border-outline-variant/20"><td class="p-2 font-mono">/v1internal:streamGenerateContent</td><td class="p-2">antigravity 内部</td><td class="p-2">按当前通道直选号发包（流式 SSE）</td></tr>
<tr class="border-t border-outline-variant/20"><td class="p-2 font-mono">CONNECT</td><td class="p-2">HTTPS 隧道(MITM)</td><td class="p-2">透传 ProxyEngine 走原生直连</td></tr>
</tbody>
</table>
</div>
</div>
<!-- 安全加固 -->
<div class="flex flex-col gap-2 border-t border-outline-variant/20 pt-4">
<h3 class="text-[13px] font-bold text-on-surface dark:text-white flex items-center gap-1.5">
<span class="material-symbols-outlined text-primary text-[16px]">shield</span>
<span data-i18n="tutorialSecurityTitle">公网部署安全加固（可选但推荐）</span>
</h3>
<p class="text-[12px] text-outline leading-relaxed" data-i18n="tutorialSecurityDesc">
若部署到公网，强烈建议在「中继配置」子页开启 SSRF 拦截、端口黑名单、域名过滤/白名单，防止网关被滥用为开放代理隧道。并务必为每个中继用户配置合理的速率限制（默认 30 次/分钟）与 Token 配额。
</p>
</div>

</div>
</div>
</div>
</template>

<script setup lang="ts">
// RelayPanel: 从 Settings.vue 提取的纯展示面板。
// 保留所有 id / data-i18n / onclick / class 属性，
// 使 settingsController / relayController 的 getElementById 与 classList 操作零改动。
</script>
