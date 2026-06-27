<template>
<div class="flex flex-col gap-6 w-full" id="view-dashboard">
<!-- 页面标题 -->
<div class="flex justify-between items-end">
<div>
<h1 class="text-2xl font-bold text-on-surface dark:text-white" data-i18n="title">控制台</h1>
<p class="text-xs text-outline dark:text-outline-variant" data-i18n="logBufferTitle">监控和分析被拦截的代理流量</p>
</div>
<div class="flex gap-2">
<button class="flex items-center gap-1.5 px-3 py-1.5 bg-white dark:bg-[#1a1f30] border border-outline-variant/50 rounded-md text-[13px] font-medium text-primary dark:text-primary-fixed-dim hover:bg-surface-container-low transition-colors" id="btnExportLogs">
<span class="material-symbols-outlined text-[16px]">download</span>
                    导出日志
                </button>
</div>
</div>
<!-- 账号池总额度汇总 -->
<div class="hidden glass-card rounded-xl p-4 flex-col gap-3 relative overflow-hidden border border-outline-variant/30" id="aggregate-quota-panel">
<div class="flex justify-between items-center">
<div class="flex items-center gap-2">
<span class="text-[11px] font-bold text-outline dark:text-outline-variant uppercase tracking-wider">账号池总额度汇总</span>
<button class="flex items-center gap-1 text-[10px] font-semibold text-outline hover:text-primary dark:hover:text-primary-fixed-dim bg-outline-variant/10 hover:bg-primary/10 border border-outline-variant/20 hover:border-primary/30 px-2 py-0.5 rounded transition-all duration-200 select-none" id="btnRefreshAggregateQuota" title="刷新账号池所有账号的配额">
<span class="material-symbols-outlined text-[12px]" id="btnRefreshAggregateIcon">sync</span>
<span>一键刷新</span>
</button>
</div>
<span class="text-[11px] text-primary dark:text-primary-fixed-dim font-bold" id="aggregate-quota-info">共 0 个账号</span>
</div>
<div class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4" id="aggregate-quota-grid">
<!-- JS 动态填充 -->
</div>
</div>
<!-- 指标卡片行 -->
<div class="grid grid-cols-1 sm:grid-cols-2 xl:grid-cols-4 gap-4">
<!-- 指标 1: 总请求次数 -->
<div class="glass-card rounded-xl p-5 flex flex-col gap-2 relative overflow-hidden">
<div class="flex justify-between items-center">
<div class="text-[11px] font-bold text-outline dark:text-outline-variant uppercase tracking-wider" data-i18n="totalRequests">总请求次数</div>
<div class="text-[28px] font-data-mono font-medium text-on-surface dark:text-white" id="valReqs">0</div>
</div>
<div class="flex items-end gap-4 h-full pt-2">
<div class="flex-1">
<div class="flex justify-between text-[13px] font-medium mb-1 font-data-mono">
<span class="text-amber-500 dark:text-amber-400 cursor-pointer hover:underline select-none" id="btnViewRetries"><span data-i18n="totalRetries">重试次数</span>: <span class="font-bold" id="valRetries">0</span></span>
<span class="text-rose-500 dark:text-rose-400 cursor-pointer hover:underline select-none" id="btnViewErrors"><span data-i18n="totalErrors">报错次数</span>: <span class="font-bold" id="valErrors">0</span></span>
</div>
<div class="w-full h-2 bg-outline-variant/20 dark:bg-white/5 rounded-full overflow-hidden flex">
<div class="bg-emerald-500 h-full" id="barSuccess" style="width: 100%;"></div>
<div class="bg-rose-500 h-full" id="barErrors" style="width: 0%;"></div>
</div>
<div class="flex justify-between items-center text-[13px] text-slate-500 dark:text-slate-400 mt-2">
<span></span>
<span><span data-i18n="successRate">成功率:</span> <span class="font-data-mono text-emerald-600 dark:text-emerald-400 font-bold" id="valSuccessRate">100.0%</span></span>
</div>
</div>
</div>
</div>
<!-- 指标 2: Token 使用总量 -->
<div class="glass-card rounded-xl p-5 flex flex-col gap-2 relative overflow-hidden">
<div class="flex justify-between items-center">
<div class="text-[11px] font-bold text-outline dark:text-outline-variant uppercase tracking-wider" data-i18n="totalTokens">Token总使用量</div>
<div class="text-[28px] font-data-mono font-medium text-primary dark:text-primary-fixed-dim" id="valTokens">0</div>
</div>
<div class="flex items-end gap-4 h-full pt-2">
<div class="flex-1">
<div class="flex justify-between text-[13px] font-medium mb-1 font-data-mono">
<span class="text-blue-500 dark:text-blue-400"><span data-i18n="input">输入</span>: <span class="font-bold" id="valTokensIn">0</span></span>
<span class="text-emerald-500 dark:text-emerald-400"><span data-i18n="output">输出</span>: <span class="font-bold" id="valTokensOut">0</span></span>
</div>
<div class="w-full h-2 bg-outline-variant/20 dark:bg-white/5 rounded-full overflow-hidden flex">
<div class="bg-blue-500 h-full" id="barTokensIn" style="width: 50%;"></div>
<div class="bg-emerald-500 h-full" id="barTokensOut" style="width: 50%;"></div>
</div>
<div class="flex justify-between items-center text-[13px] text-slate-500 dark:text-slate-400 mt-2">
<span></span>
<span><span data-i18n="totalCost">总成本:</span> <span class="font-data-mono text-red-600 dark:text-red-400 font-bold" id="valTotalCost">$0.0000</span></span>
</div>
</div>
</div>
</div>
<!-- 指标 3: 缓存命中率 -->
<div class="glass-card rounded-xl p-5 flex flex-col gap-2 relative overflow-hidden">
<div class="text-[11px] font-bold text-outline dark:text-outline-variant uppercase tracking-wider" data-i18n="cacheHitRate">缓存命中率</div>
<div class="flex items-center justify-between mt-1">
<div class="flex flex-col">
<span class="text-[24px] font-data-mono font-medium text-emerald-600 dark:text-emerald-400" id="valHitRate">0.0%</span>
<span class="text-[13px] text-slate-500 dark:text-slate-400 mt-1"><span data-i18n="savedCost">节省成本:</span> <span class="font-data-mono text-emerald-600 dark:text-emerald-400 font-bold" id="valSavedCost">$0.0000</span></span>
<span class="text-[13px] text-slate-500 dark:text-slate-400"><span data-i18n="cachedTokens">缓存Tokens:</span> <span class="font-data-mono font-bold text-on-surface dark:text-white" id="valCached">0</span></span>
</div>
<div class="relative w-12 h-12">
<svg class="w-full h-full transform -rotate-90" viewBox="0 0 36 36">
<path class="text-outline-variant/30" d="M18 2.0845 a 15.9155 15.9155 0 0 1 0 31.831 a 15.9155 15.9155 0 0 1 0 -31.831" fill="none" stroke="currentColor" stroke-width="3"></path>
<path class="text-emerald-500 dark:text-emerald-400" d="M18 2.0845 a 15.9155 15.9155 0 0 1 0 31.831 a 15.9155 15.9155 0 0 1 0 -31.831" fill="none" id="gaugeCircle" stroke="currentColor" stroke-dasharray="0, 100" stroke-width="3"></path>
</svg>
</div>
</div>
</div>
<!-- 指标 4: 内存占用 -->
<div class="glass-card rounded-xl p-5 flex flex-col gap-2 relative">
<div class="flex items-center gap-1.5">
<div class="text-[11px] font-bold text-outline dark:text-outline-variant uppercase tracking-wider">系统物理内存</div>
<!-- Tooltip 说明 -->
<div class="relative group">
<span class="material-symbols-outlined text-[14px] text-outline/60 cursor-help select-none">help</span>
<div class="absolute left-0 top-full mt-2 w-64 pointer-events-none opacity-0 group-hover:opacity-100 transition-opacity duration-200 z-50">
<div class="bg-slate-900 dark:bg-slate-800 text-white text-[11px] leading-relaxed rounded-lg shadow-xl px-3 py-2.5">
<div class="absolute left-3 bottom-full w-0 h-0 border-x-4 border-x-transparent border-b-4 border-b-slate-900 dark:border-b-slate-800"></div>
<p class="font-bold text-blue-300 mb-1">📌 关于内存数字说明</p>
<p class="text-slate-300"><span class="text-emerald-400 font-semibold">系统物理内存</span>：主进程及所有渲染/网络子进程在物理内存 (RAM) 中的实际占用总和，与任务管理器相符。</p>
<p class="text-slate-300 mt-1.5"><span class="text-blue-400 font-semibold">Go 堆内存</span>：Go 后端运行时当前分配的堆对象大小，反映核心引擎的数据处理开销。</p>
</div>
</div>
</div>
</div>
<div class="flex items-center justify-between mt-1">
<div class="flex flex-col z-10 shrink-0">
<!-- 主数值：系统物理内存 (Total Working Set) -->
<span class="text-[24px] font-data-mono font-medium text-emerald-600 dark:text-emerald-400 leading-none" id="valHeapAlloc">0.0 MB</span>
<!-- 辅助信息：Go 后端堆内存 (HeapAlloc) -->
<span class="text-[13px] text-slate-500 dark:text-slate-400 mt-1.5 flex items-center gap-1">
<span class="inline-block w-1.5 h-1.5 rounded-full bg-blue-500"></span>
<span>Go 堆内存: </span>
<span class="font-data-mono font-bold text-blue-500 dark:text-blue-400" id="valMemory">0.0 MB</span>
</span>
<span class="text-[13px] text-slate-500 dark:text-slate-400 mt-0.5"><span data-i18n="sysProcess">活跃进程:</span> <span class="font-data-mono font-bold text-on-surface dark:text-white" id="valProcessCount">0</span></span>
<span class="text-[13px] text-slate-500 dark:text-slate-400 mt-0.5"><span>CPU 占用率:</span> <span class="font-data-mono font-bold text-on-surface dark:text-white" id="valCpuUsage">0.0%</span></span>
</div>
<!-- 内存占用曲线图 -->
<div class="w-[200px] h-[45px] relative z-10 mr-1" id="memoryChartContainer">
<svg class="w-full h-full" id="memorySvg" preserveAspectRatio="none" style="overflow: visible;" viewBox="0 0 200 45">
<defs>
<linearGradient id="memoryLineGrad" x1="0%" x2="100%" y1="0%" y2="0%">
<stop offset="0%" style="stop-color:#3b82f6;"></stop>
<stop offset="100%" style="stop-color:#a855f7;"></stop>
</linearGradient>
<linearGradient id="gradMemory" x1="0%" x2="0%" y1="0%" y2="100%">
<stop offset="0%" style="stop-color:#3b82f6;stop-opacity:0.20"></stop>
<stop offset="100%" style="stop-color:#3b82f6;stop-opacity:0.0"></stop>
</linearGradient>
<linearGradient id="memoryAreaMaskGrad" x1="0%" y1="0%" x2="100%" y2="0%">
<stop offset="0%" stop-color="white" stop-opacity="0"></stop>
<stop offset="15%" stop-color="white" stop-opacity="1"></stop>
<stop offset="100%" stop-color="white" stop-opacity="1"></stop>
</linearGradient>
<mask id="memoryAreaMask">
<rect x="0" y="0" width="200" height="45" fill="url(#memoryAreaMaskGrad)"></rect>
</mask>
<filter height="140%" id="memoryGlow" width="140%" x="-20%" y="-20%">
<feGaussianBlur result="blur" stdDeviation="1.2"></feGaussianBlur>
<feMerge>
<feMergeNode in="blur"></feMergeNode>
<feMergeNode in="SourceGraphic"></feMergeNode>
</feMerge>
</filter>
</defs>
<path d="" fill="url(#gradMemory)" id="memoryChartArea" mask="url(#memoryAreaMask)"></path>
<path d="" fill="none" filter="url(#memoryGlow)" id="memoryChartPath" stroke="url(#memoryLineGrad)" stroke-linecap="round" stroke-linejoin="round" stroke-width="2.5"></path>
<circle cx="-10" cy="-10" fill="#ffffff" id="memoryChartDot" r="3" stroke="#a855f7" stroke-width="1.5" style="transition: cx 0.2s, cy 0.2s;"></circle>
</svg>
</div>
<div class="absolute -right-4 -bottom-4 text-blue-500 opacity-5 pointer-events-none">
<span class="material-symbols-outlined" style="font-size: 80px;">memory</span>
</div>
</div>
</div>
</div>
<!-- 使用趋势折线图 (SVG 矢量绘图) -->
<div class="glass-card rounded-xl p-5 flex flex-col relative">
<div class="flex justify-between items-center mb-3">
<div class="text-[11px] font-bold text-outline dark:text-outline-variant uppercase tracking-wider" data-i18n="usageTrend">使用趋势</div>
<!-- 快捷选项与筛选 -->
<div class="flex gap-1 bg-slate-100 dark:bg-white/5 p-0.5 rounded-lg text-[10px]" id="chartRangeSelector">
<button class="px-2.5 py-0.5 text-[10px] bg-white dark:bg-[#1a1f30] text-primary dark:text-primary-fixed-dim rounded-md shadow-sm font-semibold" data-range="24h">近24小时</button>
<button class="px-2.5 py-0.5 text-[10px] text-slate-500 dark:text-slate-400 hover:text-slate-800 dark:hover:text-slate-200 rounded-md transition-all font-medium" data-range="today">今日</button>
<button class="px-2.5 py-0.5 text-[10px] text-slate-500 dark:text-slate-400 hover:text-slate-800 dark:hover:text-slate-200 rounded-md transition-all font-medium" data-range="3d">近三天</button>
<button class="px-2.5 py-0.5 text-[10px] text-slate-500 dark:text-slate-400 hover:text-slate-800 dark:hover:text-slate-200 rounded-md transition-all font-medium" data-range="7d">七天</button>
<button class="px-2.5 py-0.5 text-[10px] text-slate-500 dark:text-slate-400 hover:text-slate-800 dark:hover:text-slate-200 rounded-md transition-all font-medium" data-range="30d">30天</button>
<button class="px-2.5 py-0.5 text-[10px] text-slate-500 dark:text-slate-400 hover:text-slate-800 dark:hover:text-slate-200 rounded-md transition-all font-medium flex items-center gap-0.5" data-range="filter" id="btnToggleFilterPanel">
<span>筛选</span>
<span class="material-symbols-outlined text-[12px]">filter_alt</span>
</button>
</div>
</div>
<!-- 趋势图成本汇总统计 -->
<div class="flex flex-wrap gap-2 pb-2 mb-2" id="trendCostSummary">
<span class="flex items-center gap-1.5 bg-slate-100/50 dark:bg-white/5 border border-outline-variant/20 px-2.5 py-1 rounded-lg text-[11px] md:text-[12px] whitespace-nowrap text-slate-600 dark:text-slate-400 shadow-sm">
<span class="font-medium" id="labelSummaryTotal">近24小时总成本:</span>
<span class="font-data-mono font-bold text-red-600 dark:text-red-400" id="valSummaryTotal">$0.0000</span>
</span>
<span class="flex items-center gap-1.5 bg-slate-100/50 dark:bg-white/5 border border-outline-variant/20 px-2.5 py-1 rounded-lg text-[11px] md:text-[12px] whitespace-nowrap text-slate-600 dark:text-slate-400 shadow-sm">
<span class="font-medium" data-i18n="summaryInputCost">输入总成本:</span>
<span class="font-data-mono font-bold text-blue-500 dark:text-blue-400" id="valSummaryInput">$0.0000</span>
</span>
<span class="flex items-center gap-1.5 bg-slate-100/50 dark:bg-white/5 border border-outline-variant/20 px-2.5 py-1 rounded-lg text-[11px] md:text-[12px] whitespace-nowrap text-slate-600 dark:text-slate-400 shadow-sm">
<span class="font-medium" data-i18n="summaryOutputCost">输出总成本:</span>
<span class="font-data-mono font-bold text-emerald-600 dark:text-emerald-400" id="valSummaryOutput">$0.0000</span>
</span>
<span class="flex items-center gap-1.5 bg-slate-100/50 dark:bg-white/5 border border-outline-variant/20 px-2.5 py-1 rounded-lg text-[11px] md:text-[12px] whitespace-nowrap text-slate-600 dark:text-slate-400 shadow-sm">
<span class="font-medium" data-i18n="summaryCachedCost">缓存命中成本:</span>
<span class="font-data-mono font-bold text-purple-600 dark:text-purple-400" id="valSummaryCached">$0.0000</span>
</span>
</div>
<!-- 趋势图统计概览 -->
<div class="flex flex-wrap gap-2 pb-3 border-b border-outline-variant/10 mb-3" id="trendTokenSummary">
<span class="flex items-center gap-1.5 bg-slate-100/50 dark:bg-white/5 border border-outline-variant/20 px-2.5 py-1 rounded-lg text-[11px] md:text-[12px] whitespace-nowrap text-slate-600 dark:text-slate-400 shadow-sm">
<span class="font-medium" id="labelSummaryTotalRequests">今日总请求数:</span>
<span class="font-data-mono font-bold text-orange-500" id="valSummaryTotalRequests">0</span>
</span>
<span class="flex items-center gap-1.5 bg-slate-100/50 dark:bg-white/5 border border-outline-variant/20 px-2.5 py-1 rounded-lg text-[11px] md:text-[12px] whitespace-nowrap text-slate-600 dark:text-slate-400 shadow-sm">
<span class="font-medium" id="labelSummaryTotalTokens">近24小时总 Token:</span>
<span class="font-data-mono font-bold text-slate-700 dark:text-slate-300" id="valSummaryTotalTokens">0</span>
</span>
<span class="flex items-center gap-1.5 bg-slate-100/50 dark:bg-white/5 border border-outline-variant/20 px-2.5 py-1 rounded-lg text-[11px] md:text-[12px] whitespace-nowrap text-slate-600 dark:text-slate-400 shadow-sm">
<span class="font-medium" data-i18n="summaryInputTokens">输入总 Token:</span>
<span class="font-data-mono font-bold text-blue-500 dark:text-blue-400" id="valSummaryInputTokens">0</span>
</span>
<span class="flex items-center gap-1.5 bg-slate-100/50 dark:bg-white/5 border border-outline-variant/20 px-2.5 py-1 rounded-lg text-[11px] md:text-[12px] whitespace-nowrap text-slate-600 dark:text-slate-400 shadow-sm">
<span class="font-medium" data-i18n="summaryOutputTokens">输出总 Token:</span>
<span class="font-data-mono font-bold text-emerald-600 dark:text-emerald-400" id="valSummaryOutputTokens">0</span>
</span>
<span class="flex items-center gap-1.5 bg-slate-100/50 dark:bg-white/5 border border-outline-variant/20 px-2.5 py-1 rounded-lg text-[11px] md:text-[12px] whitespace-nowrap text-slate-600 dark:text-slate-400 shadow-sm">
<span class="font-medium" data-i18n="summaryCachedTokens">缓存命中 Token:</span>
<span class="font-data-mono font-bold text-purple-600 dark:text-purple-400" id="valSummaryCachedTokens">0</span>
</span>
</div>
<!-- 多维度筛选折叠面板 -->
<div class="hidden border-b border-outline-variant/30 pb-4 mb-4 mt-1 bg-slate-50/50 dark:bg-white/5 p-4 rounded-xl flex flex-col gap-4 transition-all duration-200" id="chartFilterPanel">
<div class="text-[12px] font-bold text-on-surface dark:text-white flex items-center gap-1.5">
<span class="material-symbols-outlined text-primary text-[16px]">calendar_month</span>
<span>支持日期与时间筛选</span>
</div>
<div class="flex flex-col md:flex-row gap-4 items-end">
<!-- 开始时间 -->
<div class="flex-1 flex flex-col gap-1.5 w-full">
<span class="text-[11px] text-outline font-medium">开始时间</span>
<div class="flex gap-2 w-full">
<input class="cursor-pointer flex-grow px-3 py-1.5 text-[12px] bg-white dark:bg-[#1a1f30] border border-outline-variant/60 rounded-md focus:border-primary focus:outline-none transition-shadow text-on-surface dark:text-white font-data-mono" id="filterStartDate" onclick="this.showPicker()" type="date"/>
<input class="cursor-pointer w-24 px-3 py-1.5 text-[12px] bg-white dark:bg-[#1a1f30] border border-outline-variant/60 rounded-md focus:border-primary focus:outline-none transition-shadow text-on-surface dark:text-white font-data-mono" id="filterStartTime" onclick="this.showPicker()" type="time"/>
</div>
</div>
<!-- 结束时间 -->
<div class="flex-1 flex flex-col gap-1.5 w-full">
<span class="text-[11px] text-outline font-medium">结束时间</span>
<div class="flex gap-2 w-full">
<input class="cursor-pointer flex-grow px-3 py-1.5 text-[12px] bg-white dark:bg-[#1a1f30] border border-outline-variant/60 rounded-md focus:border-primary focus:outline-none transition-shadow text-on-surface dark:text-white font-data-mono font-bold" id="filterEndDate" onclick="this.showPicker()" type="date"/>
<input class="cursor-pointer w-24 px-3 py-1.5 text-[12px] bg-white dark:bg-[#1a1f30] border border-outline-variant/60 rounded-md focus:border-primary focus:outline-none transition-shadow text-on-surface dark:text-white font-data-mono font-bold" id="filterEndTime" onclick="this.showPicker()" type="time"/>
</div>
</div>
<!-- 动作按钮 -->
<div class="flex gap-2 justify-end w-full md:w-auto">
<button class="px-4 py-1.5 text-[12px] font-medium bg-slate-100 hover:bg-slate-200 dark:bg-white/5 dark:hover:bg-white/10 text-on-surface dark:text-white rounded-lg transition-colors border border-outline-variant/40" id="btnCancelFilter">取消</button>
<button class="px-4 py-1.5 text-[12px] font-bold bg-primary text-white hover:bg-primary/90 rounded-lg transition-colors shadow-sm" id="btnApplyFilter">确定</button>
</div>
</div>
</div>
<!-- 整合后的图表布局 -->
<div class="w-full flex flex-col relative mt-1">
<div class="flex items-stretch relative h-60">
<!-- 左侧 Y 轴 (Token 数) -->
<div class="w-12 relative select-none h-full" id="chartLeftAxis"></div>
<!-- 中间 SVG 绘图区 -->
<div class="flex-grow relative h-full">
<svg class="w-full h-full" id="trendSvg" preserveAspectRatio="none" style="overflow: visible;" viewBox="0 0 1000 300">
<defs>
<!-- 输入渐变 (淡蓝色) -->
<linearGradient id="gradInput" x1="0%" x2="0%" y1="0%" y2="100%">
<stop offset="0%" style="stop-color:#3b82f6;stop-opacity:0.25"></stop>
<stop offset="100%" style="stop-color:#3b82f6;stop-opacity:0.00"></stop>
</linearGradient>
<!-- 缓存命中渐变 (淡紫色) -->
<linearGradient id="gradCached" x1="0%" x2="0%" y1="0%" y2="100%">
<stop offset="0%" style="stop-color:#a855f7;stop-opacity:0.25"></stop>
<stop offset="100%" style="stop-color:#a855f7;stop-opacity:0.00"></stop>
</linearGradient>
</defs>
<!-- 网格虚线组 -->
<g id="chartGridLines"></g>
<!-- 渐变填充路径 -->
<path d="" fill="url(#gradInput)" id="chartAreaInput"></path>
<path d="" fill="url(#gradCached)" id="chartAreaCached"></path>
<!-- 趋势线条 -->
<!-- Cost Line (dashed red) -->
<path class="chart-line" fill="none" id="chartPathCost" stroke="#ef4444" stroke-dasharray="3,3"></path>
<!-- Requests Line (orange) -->
<path class="chart-line" fill="none" id="chartPathRequests" stroke="#f97316"></path>
<!-- Cached Line (purple) -->
<path class="chart-line" fill="none" id="chartPathCached" stroke="#a855f7"></path>
<!-- Input Line (blue) -->
<path class="chart-line" fill="none" id="chartPathInput" stroke="#3b82f6"></path>
<!-- Output Line (green) -->
<path class="chart-line" fill="none" id="chartPathOutput" stroke="#10b981"></path>
<!-- 悬停指示垂直虚线 -->
<line id="chartHoverLine" opacity="0" stroke="#94a3b8" stroke-dasharray="3,3" stroke-width="1" x1="-10" x2="-10" y1="20" y2="265"></line>
<!-- 交互感应透明矩形 -->
<rect fill="transparent" height="245" id="chartSensor" style="cursor: crosshair;" width="1000" x="0" y="20"></rect>
</svg>
<!-- 悬停焦点圆圈 (HTML 绝对定位，防止 preserveAspectRatio="none" 导致拉伸变形) -->
<div class="absolute inset-0 pointer-events-none opacity-0 transition-opacity duration-150" id="chartHoverPoints">
<div class="absolute w-2.5 h-2.5 rounded-full border-2 border-white shadow-sm -translate-x-1/2 -translate-y-1/2 bg-[#ef4444] z-10" id="hoverPointCost"></div>
<div class="absolute w-2.5 h-2.5 rounded-full border-2 border-white shadow-sm -translate-x-1/2 -translate-y-1/2 bg-[#f97316] z-10" id="hoverPointRequests"></div>
<div class="absolute w-2.5 h-2.5 rounded-full border-2 border-white shadow-sm -translate-x-1/2 -translate-y-1/2 bg-[#a855f7] z-10" id="hoverPointCached"></div>
<div class="absolute w-2.5 h-2.5 rounded-full border-2 border-white shadow-sm -translate-x-1/2 -translate-y-1/2 bg-[#3b82f6] z-10" id="hoverPointInput"></div>
<div class="absolute w-2.5 h-2.5 rounded-full border-2 border-white shadow-sm -translate-x-1/2 -translate-y-1/2 bg-[#10b981] z-10" id="hoverPointOutput"></div>
</div>
</div>
<!-- 右侧 Y 轴 (Cost 美元) -->
<div class="w-16 relative select-none h-full" id="chartRightAxis"></div>
</div>
<!-- HTML X 轴时间刻度 -->
<div class="flex pl-12 pr-16 mt-2 relative h-4">
<div class="relative w-full h-full select-none pointer-events-none" id="chartXAxis"></div>
</div>
</div>
<!-- HTML Tooltip 浮窗 -->
<div class="absolute pointer-events-none bg-white/95 dark:bg-slate-900/95 border border-slate-200 dark:border-slate-800 shadow-xl rounded-xl p-3 text-[12px] flex flex-col gap-1.5 opacity-0 transition-opacity duration-150 z-20" id="chartTooltip" style="left: -1000px; top: -1000px; min-width: 160px; backdrop-filter: blur(4px);">
<div class="font-bold text-slate-800 dark:text-slate-200 border-b border-slate-100 dark:border-slate-800 pb-1 mb-0.5" id="tooltipDate"></div>
<div class="flex items-center justify-between gap-4">
<div class="flex items-center gap-1.5">
<span class="w-2 h-2 rounded-full bg-[#f97316]"></span>
<span class="text-slate-500 dark:text-slate-400" data-i18n="tooltipRequestsLabel">请求次数:</span>
</div>
<span class="font-data-mono font-semibold text-slate-800 dark:text-slate-200" id="tooltipRequests">0</span>
</div>
<div class="flex items-center justify-between gap-4">
<div class="flex items-center gap-1.5">
<span class="w-2 h-2 rounded-full bg-[#3b82f6]"></span>
<span class="text-slate-500 dark:text-slate-400">输入:</span>
</div>
<span class="font-data-mono font-semibold text-slate-800 dark:text-slate-200" id="tooltipInput">0</span>
</div>
<div class="flex items-center justify-between gap-4">
<div class="flex items-center gap-1.5">
<span class="w-2 h-2 rounded-full bg-[#10b981]"></span>
<span class="text-slate-500 dark:text-slate-400">输出:</span>
</div>
<span class="font-data-mono font-semibold text-slate-800 dark:text-slate-200" id="tooltipOutput">0</span>
</div>
<div class="flex items-center justify-between gap-4">
<div class="flex items-center gap-1.5">
<span class="w-2 h-2 rounded-full bg-[#a855f7]"></span>
<span class="text-slate-500 dark:text-slate-400">缓存命中:</span>
</div>
<span class="font-data-mono font-semibold text-slate-800 dark:text-slate-200" id="tooltipCached">0</span>
</div>
<div class="flex items-center justify-between gap-4">
<div class="flex items-center gap-1.5">
<span class="w-2 h-2 rounded-full bg-[#ef4444]"></span>
<span class="text-slate-500 dark:text-slate-400">成本:</span>
</div>
<span class="font-data-mono font-semibold text-rose-500" id="tooltipCost">0</span>
</div>
</div>
<!-- 图例说明 -->
<div class="flex justify-center flex-wrap gap-x-5 gap-y-2 mt-3 text-[11px] text-outline select-none">
<div class="flex items-center gap-1.5">
<div class="w-2.5 h-2.5 rounded-full bg-[#f97316]"></div>
<span data-i18n="legendRequests">请求次数</span>
</div>
<div class="flex items-center gap-1.5">
<span class="w-3 h-0.5 bg-[#ef4444] border-t border-[#ef4444]"></span>
<span data-i18n="legendCost">成本 ($)</span>
</div>
<div class="flex items-center gap-1.5">
<div class="w-2.5 h-2.5 rounded-full bg-[#a855f7]"></div>
<span data-i18n="legendCached">缓存命中</span>
</div>
<div class="flex items-center gap-1.5">
<div class="w-2.5 h-2.5 rounded-full bg-[#3b82f6]"></div>
<span data-i18n="legendInput">输入</span>
</div>
<div class="flex items-center gap-1.5">
<div class="w-2.5 h-2.5 rounded-full bg-[#10b981]"></div>
<span data-i18n="legendOutput">输出</span>
</div>
</div>
</div>
<!-- 详细数据分类表 -->
<div class="glass-card rounded-xl flex flex-col flex-1 min-h-[300px]">
<!-- 页签切换 -->
<div class="flex border-b border-outline-variant px-5 pt-3">
<button class="px-4 py-2 text-[13px] font-bold text-outline hover:text-primary transition-colors border-b-2 border-transparent" id="tabModels">模型统计</button>
<button class="px-4 py-2 text-[13px] font-bold text-primary border-b-2 border-primary" id="tabLogs">请求日志</button>
<button class="px-4 py-2 text-[13px] font-bold text-outline hover:text-primary transition-colors border-b-2 border-transparent" id="tabPricing">计费配置</button>
</div>
<!-- 表格搜索控制条 -->
<div class="p-3.5 border-b border-outline-variant/30 flex justify-between items-center bg-slate-50/50 dark:bg-white/5" id="logSearchRow">
<div class="relative w-64">
<span class="material-symbols-outlined absolute left-3 top-1/2 -translate-y-1/2 text-outline text-[16px]">search</span>
<input class="w-full pl-9 pr-3 py-1.5 text-[12px] bg-white dark:bg-[#1a1f30] border border-outline-variant/60 rounded-md focus:border-primary focus:ring-1 focus:ring-primary/20 focus:outline-none transition-shadow" id="logSearchInput" placeholder="Search logs..." type="text">
</input></div>
</div>
<!-- 内容数据框 -->
<div class="flex-grow overflow-y-auto">
<!-- 模型统计面板 -->
<div class="hidden" id="modelsContent">
<table class="w-full text-left border-collapse" id="modelsTable">
<thead>
<tr class="border-b border-outline-variant/50 bg-slate-50/50 dark:bg-white/5">
<th class="p-3 text-[11px] font-bold text-outline uppercase tracking-wider" data-i18n="colModel">模型</th>
<th class="p-3 text-[11px] font-bold text-outline uppercase tracking-wider text-right" data-i18n="colRequests">请求次数</th>
<th class="p-3 text-[11px] font-bold text-outline uppercase tracking-wider text-right" data-i18n="colTotalTokens">总Tokens</th>
<th class="p-3 text-[11px] font-bold text-outline uppercase tracking-wider text-right" data-i18n="colInputTokens">输入Tokens</th>
<th class="p-3 text-[11px] font-bold text-outline uppercase tracking-wider text-right" data-i18n="colOutputTokens">输出Tokens</th>
<th class="p-3 text-[11px] font-bold text-outline uppercase tracking-wider text-right" data-i18n="colHitRate">缓存命中率</th>
<th class="p-3 text-[11px] font-bold text-outline uppercase tracking-wider text-right" data-i18n="colCost">总成本</th>
<th class="p-3 text-[11px] font-bold text-outline uppercase tracking-wider text-right" data-i18n="colAvgCost">平均成本</th>
</tr>
</thead>
<tbody class="text-[13px] font-data-mono text-on-surface dark:text-white divide-y divide-outline-variant/20">
<!-- JS 填充 -->
</tbody>
</table>
</div>
<!-- 请求日志面板 -->
<div id="logsContent">
<table class="w-full text-left table-fixed border-collapse" id="logsTable">
<thead>
<tr class="border-b border-outline-variant/50 bg-slate-50/50 dark:bg-white/5">
<th class="p-3 text-[11px] font-bold text-outline uppercase tracking-wider w-[9%]" data-i18n="colTime">请求时间</th>
<th class="p-3 text-[11px] font-bold text-outline uppercase tracking-wider w-[19%]" data-i18n="colMethodHost">请求方式 &amp; 域名</th>
<th class="p-3 text-[11px] font-bold text-outline uppercase tracking-wider w-[15%]" data-i18n="colPath">API 接口</th>
<th class="p-3 text-[11px] font-bold text-outline uppercase tracking-wider w-[9%]" data-i18n="colSession">会话 ID</th>
<th class="p-3 text-[11px] font-bold text-outline uppercase tracking-wider w-[10%]" data-i18n="colModel">所用模型</th>
<th class="p-3 text-[11px] font-bold text-outline uppercase tracking-wider text-right w-[11%]">Token 消耗</th>
<th class="p-3 text-[11px] font-bold text-outline uppercase tracking-wider text-right w-[7%]" data-i18n="colPrice">价格</th>
<th class="p-3 text-[11px] font-bold text-outline uppercase tracking-wider text-right w-[6%]" data-i18n="colDuration">耗时</th>
<th class="p-3 text-[11px] font-bold text-outline uppercase tracking-wider text-center w-[5%]" data-i18n="colHitRate">缓存</th>
<th class="p-3 text-[11px] font-bold text-outline uppercase tracking-wider text-center w-[5%]" data-i18n="colCacheStatus">状态</th>
<th class="p-3 text-[11px] font-bold text-outline uppercase tracking-wider text-center w-[4%]">操作</th>
</tr>
</thead>
<tbody class="text-[13px] font-data-mono text-on-surface dark:text-white divide-y divide-outline-variant/20">
<!-- JS 填充 -->
</tbody>
</table>
</div>
<!-- 计费配置面板 -->
<div class="hidden" id="pricingContent">
<div class="p-3.5 border-b border-outline-variant/30 flex justify-between items-center bg-slate-50/50 dark:bg-white/5">
<span class="text-[12px] font-bold text-outline dark:text-outline-variant">模型代币单价配置 (单位: USD/每百万 Tokens)</span>
<div class="flex gap-2">
<button class="px-3 py-1.5 text-[11px] bg-slate-100 hover:bg-slate-200 dark:bg-white/5 dark:hover:bg-white/10 text-on-surface dark:text-white border border-outline-variant/40 rounded-md transition-colors flex items-center gap-1" id="btnResetPricing">
<span class="material-symbols-outlined text-[14px]">restart_alt</span>
                                恢复默认
                            </button>
<button class="px-3 py-1.5 text-[11px] bg-primary text-white hover:bg-primary/90 rounded-md transition-colors flex items-center gap-1 shadow-sm font-bold" id="btnAddPricing">
<span class="material-symbols-outlined text-[14px]">add</span>
                                新增模型
                            </button>
</div>
</div>
<table class="w-full text-left border-collapse" id="pricingTable">
<thead>
<tr class="border-b border-outline-variant/50 bg-slate-50/50 dark:bg-white/5">
<th class="p-3 text-[11px] font-bold text-outline uppercase tracking-wider pl-5">模型匹配名称</th>
<th class="p-3 text-[11px] font-bold text-outline uppercase tracking-wider text-right">输入 Token 单价 (/1M)</th>
<th class="p-3 text-[11px] font-bold text-outline uppercase tracking-wider text-right">输出 Token 单价 (/1M)</th>
<th class="p-3 text-[11px] font-bold text-outline uppercase tracking-wider text-right">缓存 Token 单价 (/1M)</th>
<th class="p-3 text-[11px] font-bold text-outline uppercase tracking-wider text-center w-[15%]">操作</th>
</tr>
</thead>
<tbody class="text-[13px] font-data-mono text-on-surface dark:text-white divide-y divide-outline-variant/20">
<!-- JS 动态填充 -->
</tbody>
</table>
</div>
</div>
<!-- 表格页脚 (Mock 分页，当日志数较多时可用) -->
<div class="p-3 border-t border-outline-variant/30 flex justify-between items-center text-[12px] text-outline" id="tableFooter">
<span id="valShowingText">Showing 0 of 0 entries</span>
<div class="flex gap-1" id="paginationControls">
<!-- JS 填充 -->
</div>
</div>
</div>
</div>
</template>

<script setup lang="ts">
import { onMounted } from 'vue';

onMounted(() => {
  // Logic from dashboard controller will go here
});
</script>
